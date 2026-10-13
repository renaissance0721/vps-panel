package database_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/databasehealth"
	"github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestServerNetworkMetadataMigrationPreservesData(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'owner', 'hash', 'admin', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	s := server.NewService(db)
	expires := time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)
	period, limit := 3, int64(2*1024*1024*1024*1024)
	created, err := s.CreateWithSettings(t.Context(), server.CreateServerInput{
		Name: "legacy", CreatorID: 1, Visibility: "private", UserIDs: []int64{1},
		BoundDomainIPv4: "v4.example.com", BoundDomainIPv6: "v6.example.com",
		ExpiresAt: &expires, RenewalPeriodMonths: &period, AutoRenew: true,
		MonthlyTrafficLimitBytes: &limit, TrafficCountMode: "bidirectional", TrafficResetDay: 15, TrafficResetTime: "12:34",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportSystemInfo(t.Context(), agent.ID, created.ID, server.SystemInfoReport{
		Hostname: "legacy-host", OSName: "Linux", IPv4: []string{"198.51.100.1"}, IPv6: []string{"2606:4700::1111"},
		PublicIPv4: "198.51.100.1", PublicIPv6: "2606:4700::1111",
	}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO server_metrics (server_id, cpu_percent, memory_used_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes, uptime_seconds,
		 nic_rx_bytes, nic_tx_bytes, cycle_rx_bytes, cycle_tx_bytes, traffic_adjustment_bytes, cycle_started_at, updated_at)
		 VALUES (1, 12.5, 1024, 4096, 2048, 8192, 600, 10000, 20000, 3000, 4000, -100, 123, 456)`,
		`UPDATE servers SET outbound_preference = 'prefer_ipv6', block_china_inbound = 1, desired_state_version = 8`,
		`UPDATE agents SET implementation = 'custom', api_version = 1, capabilities_json = '["outbound_preference","firewall.cn_block"]',
		 applied_config_version = 7, config_sync_status = 'failed', config_sync_error = 'apply failed', config_synced_at = 123`,
		`UPDATE sqlite_sequence SET seq = 100 WHERE name = 'servers'`,
		`ALTER TABLE servers DROP COLUMN bandwidth_mbps`,
		`ALTER TABLE servers DROP COLUMN network_stack`,
		`DELETE FROM schema_migrations WHERE version = 30`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// Capture every old column in the affected table and its related state.
	snapshot := func(db *sql.DB) map[string][][]any {
		t.Helper()
		result := map[string][][]any{}
		for _, table := range []string{"servers", "agents", "agent_enrollments", "server_access", "server_system_info", "server_metrics"} {
			rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				values, dest := make([]any, len(columns)), make([]any, len(columns))
				for i := range values {
					dest[i] = &values[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				if table == "servers" && len(columns) >= 2 && columns[len(columns)-1] == "network_stack" {
					if values[len(values)-1] != nil || values[len(values)-2] != nil {
						t.Fatal("historical metadata should remain NULL")
					}
					values = values[:len(values)-2]
				}
				result[table] = append(result[table], values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		return result
	}
	before := snapshot(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !reflect.DeepEqual(before, snapshot(db)) {
		t.Fatal("migration changed historical data")
	}
	got, err := server.NewService(db).Get(t.Context(), created.ID)
	if err != nil || got.BandwidthMbps != nil || got.NetworkStack != nil || got.SystemInfo == nil || got.OwnerUserID == nil {
		t.Fatalf("migrated server read: %v", err)
	}
	var version, sequence int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != database.LatestSchemaVersion {
		t.Fatalf("schema version: %d, %v", version, err)
	}
	if err := db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'servers'`).Scan(&sequence); err != nil || sequence != 100 {
		t.Fatalf("server sequence: %d, %v", sequence, err)
	}
	for _, field := range []string{"bandwidth_mbps", "network_stack"} {
		var notNull int
		var defaultValue sql.NullString
		if err := db.QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info('servers') WHERE name = ?`, field).Scan(&notNull, &defaultValue); err != nil || notNull != 0 || defaultValue.Valid {
			t.Fatalf("nullable metadata %s: %v", field, err)
		}
	}
	for _, value := range []any{0, -1, 10000001, 1.5, "bad"} {
		if _, err := db.Exec(`UPDATE servers SET bandwidth_mbps = ?`, value); err == nil {
			t.Fatal("database accepted invalid bandwidth")
		}
	}
	if _, err := db.Exec(`UPDATE servers SET network_stack = 'unknown'`); err == nil {
		t.Fatal("database accepted invalid network stack")
	}
	if report, err := databasehealth.Inspect(t.Context(), db); err != nil || !report.Healthy() {
		t.Fatalf("database health: %+v, %v", report, err)
	}
}
