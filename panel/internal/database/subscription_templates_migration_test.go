package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestClientTemplateMigrationPreservesContentReferencesAndSequence(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:18] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	content := "# 原有注释\r\ndns:\r\n  enable: false\r\n"
	if _, err := db.Exec(`INSERT INTO subscription_templates (id,name,enabled,config_yaml,created_at,updated_at)
		VALUES (7,'原模板',0,?,123,456),(99,'deleted',1,'dns: {}',1,1)`, content); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DELETE FROM subscription_templates WHERE id = 99`,
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (1,'admin','test-hash','admin',1,1)`,
		`INSERT INTO subscription_plans (id,name,enabled,template_id,routing_preset_id,created_at,updated_at)
		 SELECT 2,'plan',1,7,id,123,456 FROM subscription_routing_presets WHERE is_default = 1`,
		`INSERT INTO personal_subscription_groups (id,owner_user_id,name,token,client_name,mihomo_template_id,routing_preset_id,created_at,updated_at)
		 SELECT 3,1,'personal','migration-test-token','client',7,id,123,456 FROM subscription_routing_presets WHERE is_default = 1`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	var name, kind, got string
	var enabled, created, updated int64
	if err := db.QueryRow(`SELECT name,type,content,enabled,created_at,updated_at FROM subscription_templates WHERE id = 7`).Scan(&name, &kind, &got, &enabled, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if name != "原模板" || kind != "mihomo" || got != content || enabled != 0 || created != 123 || updated != 456 {
		t.Fatal("existing template changed")
	}
	for _, query := range []string{
		`SELECT template_id, shadowrocket_template_id FROM subscription_plans WHERE id = 2`,
		`SELECT mihomo_template_id, shadowrocket_template_id FROM personal_subscription_groups WHERE id = 3`,
	} {
		var mihomoID int64
		var shadowrocketID sql.NullInt64
		if err := db.QueryRow(query).Scan(&mihomoID, &shadowrocketID); err != nil || mihomoID != 7 || shadowrocketID.Valid {
			t.Fatal("template references changed", err)
		}
	}
	result, err := db.Exec(`INSERT INTO subscription_templates (name,type,enabled,content,created_at,updated_at) VALUES ('Shadowrocket','shadowrocket',1,'test',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil || id != 100 {
		t.Fatal("sequence changed", err)
	}
	for _, table := range []string{"subscription_plans", "personal_subscription_groups"} {
		if _, err := db.Exec(`UPDATE ` + table + ` SET shadowrocket_template_id = 999`); err == nil {
			t.Fatal("invalid foreign key accepted")
		}
		if _, err := db.Exec(`UPDATE `+table+` SET shadowrocket_template_id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM subscription_templates WHERE id = ?`, id); err == nil {
		t.Fatal("referenced template deleted")
	}
	if _, err := db.Exec(`UPDATE subscription_templates SET type = 'surge' WHERE id = ?`, id); err == nil {
		t.Fatal("unknown client type accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
}

func TestFreshDatabaseHasTypedTemplatesAndIndependentReferences(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	for _, column := range []struct{ table, name string }{
		{"subscription_templates", "type"}, {"subscription_templates", "content"},
		{"subscription_plans", "shadowrocket_template_id"}, {"personal_subscription_groups", "shadowrocket_template_id"},
	} {
		if exists, err := migrationColumnExists(t.Context(), db, column.table, column.name); err != nil || !exists {
			t.Fatal("missing migrated column", column, err)
		}
	}
	if exists, err := migrationColumnExists(t.Context(), db, "subscription_templates", "config_yaml"); err != nil || exists {
		t.Fatal("legacy column remains", err)
	}
	assertForeignKeysValid(t, db)
}
