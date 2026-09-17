package relay

import (
	"database/sql"
	"testing"
)

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
