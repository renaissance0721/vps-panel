package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestPersonalRemoteMigrationPreservesNodesBindingsAndSequence(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err = configure(db); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:37] {
		if err = applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'owner','hash','vip',1,1)`,
		`INSERT INTO personal_subscription_groups(id,owner_user_id,name,token,client_name,routing_preset_id,routing_bindings_json,created_at,updated_at) SELECT 1,1,'p','synthetic','owner',id,'{"group":[10]}',1,1 FROM subscription_routing_presets WHERE is_default=1`,
		`INSERT INTO personal_subscription_nodes(id,group_id,source_type,source_id,display_name,position,created_at,updated_at) VALUES(10,1,'proxy',1,'Local',1,1,1),(90,1,'landing',1,'Deleted',2,1,1)`,
		`DELETE FROM personal_subscription_nodes WHERE id=90`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertForeignKeysValid(t, db)
	var bindings string
	if err = db.QueryRow(`SELECT routing_bindings_json FROM personal_subscription_groups WHERE id=1`).Scan(&bindings); err != nil || bindings != "{\"group\":[10]}" {
		t.Fatalf("bindings %q: %v", bindings, err)
	}
	var id int64
	if err = db.QueryRow(`SELECT id FROM personal_subscription_nodes WHERE display_name='Local'`).Scan(&id); err != nil || id != 10 {
		t.Fatalf("node %d: %v", id, err)
	}
	res, err := db.Exec(`INSERT INTO personal_subscription_nodes(group_id,source_type,source_id,display_name,position,created_at,updated_at) VALUES(1,'remote_source',1,'Remote',2,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err = res.LastInsertId()
	if err != nil || id <= 90 {
		t.Fatalf("sequence %d: %v", id, err)
	}
}
