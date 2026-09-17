package proxy

import (
	"database/sql"
	"encoding/base64"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func newTestService(t *testing.T) (*sql.DB, *Service, int64) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	result, err := db.Exec(`INSERT INTO servers (name, status, created_at, updated_at) VALUES ('test', 'offline', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return db, NewService(db), id
}

func createRealityProxy(t *testing.T, service *Service, serverID int64, port int, name string) Proxy {
	t.Helper()
	value, _, err := service.Create(t.Context(), CreateInput{ServerID: serverID, Name: name, ListenPort: port, EntryHostMode: EntryHostAuto, Enabled: true, Security: SecurityReality, ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "默认客户端"})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func getProxyForTest(service *Service, id int64) (Proxy, storedConfig, error) {
	row := service.db.QueryRow(`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		proxies.config_json, proxies.created_at, proxies.updated_at
		FROM proxies JOIN servers ON servers.id = proxies.server_id
		LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id WHERE proxies.id = ?`, id)
	return scanProxy(row)
}

func assertCounts(t *testing.T, db *sql.DB, proxies, clients int) {
	t.Helper()
	var proxyCount, clientCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&proxyCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&clientCount)
	if proxyCount != proxies || clientCount != clients {
		t.Fatalf("counts = proxies %d clients %d", proxyCount, clientCount)
	}
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
