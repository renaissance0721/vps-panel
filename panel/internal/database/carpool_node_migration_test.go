package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCarpoolAssignmentsMigrationKeepsDuplicateClients(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = configure(db); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:31] {
		if err = applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'member','hash','carpool',1,1)`,
		`INSERT INTO servers(id,name,created_by_role,status,created_at,updated_at) VALUES(1,'server','admin','offline',1,1)`,
		`INSERT INTO proxies(id,server_id,name,protocol,listen_port,config_json,created_at,updated_at) VALUES(1,1,'proxy','vless',443,'{}',1,1)`,
		`INSERT INTO clients(id,proxy_id,assigned_user_id,name,credential_json,created_at,updated_at) VALUES(10,1,1,'first','{}',1,1),(20,1,1,'second','{}',2,2)`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = applyMigration(t.Context(), db, migrations[31]); err != nil {
		t.Fatal(err)
	}
	var count, clientID, clients int
	if err = db.QueryRow(`SELECT COUNT(*),MIN(client_id) FROM carpool_node_assignments WHERE user_id=1 AND source_type='proxy' AND source_id=1`).Scan(&count, &clientID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM clients WHERE assigned_user_id=1`).Scan(&clients); err != nil {
		t.Fatal(err)
	}
	if count != 1 || clientID != 10 || clients != 2 {
		t.Fatalf("assignments=%d client=%d clients=%d", count, clientID, clients)
	}
	assertForeignKeysValid(t, db)
}
