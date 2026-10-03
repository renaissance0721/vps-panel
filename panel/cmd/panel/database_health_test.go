package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestRunDatabaseHealthCheckOutput(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	healthy, err := runDatabaseHealthCheck(t.Context(), dataDir, &output)
	if err != nil || !healthy {
		t.Fatalf("health check = %t, %v\n%s", healthy, err, output.String())
	}
	for _, expected := range []string{
		"Database integrity: OK",
		"Foreign keys: 0 issue(s)",
		"[orphan-order]",
		"Database health: OK",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("health output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestRunDatabaseHealthCheckReportsForeignKeyDetails(t *testing.T) {
	dataDir := createCommandHealthDatabase(t)

	var output bytes.Buffer
	healthy, err := runDatabaseHealthCheck(t.Context(), dataDir, &output)
	if err != nil || healthy {
		t.Fatalf("health check = %t, %v\n%s", healthy, err, output.String())
	}
	for _, expected := range []string{
		"[foreign-key]",
		"total violations: 1",
		"table: user_relay_order",
		"rowid: 10",
		"column: relay_id",
		"value: 11",
		"parent: relays(id)",
		"user_relay_order orphan rows: 1",
		"Database health: FAILED",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("health output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestRunDatabaseRepairOutput(t *testing.T) {
	dataDir := createCommandHealthDatabase(t)

	var output bytes.Buffer
	healthy, err := runDatabaseRepair(t.Context(), dataDir, &output,
		time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	if err != nil || !healthy {
		t.Fatalf("repair = %t, %v\n%s", healthy, err, output.String())
	}
	for _, expected := range []string{
		"Before:",
		"Foreign keys: 1 issue(s)",
		"Backup: " + filepath.Join(dataDir, "panel.db.repair-backup-20261003T120000Z"),
		"Repaired:",
		"user_relay_order orphan rows: 1",
		"After:",
		"Foreign keys: 0 issue(s)",
		"Database health: OK",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("repair output %q does not contain %q", output.String(), expected)
		}
	}
}

func createCommandHealthDatabase(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash, role, created_at, updated_at)
		VALUES (1, 'admin', 'hash', 'admin', 1, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_relay_order(rowid, user_id, relay_id, position)
		VALUES (10, 1, 11, 10)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	return dataDir
}
