package database_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/databasehealth"
	"github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestServerCountryMigrationPreservesSystemInformation(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	service := server.NewService(db)
	created, err := service.Create(t.Context(), "country migration")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReportSystemInfo(t.Context(), agent.ID, created.ID, server.SystemInfoReport{
		Hostname: "legacy-country-host", PublicIPv4: "198.51.100.70", IPv4: []string{"10.0.0.1"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE server_system_info DROP COLUMN country_code`,
		`ALTER TABLE server_system_info DROP COLUMN country_ip`,
		`ALTER TABLE server_system_info DROP COLUMN country_lookup_at`,
		`DELETE FROM schema_migrations WHERE version = 31`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	value, err := server.NewService(db).Get(t.Context(), created.ID)
	if err != nil || value.SystemInfo == nil || value.SystemInfo.Hostname != "legacy-country-host" ||
		value.SystemInfo.PublicIPv4 != "198.51.100.70" || value.SystemInfo.CountryCode != "" {
		t.Fatalf("migrated system information = (%+v, %v)", value.SystemInfo, err)
	}
	for _, field := range []string{"country_code", "country_ip"} {
		var notNull int
		var defaultValue sql.NullString
		if err := db.QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info('server_system_info') WHERE name = ?`, field).Scan(&notNull, &defaultValue); err != nil || notNull != 1 || !defaultValue.Valid || defaultValue.String != "''" {
			t.Fatalf("country field %s metadata = (%d, %q, %v)", field, notNull, defaultValue.String, err)
		}
	}
	var lookupAt int64
	if err := db.QueryRow(`SELECT country_lookup_at FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&lookupAt); err != nil || lookupAt != 0 {
		t.Fatalf("migrated lookup timestamp = %d, %v", lookupAt, err)
	}
	for _, invalid := range []string{"D", "de", "DEU", "123"} {
		if _, err := db.Exec(`UPDATE server_system_info SET country_code = ? WHERE server_id = ?`, invalid, created.ID); err == nil {
			t.Fatalf("database accepted invalid country code %q", invalid)
		}
	}
	if _, err := db.Exec(`UPDATE server_system_info SET country_code = 'TW' WHERE server_id = ?`, created.ID); err != nil {
		t.Fatalf("database rejected TW: %v", err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET country_ip = ? WHERE server_id = ?`, strings.Repeat("a", 46), created.ID); err == nil {
		t.Fatal("database accepted oversized country IP")
	}
	if report, err := databasehealth.Inspect(t.Context(), db); err != nil || !report.Healthy() {
		t.Fatalf("database health = (%+v, %v)", report, err)
	}
}
