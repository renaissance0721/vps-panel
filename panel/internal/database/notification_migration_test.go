package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNotificationMigrationFreshUpgradeReopenAndCascade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "from15"}[upgrade], func(t *testing.T) {
			dir := t.TempDir()
			if upgrade {
				db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
				if err != nil {
					t.Fatal(err)
				}
				for _, migration := range migrations[:15] {
					if err := applyMigration(t.Context(), db, migration); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				// Baseline uses the current fresh schema: remove v16 tables to
				// faithfully simulate a database produced by the prior release.
				for _, query := range []string{`DROP TABLE server_notification_state`, `DROP TABLE notification_settings`, `INSERT INTO servers(id,name,status,created_at,updated_at) VALUES(123,'preserved','offline',1,1)`} {
					if _, err := db.Exec(query); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				db.Close()
			}
			db, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			var token, chat string
			var grace, threshold, step, online, recovery, traffic, full int
			if err := db.QueryRow(`SELECT telegram_bot_token,telegram_chat_id,offline_grace_minutes,traffic_threshold_percent,traffic_step_percent,online_enabled,recovery_enabled,traffic_enabled,traffic_full_enabled FROM notification_settings WHERE id=1`).Scan(&token, &chat, &grace, &threshold, &step, &online, &recovery, &traffic, &full); err != nil {
				t.Fatal(err)
			}
			if token != "" || chat != "" || grace != 3 || threshold != 80 || step != 10 || online != 1 || recovery != 1 || traffic != 1 || full != 1 {
				t.Fatal("incorrect defaults")
			}
			for _, query := range []string{`INSERT INTO notification_settings(id,updated_at) VALUES(2,1)`, `UPDATE notification_settings SET offline_grace_minutes=0`, `UPDATE notification_settings SET traffic_threshold_percent=101`, `UPDATE notification_settings SET traffic_step_percent=0`, `UPDATE notification_settings SET online_enabled=2`} {
				if _, err := db.Exec(query); err == nil {
					t.Fatalf("constraint allowed %s", query)
				}
			}
			if !upgrade {
				if _, err := db.Exec(`INSERT INTO servers(id,name,status,created_at,updated_at) VALUES(123,'preserved','offline',1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`INSERT INTO server_notification_state(server_id,offline_started_at,offline_notified_at,traffic_cycle_started_at,traffic_last_notified_step,updated_at) VALUES(123,1,2,3,90,4)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE notification_settings SET telegram_bot_token='123:SAVED',telegram_chat_id='-1001',offline_grace_minutes=5`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.QueryRow(`SELECT telegram_bot_token,offline_grace_minutes FROM notification_settings WHERE id=1`).Scan(&token, &grace); err != nil || token != "123:SAVED" || grace != 5 {
				t.Fatalf("settings lost: %v", err)
			}
			if err := db.QueryRow(`SELECT traffic_last_notified_step FROM server_notification_state WHERE server_id=123`).Scan(&step); err != nil || step != 90 {
				t.Fatalf("state lost: %v", err)
			}
			if _, err := db.Exec(`DELETE FROM servers WHERE id=123`); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM server_notification_state`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("cascade=%d err=%v", count, err)
			}
		})
	}
}
