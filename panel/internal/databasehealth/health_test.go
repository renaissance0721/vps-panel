package databasehealth_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/databasehealth"
)

func TestForeignKeyViolationDetails(t *testing.T) {
	path := createHealthDatabase(t)
	insertRelayOrderOrphan(t, path)
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignKeys.Total != 1 || len(report.ForeignKeys.Violations) != 1 {
		t.Fatalf("foreign keys = %+v", report.ForeignKeys)
	}
	violation := report.ForeignKeys.Violations[0]
	if violation.Table != "user_relay_order" || violation.RowID == nil || *violation.RowID != 10 ||
		violation.ChildColumn != "relay_id" || violation.Value != "11" || !violation.ValueAvailable ||
		violation.ParentTable != "relays" || violation.ParentColumn != "id" || violation.OnDelete != "CASCADE" {
		t.Fatalf("violation = %+v", violation)
	}
}

func TestForeignKeyViolationWithoutRowIDStillReportsMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "without-rowid.db")
	db := openRawDatabase(t, path)
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`CREATE TABLE parents(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE children(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id) ON DELETE CASCADE) WITHOUT ROWID`,
		`INSERT INTO children(id, parent_id) VALUES (1, 99)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignKeys.Total != 1 || len(report.ForeignKeys.Violations) != 1 {
		t.Fatalf("foreign keys = %+v", report.ForeignKeys)
	}
	violation := report.ForeignKeys.Violations[0]
	if violation.RowID != nil || violation.Table != "children" || violation.ChildColumn != "parent_id" ||
		violation.ParentTable != "parents" || violation.ParentColumn != "id" {
		t.Fatalf("WITHOUT ROWID violation = %+v", violation)
	}
}

func TestForeignKeyViolationWithImplicitParentKeyStillReportsChildColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "implicit-parent-key.db")
	db := openRawDatabase(t, path)
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`CREATE TABLE parents(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE children(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents)`,
		`INSERT INTO children(id, parent_id) VALUES (1, 99)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignKeys.Total != 1 || len(report.ForeignKeys.Violations) != 1 {
		t.Fatalf("foreign keys = %+v", report.ForeignKeys)
	}
	violation := report.ForeignKeys.Violations[0]
	if violation.ChildColumn != "parent_id" || violation.Value != "99" ||
		violation.ParentTable != "parents" || violation.ParentColumn != "" {
		t.Fatalf("implicit parent key violation = %+v", violation)
	}
}

func TestForeignKeyViolationDoesNotExposeSensitiveValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensitive-value.db")
	db := openRawDatabase(t, path)
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`CREATE TABLE parents(id TEXT PRIMARY KEY)`,
		`CREATE TABLE children(id INTEGER PRIMARY KEY, session_token TEXT REFERENCES parents(id))`,
		`INSERT INTO children(id, session_token) VALUES (1, 'must-not-be-printed')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	violation := report.ForeignKeys.Violations[0]
	if violation.ChildColumn != "session_token" || violation.ValueAvailable {
		t.Fatalf("sensitive violation = %+v", violation)
	}
	if formatted := databasehealth.FormatForeignKeyViolations(report.ForeignKeys); strings.Contains(formatted, "must-not-be-printed") {
		t.Fatalf("sensitive value leaked in %q", formatted)
	}
}

func TestForeignKeyViolationDetailsAreBounded(t *testing.T) {
	path := createHealthDatabase(t)
	db := openRawDatabase(t, path)
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= databasehealth.MaxForeignKeyDetails+2; id++ {
		if _, err := db.Exec(`INSERT INTO user_relay_order(user_id, relay_id, position) VALUES (1, ?, ?)`, id, id); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.ForeignKeys.Total != databasehealth.MaxForeignKeyDetails+2 || len(report.ForeignKeys.Violations) != databasehealth.MaxForeignKeyDetails {
		t.Fatalf("bounded foreign keys = %+v", report.ForeignKeys)
	}
}

func TestDatabaseHealthCheckCleanDatabase(t *testing.T) {
	report, err := databasehealth.CheckPath(t.Context(), createHealthDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Healthy() || report.ForeignKeys.Total != 0 || !report.IntegrityOK() {
		t.Fatalf("clean report = %+v", report)
	}
}

func TestDatabaseHealthCheckDetectsOrphanOrder(t *testing.T) {
	path := createHealthDatabase(t)
	insertRelayOrderOrphan(t, path)
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() || checkCount(report, "user_relay_order orphan rows") != 1 {
		t.Fatalf("orphan report = %+v", report)
	}
}

func TestDatabaseHealthCheckDetectsArchivedTargetRelay(t *testing.T) {
	path := createHealthDatabase(t)
	db := openRawDatabase(t, path)
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`INSERT INTO servers(id, name, status, archived_at, created_at, updated_at) VALUES (1, 'Source', 'offline', NULL, 1, 1), (2, 'Target', 'offline', 2, 1, 2)`,
		`INSERT INTO proxies(id, server_id, name, protocol, listen_port, enabled, config_json, created_at, updated_at) VALUES (1, 2, 'Target', 'vless', 443, 1, '{}', 1, 1)`,
		`INSERT INTO clients(id, proxy_id, name, credential_json, created_at, updated_at) VALUES (1, 1, 'Client', '{}', 1, 1)`,
		`INSERT INTO relays(id, server_id, name, listen_port, target_type, target_proxy_id, target_client_id, network, enabled, created_at, updated_at) VALUES (1, 1, 'Dirty', 9502, 'proxy', 1, 1, 'tcp', 1, 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := databasehealth.CheckPath(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy() || checkCount(report, "enabled Relay references archived target server") != 1 ||
		checkCount(report, "archived server still has enabled Proxy") != 1 {
		t.Fatalf("lifecycle report = %+v", report)
	}
}

func TestDatabaseRepairRemovesRelayOrderOrphan(t *testing.T) {
	path := createHealthDatabase(t)
	insertRelayOrderOrphan(t, path)
	result, err := databasehealth.Repair(t.Context(), path, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || !result.After.Healthy() || repairedCount(result, "user_relay_order") != 1 {
		t.Fatalf("repair result = %+v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("repair backup: %v", err)
	}
	db := openRawDatabase(t, path)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_relay_order`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan rows after repair = %d, %v", count, err)
	}
}

func TestDatabaseRepairRemovesSubscriptionOrderOrphans(t *testing.T) {
	path := createHealthDatabase(t)
	db := openRawDatabase(t, path)
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO user_personal_subscription_order(user_id, personal_subscription_id, position) VALUES (1, 101, 0)`,
		`INSERT INTO user_published_node_order(user_id, published_node_id, position) VALUES (1, 202, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := databasehealth.Repair(t.Context(), path, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || !result.After.Healthy() ||
		repairedCount(result, "user_personal_subscription_order") != 1 ||
		repairedCount(result, "user_published_node_order") != 1 {
		t.Fatalf("repair result = %+v", result)
	}

	db = openRawDatabase(t, path)
	defer db.Close()
	for _, table := range []string{"user_personal_subscription_order", "user_published_node_order"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after repair = %d, %v", table, count, err)
		}
	}
}

func TestDatabaseRepairDoesNotDeleteBusinessRows(t *testing.T) {
	path := createHealthDatabase(t)
	db := openRawDatabase(t, path)
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(user_id, token_hash, expires_at, created_at) VALUES (999, 'orphan-session', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := databasehealth.Repair(t.Context(), path, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, databasehealth.ErrManualIntervention) || result.Committed {
		t.Fatalf("business repair = %+v, %v", result, err)
	}
	db = openRawDatabase(t, path)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = 'orphan-session'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("business row count = %d, %v", count, err)
	}
}

func createHealthDatabase(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash, role, created_at, updated_at)
		VALUES (1, 'admin', 'hash', 'admin', 1, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "panel.db")
}

func insertRelayOrderOrphan(t *testing.T, path string) {
	t.Helper()
	db := openRawDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_relay_order(rowid, user_id, relay_id, position) VALUES (10, 1, 11, 10)`); err != nil {
		t.Fatal(err)
	}
}

func openRawDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func checkCount(report databasehealth.Report, name string) int {
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Count
		}
	}
	return -1
}

func repairedCount(result databasehealth.RepairResult, table string) int64 {
	for _, repaired := range result.Repaired {
		if repaired.Table == table {
			return repaired.Count
		}
	}
	return -1
}
