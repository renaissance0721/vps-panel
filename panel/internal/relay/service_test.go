package relay

import (
	"database/sql"
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
	if err != nil || len(desired) != 1 || desired[0].TargetHost != "example.com" || desired[0].Network != NetworkTCP {
		t.Fatalf("desired relays = %+v, %v", desired, err)
	}
	name, enabled := "Updated", false
	updated, mutation, err := service.Update(t.Context(), created.ID, UpdateInput{Name: &name, Enabled: &enabled})
	if err != nil || updated.Name != name || updated.Enabled || mutation.Version != 3 {
		t.Fatalf("updated relay = %+v, mutation = %+v, error = %v", updated, mutation, err)
	}
	desired, err = service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 0 {
		t.Fatalf("disabled desired relays = %+v, %v", desired, err)
	}
	mutation, err = service.Delete(t.Context(), created.ID)
	if err != nil || mutation.Version != 4 {
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
	service := NewService(db)
	created, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "Proxy relay", ListenPort: 35152,
		TargetType: TargetProxy, TargetProxyID: &targetID, Network: NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EntryHostMode != EntryHostAuto || created.EntryAddress != "198.51.100.10" {
		t.Fatalf("auto Relay entry = %+v", created)
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

func TestRelayProtocolAwarePortConflicts(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "203.0.113.1")
	if _, err := db.Exec(`INSERT INTO proxies
		(server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		VALUES (1, 'TCP Proxy', 'vless', 443, '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	udp, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "UDP same port", ListenPort: 443, TargetType: TargetManual,
		TargetHost: "1.1.1.1", TargetPort: 53, Network: NetworkUDP, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UDP should coexist with TCP: %v", err)
	}
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "TCP conflict", ListenPort: 443, TargetType: TargetManual,
		TargetHost: "example.com", TargetPort: 443, Network: NetworkTCP, Enabled: true,
	}); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("TCP conflict error = %v", err)
	}
	if err := ProxyPortAvailable(t.Context(), db, 1, 443, "vless"); err != nil {
		t.Fatalf("TCP Proxy should coexist with UDP Relay: %v", err)
	}
	if err := ProxyPortAvailable(t.Context(), db, 1, 443, "shadowsocks"); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("TCP+UDP Proxy conflict error = %v", err)
	}
	if udp.Network != NetworkUDP {
		t.Fatalf("created UDP relay = %+v", udp)
	}
}

func TestRelayValidationRejectsUnsafeValues(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "")
	service := NewService(db)
	tests := []struct {
		input CreateInput
		want  error
	}{
		{CreateInput{ServerID: 1, Name: "", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidName},
		{CreateInput{ServerID: 1, Name: "x", ListenAddress: "$(bad)", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidListenIP},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 0, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidPort},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, EntryHostMode: "guess", TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidEntryHostMode},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, EntryHostMode: EntryHostManual, EntryHost: "https://bad", TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidEntryHost},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, TargetType: TargetManual, TargetHost: "https://bad", TargetPort: 1, Network: NetworkTCP}, ErrInvalidTarget},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: "icmp"}, ErrInvalidNetwork},
	}
	for _, test := range tests {
		if _, _, err := service.Create(t.Context(), test.input); !errors.Is(err, test.want) {
			t.Fatalf("Create(%+v) error = %v, want %v", test.input, err, test.want)
		}
	}
}

func assertRelayDesiredTarget(t *testing.T, service *Service, db *sql.DB, host string, port int) {
	t.Helper()
	desired, err := service.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 1 || desired[0].TargetHost != host || desired[0].TargetPort != port {
		t.Fatalf("desired Relay target = %+v, %v; want %s:%d", desired, err, host, port)
	}
}

func insertRelayTestServer(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, id int64, name, publicIPv4 string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (?, ?, 'offline', 1, 1)`, id, name); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (?, '', '', '', '', '', '[]', '[]', ?, '', 1)`, id, publicIPv4); err != nil {
		t.Fatal(err)
	}
}

func desiredVersion(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, serverID int64) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
