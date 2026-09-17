package database

import (
	"database/sql"
	"testing"
)

func TestMigrateRelayEntryHostDefaultsExistingRowsToAuto(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE relays (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays (id, name) VALUES (1, 'Legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayEntryHost(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var mode, host string
	if err := db.QueryRow(`SELECT entry_host_mode, entry_host FROM relays WHERE id = 1`).Scan(&mode, &host); err != nil {
		t.Fatal(err)
	}
	if mode != "auto" || host != "" {
		t.Fatalf("migrated Relay entry host = (%q, %q), want auto and empty", mode, host)
	}
	if _, err := db.Exec(`UPDATE relays SET entry_host_mode = 'manual', entry_host = 'relay.example.com' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayEntryHost(t.Context(), db); err != nil {
		t.Fatalf("repeat Relay entry migration: %v", err)
	}
	if err := db.QueryRow(`SELECT entry_host_mode, entry_host FROM relays WHERE id = 1`).Scan(&mode, &host); err != nil {
		t.Fatal(err)
	}
	if mode != "manual" || host != "relay.example.com" {
		t.Fatalf("repeat migration changed Relay entry host = (%q, %q)", mode, host)
	}
}
