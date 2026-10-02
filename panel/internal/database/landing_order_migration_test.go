package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLandingOrderFreshUpgradeReopenAndConstraints(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "fresh"
		if upgrade {
			name = "upgrade12"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if upgrade {
				old, err := sql.Open("sqlite", filepath.Join(directory, "panel.db"))
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range migrations[:12] {
					if err := applyMigration(t.Context(), old, item); err != nil {
						t.Fatal(err)
					}
				}
				old.Close()
			}
			db, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertLatestMigrationHistory(t, db)
			for _, statement := range []string{
				`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (1,'owner','hash','vip',1,1),(2,'reader','hash','vip',1,1)`,
				`INSERT INTO landing_nodes (id,owner_user_id,name,visibility,protocol,host,port,uri,created_at,updated_at)
				 VALUES (1,1,'A','public','vless','example.com',443,'uri',1,1),(2,1,'B','public','vless','example.com',443,'uri',1,1)`,
				`INSERT INTO user_landing_order VALUES (1,1,1),(1,2,2),(2,2,1)`,
			} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			for _, statement := range []string{
				`INSERT INTO user_landing_order VALUES (1,1,3)`,
				`INSERT INTO user_landing_order VALUES (2,1,1)`,
				`INSERT INTO user_landing_order VALUES (999,1,1)`,
				`INSERT INTO user_landing_order VALUES (2,999,2)`,
			} {
				if _, err := db.Exec(statement); err == nil {
					t.Fatalf("invalid order accepted: %s", statement)
				}
			}
			db.Close()
			db, err = Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertLatestMigrationHistory(t, db)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM user_landing_order`).Scan(&count); err != nil || count != 3 {
				t.Fatalf("reopened order count = %d, %v", count, err)
			}
			if _, err := db.Exec(`DELETE FROM users WHERE id = 2`); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM user_landing_order WHERE user_id = 2`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("deleted user order = %d, %v", count, err)
			}
			if _, err := db.Exec(`DELETE FROM landing_nodes WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM user_landing_order`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("deleted landing order = %d, %v", count, err)
			}
			assertForeignKeysValid(t, db)
		})
	}
}
