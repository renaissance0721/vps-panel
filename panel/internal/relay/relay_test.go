package relay

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestRelayCRUDAndDesiredState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "203.0.113.10")
	service := NewService(db)
	created, mutation, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "TCP relay", ListenPort: 9502,
		TargetType: TargetManual, TargetHost: "example.com", TargetPort: 443,
		Network: NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID <= 0 || created.ListenAddress != "0.0.0.0" || created.EntryHostMode != EntryHostAuto ||
		created.EntryHost != "" || created.EntryAddress != "203.0.113.10" || mutation.ServerID != 1 || mutation.Version != 2 {
		t.Fatalf("created relay = %+v, mutation = %+v", created, mutation)
	}
	desired, err := service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 1 || desired[0].ListenAddress != "0.0.0.0" ||
		desired[0].TargetHost != "example.com" || desired[0].Network != NetworkTCP {
		t.Fatalf("desired relays = %+v, %v", desired, err)
	}
	listenAddress := "::"
	updated, mutation, err := service.Update(t.Context(), created.ID, UpdateInput{ListenAddress: &listenAddress})
	if err != nil || updated.ListenAddress != "::" || mutation.Version != 3 {
		t.Fatalf("updated IPv6 listener = %+v, mutation = %+v, error = %v", updated, mutation, err)
	}
	desired, err = service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 1 || desired[0].ListenAddress != "::" {
		t.Fatalf("IPv6 desired relays = %+v, %v", desired, err)
	}
	name, enabled := "Updated", false
	updated, mutation, err = service.Update(t.Context(), created.ID, UpdateInput{Name: &name, Enabled: &enabled})
	if err != nil || updated.Name != name || updated.Enabled || updated.ListenAddress != "::" || mutation.Version != 4 {
		t.Fatalf("updated relay = %+v, mutation = %+v, error = %v", updated, mutation, err)
	}
	desired, err = service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 0 {
		t.Fatalf("disabled desired relays = %+v, %v", desired, err)
	}
	mutation, err = service.Delete(t.Context(), created.ID)
	if err != nil || mutation.Version != 5 {
		t.Fatalf("delete mutation = %+v, %v", mutation, err)
	}
	if _, err := service.Get(t.Context(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted relay error = %v", err)
	}
}

func TestRelayEntryHostChangesShareEndpointWithoutChangingDesiredTarget(t *testing.T) {
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
		ServerID: 1, Name: "Proxy relay", ListenPort: 35152,
		TargetType: TargetProxy, TargetProxyID: &targetID, TargetClientID: &clientID, Network: NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EntryHostMode != EntryHostAuto || created.EntryAddress != "198.51.100.10" {
		t.Fatalf("auto Relay entry = %+v", created)
	}
	if created.TargetClientName != "Client" {
		t.Fatalf("Relay target client name = %q", created.TargetClientName)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)

	mode, host := EntryHostManual, "core.example.com"
	updated, _, err := service.Update(t.Context(), created.ID, UpdateInput{EntryHostMode: &mode, EntryHost: &host})
	if err != nil || updated.EntryAddress != host || updated.TargetHost != "203.0.113.20" {
		t.Fatalf("manual Relay entry update = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)

	host = "198.51.100.12"
	updated, _, err = service.Update(t.Context(), created.ID, UpdateInput{EntryHost: &host})
	if err != nil || updated.EntryAddress != host {
		t.Fatalf("IPv4 Relay entry update = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)

	host = "core.example.com"
	updated, _, err = service.Update(t.Context(), created.ID, UpdateInput{EntryHost: &host})
	if err != nil || updated.EntryAddress != host {
		t.Fatalf("hostname Relay entry update = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)

	host = "[2001:db8::1]"
	updated, _, err = service.Update(t.Context(), created.ID, UpdateInput{EntryHost: &host})
	if err != nil || updated.EntryAddress != "2001:db8::1" {
		t.Fatalf("IPv6 Relay entry update = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)

	mode = EntryHostAuto
	if _, _, err := service.Update(t.Context(), created.ID, UpdateInput{EntryHostMode: &mode}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = '198.51.100.11' WHERE server_id = 1`); err != nil {
		t.Fatal(err)
	}
	updated, err = service.Get(t.Context(), created.ID)
	if err != nil || updated.EntryAddress != "198.51.100.11" || updated.EntryHost != "" {
		t.Fatalf("dynamic auto Relay entry = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)
	otherClientID := int64(21)
	if _, err := db.Exec(`INSERT INTO clients (id, proxy_id, name, credential_json, created_at, updated_at) VALUES (21, 10, 'Other', '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	updated, _, err = service.Update(t.Context(), created.ID, UpdateInput{TargetClientID: &otherClientID})
	if err != nil || updated.TargetClientID == nil || *updated.TargetClientID != otherClientID {
		t.Fatalf("selected Relay client = %+v, %v", updated, err)
	}
	assertRelayDesiredTarget(t, service, db, "203.0.113.20", 443)
	if _, err := db.Exec(`DELETE FROM clients WHERE id = ?`, otherClientID); err != nil {
		t.Fatal(err)
	}
	enabled := false
	updated, _, err = service.Update(t.Context(), created.ID, UpdateInput{Enabled: &enabled})
	if err != nil || updated.TargetClientID != nil || updated.Enabled {
		t.Fatalf("legacy Relay with no selected Client = %+v, %v", updated, err)
	}
}

func TestRelayAutoEntryCanBeUnavailableWithoutAffectingDesiredState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "")
	service := NewService(db)
	created, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Manual target", ListenPort: 9502,
		TargetType: TargetManual, TargetHost: "target.example.com", TargetPort: 443,
		Network: NetworkTCP, Enabled: true,
	})
	if err != nil || created.EntryAddress != "" || !created.TargetAddressReady {
		t.Fatalf("Relay with unavailable auto entry = %+v, %v", created, err)
	}
	assertRelayDesiredTarget(t, service, db, "target.example.com", 443)
}
