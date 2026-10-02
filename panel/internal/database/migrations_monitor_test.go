package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMonitorMigrationUpgradeReopenAndCascade(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "upgrade_v10"}[existing], func(t *testing.T) {
			dir := t.TempDir()
			if existing {
				db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range migrations[:10] {
					if err := applyMigration(context.Background(), db, item); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'existing', 'offline', 1, 1)`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			db, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !existing {
				if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'new', 'offline', 1, 1)`); err != nil {
					t.Fatal(err)
				}
			}
			assertLatestMigrationHistory(t, db)
			for _, deletion := range []string{`DELETE FROM monitor_probe_tasks WHERE id = 1`, `UPDATE servers SET archived_at = 1 WHERE id = 1`, `DELETE FROM servers WHERE id = 1`} {
				if _, err := db.Exec(`DELETE FROM monitor_probe_tasks WHERE id = 1`); err != nil {
					t.Fatal(err)
				}
				for _, stmt := range []string{
					`INSERT INTO monitor_probe_tasks (id, name, type, target, port, interval_seconds, enabled, created_at, updated_at) VALUES (1, 'probe', 'tcp', 'example.com', 443, 60, 1, 1, 1)`,
					`INSERT INTO monitor_probe_servers VALUES (1, 1)`,
					`INSERT INTO monitor_probe_records VALUES (1, 1, 1, 'success', 12)`,
				} {
					if _, err := db.Exec(stmt); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, 1, 2, 'timeout', -1)`); err == nil {
					t.Fatal("magic latency accepted")
				}
				if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, 1, 2, 'success', NULL)`); err == nil {
					t.Fatal("missing latency accepted")
				}
				if _, err := db.Exec(`INSERT INTO monitor_probe_servers VALUES (1, 999)`); err == nil {
					t.Fatal("missing server FK accepted")
				}
				if _, err := db.Exec(deletion); err != nil {
					t.Fatal(err)
				}
				for _, table := range []string{"monitor_probe_servers", "monitor_probe_records"} {
					var count int
					if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("cascade %s = %d, %v", table, count, err)
					}
				}
			}
			assertForeignKeysValid(t, db)
			db.Close()
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertLatestMigrationHistory(t, db)
			assertForeignKeysValid(t, db)
		})
	}
}

func TestMonitorDefaultOnMigrationPreservesExistingAssignments(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:11] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'existing', 'offline', 1, 1)`,
		`INSERT INTO monitor_probe_tasks (id, name, type, target, port, interval_seconds, enabled, created_at, updated_at) VALUES (1, 'old probe', 'tcp', 'example.com', 443, 60, 1, 1, 1)`,
		`INSERT INTO monitor_probe_servers VALUES (1, 1)`,
		`INSERT INTO monitor_probe_records VALUES (1, 1, 1, 'success', 42)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for range 2 {
		db, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		assertLatestMigrationHistory(t, db)
		var defaultOn bool
		if err := db.QueryRow(`SELECT default_on FROM monitor_probe_tasks WHERE id = 1`).Scan(&defaultOn); err != nil || defaultOn {
			t.Fatalf("old default = %v %v", defaultOn, err)
		}
		for _, table := range []string{"monitor_probe_servers", "monitor_probe_records"} {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("preserved %s = %d %v", table, count, err)
			}
		}
		for _, invalid := range []string{"NULL", "2", "-1"} {
			if _, err := db.Exec(`UPDATE monitor_probe_tasks SET default_on = ` + invalid); err == nil {
				t.Fatalf("invalid default_on %s accepted", invalid)
			}
		}
		assertForeignKeysValid(t, db)
		db.Close()
	}
}

func TestMaterializeDefaultProbeAssignmentsMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:17] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO servers (id,name,status,archived_at,decommission_status,created_at,updated_at) VALUES
   (1,'online','online',NULL,'',1,1),(2,'pending','pending',NULL,'',1,1),(3,'archived','offline',1,'',1,1),(4,'removing','offline',NULL,'pending',1,1),(5,'offline','offline',NULL,'',1,1)`,
		`INSERT INTO monitor_probe_tasks (id,name,type,target,port,interval_seconds,enabled,default_on,created_at,updated_at) VALUES
   (1,'default','tcp','example.com',443,60,1,1,1,1),(2,'disabled default','icmp','example.com',NULL,60,0,1,1,1),(3,'manual','tcp','example.com',80,60,1,0,1,1)`,
		`INSERT INTO monitor_probe_servers VALUES (1,1),(3,2)`,
		`INSERT INTO monitor_probe_records VALUES (1,1,123,'success',42),(4,1,124,'timeout',NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		db, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var assignments string
		if err := db.QueryRow(`SELECT group_concat(pair) FROM (SELECT task_id || ':' || server_id AS pair FROM monitor_probe_servers ORDER BY task_id,server_id)`).Scan(&assignments); err != nil || assignments != "1:1,1:2,1:5,2:1,2:2,2:5,3:2" {
			t.Fatalf("assignments = %s %v", assignments, err)
		}
		var defaults, records int
		if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_tasks WHERE default_on = 1`).Scan(&defaults); err != nil || defaults != 2 {
			t.Fatalf("defaults = %d %v", defaults, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records WHERE (server_id = 1 AND task_id = 1 AND ts = 123 AND latency_ms = 42) OR (server_id = 4 AND task_id = 1 AND ts = 124 AND outcome = 'timeout')`).Scan(&records); err != nil || records != 2 {
			t.Fatalf("history = %d %v", records, err)
		}
		assertLatestMigrationHistory(t, db)
		assertForeignKeysValid(t, db)
		if pass == 0 {
			// A later startup must not rerun the one-time materialization.
			if _, err := db.Exec(`INSERT INTO servers (id,name,status,created_at,updated_at) VALUES (6,'later','pending',2,2)`); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
