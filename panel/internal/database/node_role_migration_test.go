package database_test

import (
	"path/filepath"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/databasehealth"
	"github.com/renaissance0721/vps-panel/panel/internal/landing"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	"github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
	"github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestNodeRoleMigrationAndOldBackupPreserveBehavior(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', 1, 1)`,
		`INSERT INTO servers (id, name, visibility, created_by_role, status, created_at, updated_at) VALUES (1, 'server', 'public', 'admin', 'offline', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	proxies := proxy.NewService(db)
	var nodes []proxy.Proxy
	for _, port := range []int{443, 444} {
		value, _, err := proxies.Create(t.Context(), proxy.CreateInput{ServerID: 1, Name: "original", ListenPort: port, EntryHostMode: proxy.EntryHostManual, EntryHost: "node.example.com", Enabled: true, Security: proxy.SecurityReality, ServerName: "example.com", RealityTarget: "example.com:443", FirstClientName: "default"})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, value)
	}
	external, err := landing.NewService(db).Create(t.Context(), 1, landing.CreateInput{Name: "external", Visibility: landing.VisibilityPublic, URI: "vless://uuid@external.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	var originalConfig, originalCredential string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, nodes[0].ID).Scan(&originalConfig); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT credential_json FROM clients WHERE proxy_id = ?`, nodes[0].ID).Scan(&originalCredential); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_proxy_order VALUES (1, ?, 2), (1, ?, 1)`, nodes[0].ID, nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_landing_order VALUES (1, ?, 1)`, external.ID); err != nil {
		t.Fatal(err)
	}
	// Reproduce v28: identical schema and migration history, with neither new column.
	for _, statement := range []string{
		`ALTER TABLE proxies DROP COLUMN node_role`,
		`ALTER TABLE landing_nodes DROP COLUMN node_role`,
		`DELETE FROM schema_migrations WHERE version = 29`,
		`UPDATE sqlite_sequence SET seq = 100 WHERE name IN ('proxies', 'landing_nodes')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	archive, cleanup, err := backup.CreateArchive(t.Context(), db, directory, "v1.0.0", "panel.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restoredDirectory := t.TempDir()
	if err := backup.StageImport(t.Context(), archive, restoredDirectory, "v1.0.0", "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(restoredDirectory)
	if err != nil || attempt == nil {
		t.Fatalf("restore old backup: %v", err)
	}
	for _, path := range []string{directory, restoredDirectory} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			db, err := database.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version int
			if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != database.LatestSchemaVersion {
				t.Fatalf("schema version=%d: %v", version, err)
			}
			for _, table := range []string{"proxies", "landing_nodes"} {
				var invalid, nullable int
				var defaultValue string
				if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE node_role != 'direct'`).Scan(&invalid); err != nil || invalid != 0 {
					t.Fatalf("legacy %s roles: %v", table, err)
				}
				if err := db.QueryRow(`SELECT "notnull", dflt_value FROM pragma_table_info(?) WHERE name = 'node_role'`, table).Scan(&nullable, &defaultValue); err != nil || nullable != 1 || defaultValue != "'direct'" {
					t.Fatalf("%s default/NOT NULL: %v", table, err)
				}
				for _, value := range []any{"invalid", nil} {
					if _, err := db.Exec(`UPDATE `+table+` SET node_role = ?`, value); err == nil {
						t.Fatalf("%s accepted invalid role", table)
					}
				}
				var sequence int
				if err := db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = ?`, table).Scan(&sequence); err != nil || sequence != 100 {
					t.Fatalf("%s sequence=%d: %v", table, sequence, err)
				}
			}
			proxies := proxy.NewService(db)
			for _, value := range nodes {
				got, err := proxies.Get(t.Context(), value.ID)
				if err != nil || got.NodeRole != "direct" || got.Name != value.Name || got.CreatedAt != value.CreatedAt || got.UpdatedAt != value.UpdatedAt {
					t.Fatalf("legacy Proxy changed: %v", err)
				}
				if _, err := proxies.GetClientShare(t.Context(), got.Clients[0].ID); err != nil {
					t.Fatalf("legacy share: %v", err)
				}
			}
			var config, credential string
			if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, nodes[0].ID).Scan(&config); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT credential_json FROM clients WHERE proxy_id = ?`, nodes[0].ID).Scan(&credential); err != nil {
				t.Fatal(err)
			}
			if config != originalConfig || credential != originalCredential {
				t.Fatal("migration changed config/credentials")
			}
			got, err := landing.NewService(db).Get(t.Context(), external.ID, 1)
			if err != nil || got.NodeRole != "direct" || got.Visibility != external.Visibility || !got.OwnedByMe || got.CreatedAt != external.CreatedAt || got.UpdatedAt != external.UpdatedAt {
				t.Fatalf("legacy external node changed: %v", err)
			}
			order, err := listorder.NewStore(db).Sort(t.Context(), 1, listorder.Proxies, []int64{nodes[0].ID, nodes[1].ID})
			if err != nil || order[0] != nodes[1].ID {
				t.Fatalf("legacy order changed: %v", err)
			}
			relays := relay.NewService(db)
			for index, input := range []relay.CreateInput{
				{TargetType: relay.TargetProxy, TargetProxyID: &nodes[0].ID, TargetClientID: &nodes[0].Clients[0].ID},
				{TargetType: relay.TargetLanding, TargetLandingID: &external.ID},
			} {
				input.ServerID, input.Name, input.ListenPort = 1, "relay", 9000+index
				input.EntryHostMode, input.EntryHost, input.Network, input.Enabled = relay.EntryHostManual, "relay.example.com", relay.NetworkTCP, true
				if _, _, err := relays.Create(t.Context(), input); err != nil {
					t.Fatalf("legacy relay target: %v", err)
				}
			}
			subscriptions := subscription.NewService(db, relays)
			actor := subscription.PersonalSubscriptionActor{UserID: 1, Role: "admin"}
			group, err := subscriptions.CreatePersonalSubscription(t.Context(), actor, subscription.CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := subscriptions.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []subscription.SetPersonalSubscriptionNodeInput{
				{SourceType: "proxy", SourceID: nodes[0].ID, DisplayName: "managed", Enabled: true},
				{SourceType: "landing", SourceID: external.ID, DisplayName: "external", Enabled: true},
			}); err != nil {
				t.Fatalf("legacy personal sources: %v", err)
			}
			if data, err := subscriptions.GeneratePersonalSubscriptionData(t.Context(), group.Token); err != nil || len(data.Nodes) != 2 {
				t.Fatalf("legacy generation: %v", err)
			}
			if report, err := databasehealth.Inspect(t.Context(), db); err != nil || !report.Healthy() {
				t.Fatalf("database health: %+v, %v", report, err)
			}
		})
	}
	if err := attempt.Commit(); err != nil {
		t.Fatal(err)
	}
}
