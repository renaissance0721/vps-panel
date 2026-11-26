package database_test

import (
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestProxyKernelMigrationPreservesLegacyProxy(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (id, name, visibility, created_by_role, status, created_at, updated_at) VALUES (1, 'server', 'public', 'admin', 'online', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	service := proxy.NewService(db)
	created, _, err := service.Create(t.Context(), proxy.CreateInput{
		ServerID: 1, Name: "legacy", ListenPort: 443, EntryHostMode: proxy.EntryHostManual,
		EntryHost: "node.example.com", Enabled: true, Security: proxy.SecurityReality,
		ServerName: "example.com", RealityTarget: "example.com:443", FirstClientName: "client",
	})
	if err != nil {
		t.Fatal(err)
	}
	var config, credential string
	if err := db.QueryRow(`SELECT config_json, (SELECT credential_json FROM clients WHERE proxy_id = proxies.id) FROM proxies WHERE id = ?`, created.ID).Scan(&config, &credential); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE proxies DROP COLUMN kernel`,
		`DELETE FROM schema_migrations WHERE version >= 49`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := proxy.NewService(db).Get(t.Context(), created.ID)
	if err != nil || got.Kernel != proxy.KernelXray || got.Protocol != created.Protocol || len(got.Clients) != 1 {
		t.Fatalf("migrated proxy: %+v, %v", got, err)
	}
	var gotConfig, gotCredential string
	if err := db.QueryRow(`SELECT config_json, (SELECT credential_json FROM clients WHERE proxy_id = proxies.id) FROM proxies WHERE id = ?`, created.ID).Scan(&gotConfig, &gotCredential); err != nil {
		t.Fatal(err)
	}
	if gotConfig != config || gotCredential != credential {
		t.Fatal("migration changed proxy configuration or client credentials")
	}
	if _, err := db.Exec(`UPDATE proxies SET kernel = 'invalid' WHERE id = ?`, created.ID); err == nil {
		t.Fatal("invalid kernel accepted")
	}
	if _, err := proxy.NewService(db).GetClientShare(t.Context(), got.Clients[0].ID); err != nil {
		t.Fatalf("legacy client share: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO proxies(server_id,name,protocol,kernel,listen_port,enabled,config_json,created_at,updated_at) VALUES (1,'hy2','hysteria2','sing-box',443,1,'{}',1,1)`); err != nil {
		t.Fatalf("UDP listener should coexist with legacy TCP listener: %v", err)
	}
	var network string
	if err := db.QueryRow(`SELECT network FROM server_listener_reservations WHERE resource_type='proxy' AND port=443 AND resource_id != ?`, created.ID).Scan(&network); err != nil || network != "udp" {
		t.Fatalf("HY2 listener reservation = %q, %v", network, err)
	}
	if _, err := db.Exec(`INSERT INTO proxies(server_id,name,protocol,kernel,listen_port,enabled,config_json,created_at,updated_at) VALUES (1,'conflict','hysteria2','sing-box',443,1,'{}',1,1)`); err == nil {
		t.Fatal("duplicate UDP listener accepted")
	}
}
