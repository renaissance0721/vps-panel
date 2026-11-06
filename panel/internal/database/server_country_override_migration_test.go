package database_test

import (
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/server"
	"testing"
)

func TestCountryOverrideMigrationPreservesServerAndGeoIP(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	created, err := server.NewService(db).Create(t.Context(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info (server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, agent_version, country_code, country_ip, country_lookup_at, reported_at) VALUES (?, 'host', 'Linux', '', '', '', '[]', '[]', '', 'JP', '198.51.100.1', 42, 42)`, created.ID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`ALTER TABLE servers DROP COLUMN country_override_code`, `DELETE FROM schema_migrations WHERE version >= 33`} {
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
	got, err := server.NewService(db).Get(t.Context(), created.ID)
	if err != nil || got.Name != "legacy" || got.CountryOverrideCode != nil || got.SystemInfo == nil || got.SystemInfo.CountryCode != "JP" {
		t.Fatalf("migrated server: %+v %v", got, err)
	}
	var ip string
	var lookup int
	if err := db.QueryRow(`SELECT country_ip, country_lookup_at FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&ip, &lookup); err != nil || ip != "198.51.100.1" || lookup != 42 {
		t.Fatalf("geoip metadata changed: %s %d %v", ip, lookup, err)
	}
}
