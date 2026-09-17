package relay

import (
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
	service := NewService(db)
	created, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Proxy relay", ListenPort: 9502, TargetType: TargetProxy,
		TargetProxyID: &targetID, Network: NetworkBoth, Enabled: true,
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
