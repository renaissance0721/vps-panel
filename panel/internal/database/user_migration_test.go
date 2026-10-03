package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func openVersion13Users(t *testing.T, directory string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(directory, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	// Explicit old schema: do not derive the collation from today's fresh schema.
	if _, err := db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL COLLATE NOCASE UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('admin', 'vip', 'user', 'subscriber')),
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:13] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DROP TABLE user_account_order`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestUsernameMigrationPreservesUsersAndEveryForeignKey(t *testing.T) {
	directory := t.TempDir()
	db := openVersion13Users(t, directory)
	for _, statement := range []string{
		`INSERT INTO users VALUES (7,'refrain','original-hash','admin',123,456),(8,'member','member-hash','subscriber',234,567),(99,'deleted','hash','vip',1,1)`,
		`DELETE FROM users WHERE id = 99`,
		`INSERT INTO sessions (user_id,token_hash,expires_at,created_at) VALUES (7,'session-hash',9999999999,123)`,
		`INSERT INTO admin_invitations (token_hash,created_by,expires_at,role,created_at) VALUES ('invite-hash',7,9999999999,'vip',123)`,
		`INSERT INTO password_change_requests (user_id,proposed_password_hash,status,created_at,reviewed_by,reviewed_at) VALUES (8,NULL,'approved',123,7,456)`,
		`INSERT INTO servers (id,name,owner_user_id,created_by_user_id,created_by_role,status,created_at,updated_at) VALUES (1,'server',7,7,'admin','offline',123,456)`,
		`INSERT INTO server_access VALUES (1,7)`,
		`INSERT INTO user_server_order VALUES (7,1,1)`,
		`INSERT INTO proxies (id,server_id,name,protocol,listen_port,config_json,created_at,updated_at) VALUES (1,1,'proxy','vless',443,'{}',123,456)`,
		`INSERT INTO user_proxy_order VALUES (7,1,1)`,
		`INSERT INTO clients (id,proxy_id,assigned_user_id,name,credential_json,created_at,updated_at) VALUES (1,1,8,'client','{}',123,456)`,
		`INSERT INTO landing_nodes (id,owner_user_id,name,protocol,host,port,uri,created_at,updated_at) VALUES (1,7,'landing','vless','example.com',443,'uri',123,456)`,
		`INSERT INTO user_landing_order VALUES (7,1,1)`,
		`INSERT INTO relays (id,server_id,owner_user_id,name,listen_port,target_type,target_host,target_port,network,created_at,updated_at) VALUES (1,1,7,'relay',20000,'manual','example.com',443,'tcp',123,456)`,
		`INSERT INTO user_relay_order VALUES (7,1,1)`,
		`INSERT INTO subscription_plans (id,name,enabled,created_at,updated_at) VALUES (1,'plan',1,123,456)`,
		`INSERT INTO subscriber_profiles (user_id,plan_id,subscription_token,created_at,updated_at) VALUES (8,1,'unchanged-subscription-token',123,456)`,
		`INSERT INTO subscriber_clients (user_id,proxy_id,client_id,created_at) VALUES (8,1,1,123)`,
		`INSERT INTO subscriber_usage (user_id,cycle_started_at,updated_at) VALUES (8,123,456)`,
		`INSERT INTO personal_subscription_groups (owner_user_id,name,token,client_name,routing_preset_id,created_at,updated_at)
		 SELECT 7,'personal','unchanged-personal-token','client',id,123,456 FROM subscription_routing_presets WHERE is_default = 1`,
		`INSERT INTO audit_logs (created_at,actor_user_id,actor_username,action,resource_type,resource_id,summary,request_id) VALUES (123,7,'refrain','user.create','user',8,'created','request')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed old schema: %v; %s", err, statement)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (username,password_hash,role,created_at,updated_at) VALUES ('Refrain','hash','vip',1,1)`); err == nil {
		t.Fatal("fixture was not case insensitive")
	}
	// Require a populated reference for every users FK in the actual schema.
	rows, err := db.Query(`SELECT m.name, fk."from" FROM sqlite_master m, pragma_foreign_key_list(m.name) fk
		WHERE m.type = 'table' AND fk."table" = 'users'`)
	if err != nil {
		t.Fatal(err)
	}
	type reference struct{ table, column string }
	var references []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.table, &ref.column); err != nil {
			t.Fatal(err)
		}
		references = append(references, ref)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	tables := map[string][][]any{"users": snapshotUserMigrationTable(t, db, "users")}
	for _, ref := range references {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + ref.table + `" WHERE "` + ref.column + `" IS NOT NULL`).Scan(&count); err != nil || count == 0 {
			t.Fatalf("uncovered users reference %s.%s: %d, %v", ref.table, ref.column, count, err)
		}
		tables[ref.table] = snapshotUserMigrationTable(t, db, ref.table)
	}
	assertForeignKeysValid(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	for table, before := range tables {
		if after := snapshotUserMigrationTable(t, db, table); !reflect.DeepEqual(before, after) {
			t.Fatalf("migration changed rows in %s", table)
		}
	}
	result, err := db.Exec(`INSERT INTO users (username,password_hash,role,created_at,updated_at) VALUES ('Refrain','hash','vip',1,1)`)
	if err != nil {
		t.Fatalf("case variant after upgrade: %v", err)
	}
	if id, err := result.LastInsertId(); err != nil || id != 100 {
		t.Fatalf("AUTOINCREMENT after rebuild = %d, %v", id, err)
	}
	if _, err := db.Exec(`INSERT INTO users (username,password_hash,role,created_at,updated_at) VALUES ('refrain','hash','vip',1,1)`); err == nil {
		t.Fatal("exact duplicate accepted")
	}
	var enabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys not restored: %d, %v", enabled, err)
	}
}

func snapshotUserMigrationTable(t *testing.T, db *sql.DB, table string) [][]any {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM "` + table + `" ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		// v19 adds a nullable client-template reference after the v13 snapshot.
		// Check its default separately while comparing every original column.
		original := make([]any, 0, len(values))
		for i, column := range columns {
			if column == "shadowrocket_template_id" {
				if values[i] != nil {
					t.Fatal("new template reference must default to NULL")
				}
				continue
			}
			original = append(original, values[i])
		}
		result = append(result, original)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUsernameMigrationRollsBackAndRestoresForeignKeys(t *testing.T) {
	for _, failure := range []string{"rebuild-error", "foreign-key-check"} {
		t.Run(failure, func(t *testing.T) {
			db := openVersion13Users(t, t.TempDir())
			if _, err := db.Exec(`INSERT INTO users VALUES (7,'refrain','hash','admin',123,456)`); err != nil {
				t.Fatal(err)
			}
			item := migrations[13]
			item.up = func(ctx context.Context, tx *sql.Tx) error {
				if err := migrateCaseSensitiveUsernames(ctx, tx); err != nil {
					return err
				}
				if failure == "rebuild-error" {
					return errors.New("injected rebuild failure")
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO sessions (user_id,token_hash,expires_at,created_at) VALUES (999,'invalid',999,123)`)
				return err
			}
			if err := applyMigration(t.Context(), db, item); err == nil {
				t.Fatal("migration failure was ignored")
			}
			var schema string
			var version, enabled int
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'users'`).Scan(&schema); err != nil || !strings.Contains(schema, "COLLATE NOCASE") {
				t.Fatalf("schema not rolled back: %s, %v", schema, err)
			}
			if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 13 {
				t.Fatalf("failed migration recorded: %d, %v", version, err)
			}
			if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
				t.Fatalf("foreign keys not restored: %d, %v", enabled, err)
			}
			assertForeignKeysValid(t, db)
			if err := migrate(db); err != nil {
				t.Fatalf("retry migration: %v", err)
			}
		})
	}
}

func TestUserOrderFreshAndUpgradeConstraints(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		directory := t.TempDir()
		if upgrade {
			openVersion13Users(t, directory).Close()
		}
		db, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		for _, statement := range []string{
			`INSERT INTO users VALUES (1,'admin','hash','admin',1,1),(2,'other','hash','admin',1,1),(3,'member','hash','vip',1,1)`,
			`INSERT INTO user_account_order VALUES (1,3,1),(1,1,2),(2,3,1),(2,1,2)`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		for _, statement := range []string{
			`INSERT INTO user_account_order VALUES (1,3,3)`,
			`INSERT INTO user_account_order VALUES (1,2,1)`,
			`INSERT INTO user_account_order VALUES (999,1,1)`,
			`INSERT INTO user_account_order VALUES (1,999,3)`,
		} {
			if _, err := db.Exec(statement); err == nil {
				t.Fatalf("invalid order accepted: %s", statement)
			}
		}
		// Both the viewer and the sorted account foreign keys must cascade.
		for _, id := range []int{2, 3} {
			if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM user_account_order`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("cascade order count = %d, %v", count, err)
		}
		assertForeignKeysValid(t, db)
		assertLatestMigrationHistory(t, db)
	}
}
