package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigrateSubscriptionPlanTitle(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE subscription_plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plans (name) VALUES ('Legacy')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateSubscriptionPlanTitle(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_plans')
		WHERE name = 'subscription_title'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	var name string
	var title sql.NullString
	if err := db.QueryRow(`SELECT name, subscription_title FROM subscription_plans WHERE id = 1`).Scan(&name, &title); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 || name != "Legacy" || title.Valid {
		t.Fatalf("migrated plan = columns %d, name %q, title %+v", columnCount, name, title)
	}
}
