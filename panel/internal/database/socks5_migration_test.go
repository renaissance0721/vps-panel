package database_test

import (
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestSOCKS5MigrationPreservesReferencesAndSequence(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1,'owner','hash','vip',1,1)`,
		`INSERT INTO servers (id,name,status,created_at,updated_at) VALUES (1,'source','offline',1,1)`,
		`INSERT INTO landing_nodes (id,owner_user_id,name,visibility,protocol,host,port,uri,created_at,updated_at,node_role) VALUES (5,1,'old','private','vless','example.com',443,'vless://fake@example.com:443',1,2,'landing')`,
		`INSERT INTO user_landing_order (user_id,landing_id,position) VALUES (1,5,1)`,
		`INSERT INTO relays (id,server_id,name,listen_address,listen_port,target_type,target_landing_id,network,enabled,created_at,updated_at) VALUES (1,1,'relay','0.0.0.0',9000,'landing',5,'tcp',1,1,1)`,
		`PRAGMA foreign_keys=OFF`,
		`CREATE TABLE old_landing (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_user_id INTEGER NOT NULL REFERENCES users(id), name TEXT NOT NULL, visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')), protocol TEXT NOT NULL CHECK (protocol IN ('vless','shadowsocks')), host TEXT NOT NULL, port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535), uri TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, node_role TEXT NOT NULL DEFAULT 'direct' CHECK (node_role IN ('direct','landing')))`,
		`INSERT INTO old_landing SELECT * FROM landing_nodes`,
		`DROP TABLE landing_nodes`,
		`ALTER TABLE old_landing RENAME TO landing_nodes`,
		`CREATE INDEX idx_landing_nodes_owner_user_id ON landing_nodes(owner_user_id)`,
		`CREATE INDEX idx_landing_nodes_visibility ON landing_nodes(visibility)`,
		`UPDATE sqlite_sequence SET seq=100 WHERE name='landing_nodes'`,
		`DELETE FROM schema_migrations WHERE version>=42`,
		`PRAGMA foreign_keys=ON`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM landing_nodes l JOIN relays r ON r.target_landing_id=l.id JOIN user_landing_order o ON o.landing_id=l.id WHERE l.id=5 AND l.uri='vless://fake@example.com:443' AND l.node_role='landing'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("references and data not preserved: %d %v", count, err)
	}
	result, err := db.Exec(`INSERT INTO landing_nodes (owner_user_id,name,protocol,host,port,uri,created_at,updated_at) VALUES (1,'socks','socks5','203.0.113.10',9492,'socks5://user:pass@203.0.113.10:9492',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 100 {
		t.Fatalf("sequence: %d %v", id, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5MigrationPreservesEmptyTableSequence(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (1,'owner','hash','vip',1,1)`,
		`INSERT INTO landing_nodes (id,owner_user_id,name,protocol,host,port,uri,created_at,updated_at) VALUES (100,1,'old','vless','example.com',443,'vless://fake@example.com:443',1,1)`,
		`DELETE FROM landing_nodes`,
		`DELETE FROM schema_migrations WHERE version>=42`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO landing_nodes (owner_user_id,name,protocol,host,port,uri,created_at,updated_at) VALUES (1,'new','socks5','203.0.113.10',9492,'socks5://user:pass@203.0.113.10:9492',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 100 {
		t.Fatalf("empty table sequence: %d %v", id, err)
	}
}
