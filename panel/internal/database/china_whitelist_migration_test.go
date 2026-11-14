package database_test

import (
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestChinaWhitelistMigrationPreservesServers(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := server.NewService(db).Create(t.Context(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE servers SET block_china_inbound = 1, desired_state_version = 8 WHERE id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE server_china_inbound_whitelist`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version = 40`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enabled bool
	var version, count int
	if err = db.QueryRow(`SELECT block_china_inbound, desired_state_version FROM servers WHERE id = ?`, created.ID).Scan(&enabled, &version); err != nil || !enabled || version != 8 {
		t.Fatalf("server migration: %t %d %v", enabled, version, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM server_china_inbound_whitelist WHERE server_id = ?`, created.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("initial whitelist: %d %v", count, err)
	}
}
