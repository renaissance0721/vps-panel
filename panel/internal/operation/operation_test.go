package operation_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/operation"
)

func TestOperationLifecycleSupersedeReceiptAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers
		(id, name, created_by_role, status, desired_state_version, created_at, updated_at)
		VALUES (1, 'Server', 'admin', 'online', 4, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	record := func(version int64) {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := operation.RecordTx(t.Context(), tx, 1, "proxy", version, "create", version, now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	receipt := func(version int64, status, message string) {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := operation.RecordReceiptTx(t.Context(), tx, 1, version, status, message, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	record(1)
	assertOperationStatus(t, db, 1, operation.StatusPending, "")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.MarkSentTx(t.Context(), tx, 1, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertOperationStatus(t, db, 1, operation.StatusSent, "")
	receipt(1, "success", "")
	assertOperationStatus(t, db, 1, operation.StatusApplied, "")

	record(2)
	receipt(2, "failed", "candidate rejected")
	assertOperationStatus(t, db, 2, operation.StatusFailed, "candidate rejected")

	record(3)
	record(4)
	assertOperationStatus(t, db, 3, operation.StatusSuperseded, "")
	assertOperationStatus(t, db, 4, operation.StatusPending, "")

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertOperationStatus(t, db, 1, operation.StatusApplied, "")
	assertOperationStatus(t, db, 2, operation.StatusFailed, "candidate rejected")
	assertOperationStatus(t, db, 3, operation.StatusSuperseded, "")
	assertOperationStatus(t, db, 4, operation.StatusPending, "")
}

func assertOperationStatus(t *testing.T, db *sql.DB, version int64, wantStatus, wantError string) {
	t.Helper()
	var status, message string
	if err := db.QueryRow(`SELECT status, error FROM configuration_operations
		WHERE server_id = 1 AND desired_state_version = ?`, version).Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || message != wantError {
		t.Fatalf("operation version %d = %q/%q, want %q/%q", version, status, message, wantStatus, wantError)
	}
}
