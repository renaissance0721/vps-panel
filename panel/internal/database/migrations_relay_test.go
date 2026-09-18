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

func TestMigrateRelayTargetClientKeepsLegacyRowsAndClearsDeletedClient(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE clients (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE relays (id INTEGER PRIMARY KEY)`,
		`INSERT INTO relays (id) VALUES (1)`,
		`INSERT INTO clients (id) VALUES (2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateRelayTargetClient(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var targetClientID sql.NullInt64
	if err := db.QueryRow(`SELECT target_client_id FROM relays WHERE id = 1`).Scan(&targetClientID); err != nil || targetClientID.Valid {
		t.Fatalf("legacy target client = %v, %v", targetClientID, err)
	}
	if _, err := db.Exec(`UPDATE relays SET target_client_id = 2 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayTargetClient(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM clients WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT target_client_id FROM relays WHERE id = 1`).Scan(&targetClientID); err != nil || targetClientID.Valid {
		t.Fatalf("deleted target client = %v, %v", targetClientID, err)
	}
}
