package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestPasswordEmailMigrationPreservesTokensAndSeparatesActivePurposes(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(directory, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:26] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO users (id,username,password_hash,role,email,email_verified_at,created_at,updated_at) VALUES (1,'admin','hash','admin','admin@example.com',1,1,1)`,
		`INSERT INTO sessions (user_id,token_hash,expires_at,created_at) VALUES (1,'session-hash',9999999999,1)`,
		`INSERT INTO account_tokens (id,user_id,purpose,target,token_hash,expires_at,used_at,created_at) VALUES
		 (1,1,'verify_email','next@example.com','verification-hash',9999999999,NULL,1),
		 (2,1,'change_email','old@example.com','used-hash',9,8,1),
		 (99,1,'verify_email','deleted@example.com','deleted-hash',9,8,1)`,
		`DELETE FROM account_tokens WHERE id=99`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyMigration(t.Context(), db, migrations[26]); err != nil {
		t.Fatal(err)
	}
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	var target, hash string
	var used sql.NullInt64
	if err := db.QueryRow(`SELECT target,token_hash,used_at FROM account_tokens WHERE id=1`).Scan(&target, &hash, &used); err != nil || target != "next@example.com" || hash != "verification-hash" || used.Valid {
		t.Fatal("migration changed active verification token")
	}
	if err := db.QueryRow(`SELECT used_at FROM account_tokens WHERE id=2`).Scan(&used); err != nil || !used.Valid || used.Int64 != 8 {
		t.Fatal("migration changed used token")
	}
	created, err := db.Exec(`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,created_at) VALUES (1,'reset_password','admin@example.com','reset-hash',9999999999,1)`)
	if err != nil {
		t.Fatalf("reset cannot coexist with verification: %v", err)
	}
	if id, err := created.LastInsertId(); err != nil || id != 100 {
		t.Fatal("token sequence was not preserved")
	}
	for _, statement := range []string{
		`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,created_at) VALUES (1,'reset_password','admin@example.com','second-reset',99,1)`,
		`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,created_at) VALUES (1,'change_email','another@example.com','second-verification',99,1)`,
		`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,created_at) VALUES (1,'unknown','admin@example.com','unknown',99,1)`,
		`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,created_at) VALUES (9,'reset_password','admin@example.com','orphan',99,1)`,
		`INSERT INTO account_tokens (user_id,purpose,target,token_hash,expires_at,used_at,created_at) VALUES (1,'reset_password','admin@example.com','reset-hash',99,1,1)`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatal("new schema accepted invalid token")
		}
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration lost session")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_account_tokens_active_email','idx_account_tokens_active_password_reset','idx_account_tokens_expires_at')`).Scan(&count); err != nil || count != 3 {
		t.Fatal("token indexes missing")
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatal("token ownership cascade changed")
	}
}
