package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestServerAccessMigrationPreservesManagersAndOwner(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range migrations[:16] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1, 'admin', 'hash', 'admin', 1, 1), (2, 'vip', 'hash', 'vip', 1, 1),
		 (3, 'user', 'hash', 'user', 1, 1), (4, 'subscriber', 'hash', 'subscriber', 1, 1)`,
		`INSERT INTO servers (id, name, owner_user_id, visibility, status, created_at, updated_at)
		 VALUES (1, 'Private', 3, 'private', 'pending', 1, 1)`,
		`INSERT INTO server_access (server_id, user_id) VALUES (1, 1), (1, 2), (1, 3), (1, 4)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		db, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var ids string
		if err := db.QueryRow(`SELECT group_concat(user_id) FROM
			(SELECT user_id FROM server_access WHERE server_id = 1 ORDER BY user_id)`).Scan(&ids); err != nil || ids != "1,2" {
			db.Close()
			t.Fatalf("remaining access IDs = %q, error %v", ids, err)
		}
		var userCount, ownerID int
		if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil || userCount != 4 {
			db.Close()
			t.Fatalf("migration changed users: count=%d, error %v", userCount, err)
		}
		if err := db.QueryRow(`SELECT owner_user_id FROM servers WHERE id = 1`).Scan(&ownerID); err != nil || ownerID != 3 {
			db.Close()
			t.Fatalf("migration changed owner: id=%d, error %v", ownerID, err)
		}
		assertLatestMigrationHistory(t, db)
		assertForeignKeysValid(t, db)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
