package relay

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestRelayProxyTargetResolutionAndDependencies(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "198.51.100.10")
	insertRelayTestServer(t, db, 2, "Target", "203.0.113.20")
	if _, err := db.Exec(`INSERT INTO proxies
		(id, server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		VALUES (10, 2, 'Target Proxy', 'vless', 443, 'auto', '', 1, '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	targetID := int64(10)
	clientID := int64(20)
	if _, err := db.Exec(`INSERT INTO clients (id, proxy_id, name, credential_json, created_at, updated_at) VALUES (20, 10, 'Client', '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	created, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Proxy relay", ListenPort: 9502, TargetType: TargetProxy,
		TargetProxyID: &targetID, TargetClientID: &clientID, Network: NetworkBoth, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TargetHost != "203.0.113.20" || created.TargetPort != 443 || !created.TargetAddressReady {
		t.Fatalf("resolved relay = %+v", created)
	}
	desired, err := service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 1 || desired[0].TargetHost != "203.0.113.20" || desired[0].TargetPort != 443 {
		t.Fatalf("desired proxy target = %+v, %v", desired, err)
	}
	before := desiredVersion(t, db, 1)
	mutations, err := service.BumpForProxyTarget(t.Context(), 10, 2)
	if err != nil || len(mutations) != 1 || mutations[0].ServerID != 1 || mutations[0].Version != before+1 {
		t.Fatalf("proxy dependency mutations = %+v, %v", mutations, err)
	}
	mutations, err = service.BumpForAutoTargetServer(t.Context(), 2)
	if err != nil || len(mutations) != 1 || mutations[0].Version != before+2 {
		t.Fatalf("public IPv4 dependency mutations = %+v, %v", mutations, err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = '' WHERE server_id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListDesired(t.Context(), db, 1); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("missing public IPv4 error = %v", err)
	}
	if _, err := db.Exec(`UPDATE proxies SET entry_host_mode = 'manual', entry_host = 'node.example.com' WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	desired, err = service.ListDesired(t.Context(), db, 1)
	if err != nil || desired[0].TargetHost != "node.example.com" {
		t.Fatalf("manual proxy target = %+v, %v", desired, err)
	}
}

func TestRelayLandingTargetResolutionDesiredStateAndDependencies(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "198.51.100.10")
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (1, 'owner', 'hash', 'vip', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landing_nodes
		(id, owner_user_id, name, visibility, protocol, host, port, uri, created_at, updated_at)
		VALUES (10, 1, 'US Home', 'private', 'vless', 'landing.example.com', 443, 'secret', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agents
		(server_id, token_hash, version, registered_at, config_sync_status, created_at, updated_at)
		VALUES (1, 'token', 'v1', 1, 'success', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	landingID := int64(10)
	unusedProxyID, unusedClientID := int64(999), int64(998)
	service := NewService(db)
	created, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Landing relay", ListenPort: 9502, TargetType: TargetLanding,
		TargetLandingID: &landingID, TargetProxyID: &unusedProxyID, TargetClientID: &unusedClientID,
		TargetHost: "ignored.example.com", TargetPort: 8443, Network: NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TargetLandingID == nil || *created.TargetLandingID != landingID || created.TargetLandingName != "US Home" ||
		created.TargetLandingProtocol != "vless" || created.TargetProxyID != nil || created.TargetClientID != nil ||
		created.TargetHost != "landing.example.com" || created.TargetPort != 443 || !created.TargetAddressReady {
		t.Fatalf("created landing Relay = %+v", created)
	}
	assertRelayDesiredTarget(t, service, db, "landing.example.com", 443)
	var storedProxyID, storedClientID, storedPort sql.NullInt64
	var storedHost string
	if err := db.QueryRow(`SELECT target_proxy_id, target_client_id, target_host, target_port FROM relays WHERE id = ?`, created.ID).
		Scan(&storedProxyID, &storedClientID, &storedHost, &storedPort); err != nil {
		t.Fatal(err)
	}
	if storedProxyID.Valid || storedClientID.Valid || storedHost != "" || storedPort.Valid {
		t.Fatalf("landing Relay retained unrelated target fields")
	}

	before := desiredVersion(t, db, 1)
	mutations, err := service.BumpForLandingTarget(t.Context(), landingID)
	if err != nil || len(mutations) != 1 || mutations[0].Version != before+1 {
		t.Fatalf("landing dependency mutations = %+v, %v", mutations, err)
	}
	var syncStatus string
	if err := db.QueryRow(`SELECT config_sync_status FROM agents WHERE server_id = 1`).Scan(&syncStatus); err != nil || syncStatus != "pending" {
		t.Fatalf("Agent sync status = %q, %v", syncStatus, err)
	}
	if _, err := db.Exec(`UPDATE landing_nodes SET host = 'new.example.com', port = 8443 WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	assertRelayDesiredTarget(t, service, db, "new.example.com", 8443)
	if _, err := db.Exec(`DELETE FROM landing_nodes WHERE id = 10`); err == nil {
		t.Fatal("referenced landing deletion unexpectedly succeeded")
	}
	missingID := int64(999)
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Missing", ListenPort: 9503, TargetType: TargetLanding,
		TargetLandingID: &missingID, Network: NetworkTCP, Enabled: true,
	}); !errors.Is(err, ErrLandingNotFound) {
		t.Fatalf("missing landing error = %v", err)
	}
}
