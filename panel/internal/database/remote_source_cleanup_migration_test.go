package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestRemoveSubscriptionConnectorsMigration(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		db, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		assertRemoteSourceCleanupSchema(t, db)
	})
	for _, version := range []int{42, 43, 44} {
		t.Run(fmt.Sprintf("schema_%d", version), func(t *testing.T) {
			dir := t.TempDir()
			mode, before := seedRemoteSourceCleanup(t, dir, version)
			db, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertRemoteSourceCleanupSchema(t, db)

			for i, after := range readRemoteSourceCleanupData(t, db) {
				if after != before[i] {
					t.Fatalf("preserved data query %d changed", i)
				}
			}
			wantState := fmt.Sprintf(`[[15,"url","server-claim",99,"server error"],[16,"manual","",null,""],[17,"%s","",null,""],[18,"%s","",null,""]]`, mode, mode)
			var state string
			if err := db.QueryRow(`SELECT json_group_array(json_array(id,source_mode,refresh_claim,refresh_lease_until,last_error) ORDER BY id)
				FROM remote_subscription_sources`).Scan(&state); err != nil || state != wantState {
				t.Fatalf("source states = %s, want %s: %v", state, wantState, err)
			}
			res, err := db.Exec(`INSERT INTO remote_subscription_sources(owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at)
				VALUES (1,'new','cipher',300,1,1)`)
			if err != nil {
				t.Fatal(err)
			}
			if id, err := res.LastInsertId(); err != nil || id <= 90 {
				t.Fatalf("source sequence = %d: %v", id, err)
			}
		})
	}
}

func seedRemoteSourceCleanup(t *testing.T, dir string, version int) (string, []string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:version] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (1,'owner','user-hash','vip',1,2)`,
		`INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at) VALUES (3,1,'session-hash',90,1)`,
		`INSERT INTO remote_subscription_sources(id,owner_user_id,name,url_ciphertext,source_mode,interval_seconds,created_at,updated_at)
			VALUES (15,1,'Scheduled','cipher-url','url',300,1,2),(16,1,'Imported','cipher-empty','manual',600,3,4),
			(17,1,'Cached','cipher-original','url',900,5,6),(18,1,'Unassigned','cipher-unused','url',900,7,8),(90,1,'Deleted','cipher', 'url',300,1,1)`,
		`DELETE FROM remote_subscription_sources WHERE id=90`,
		`UPDATE remote_subscription_sources SET refresh_claim='server-claim',refresh_lease_until=99,last_error='server error' WHERE id=15`,
		`UPDATE remote_subscription_sources SET user_agent='custom',last_attempt_at=30,last_success_at=20,
			warnings_json='["warning"]',upload=1,download=2,total=65,expire=100,revision=3,cache_generation=4 WHERE id=17`,
		`INSERT INTO remote_subscription_nodes(id,source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation)
			VALUES (32,17,'identity-one','One','Display One','trojan','yaml','cipher-one',1,4),
			(35,17,'identity-two','Two','Display Two','ss','uri','cipher-two',0,4)`,
		`INSERT INTO personal_subscription_groups(id,owner_user_id,name,token,client_name,routing_preset_id,routing_bindings_json,created_at,updated_at)
			SELECT 21,1,'Personal','stub','owner',id,'{"group":[10,11]}',1,2 FROM subscription_routing_presets WHERE is_default=1`,
		`INSERT INTO personal_subscription_nodes(id,group_id,source_type,source_id,display_name,position,created_at,updated_at)
			VALUES (10,21,'remote_node',32,'Individual',1,3,4),(11,21,'remote_source',17,'Whole source',2,3,4)`,
	}
	mode := "url"
	if version >= 43 {
		statements = append(statements,
			`INSERT INTO remote_subscription_connectors(id,owner_user_id,name,token_hash,pair_expires_at)
				VALUES (1,1,'Paired','connector-hash',NULL),(2,1,'Pending','pair-hash',50)`,
			`UPDATE remote_subscription_sources SET refresh_method='local_connector',connector_id=1,
				refresh_claim='connector-claim',refresh_lease_until=99,last_error='connector error' WHERE id=17`,
			`UPDATE remote_subscription_sources SET refresh_method='local_connector' WHERE id=18`,
		)
		mode = "manual"
	}
	if version >= 44 {
		statements = append(statements, `INSERT INTO connector_proxy_settings VALUES (1,'["192.0.2.1"]')`)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return mode, readRemoteSourceCleanupData(t, db)
}

func readRemoteSourceCleanupData(t *testing.T, db *sql.DB) []string {
	t.Helper()
	queries := []string{
		`SELECT json_group_array(json_array(id,owner_user_id,name,url_ciphertext,user_agent,enabled,interval_seconds,last_attempt_at,last_success_at,
			warnings_json,upload,download,total,expire,revision,cache_generation,created_at,updated_at) ORDER BY id) FROM remote_subscription_sources`,
		`SELECT json_group_array(json_array(id,source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) ORDER BY position)
			FROM remote_subscription_nodes`,
		`SELECT g.routing_bindings_json || json_group_array(json_array(n.id,n.source_type,n.source_id,n.display_name,n.position) ORDER BY n.position)
			FROM personal_subscription_groups g JOIN personal_subscription_nodes n ON n.group_id=g.id WHERE g.id=21 GROUP BY g.id`,
		`SELECT u.username || u.password_hash || u.role || s.token_hash || s.expires_at FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.id=3`,
	}
	snapshot := make([]string, len(queries))
	for i, query := range queries {
		if err := db.QueryRow(query).Scan(&snapshot[i]); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func assertRemoteSourceCleanupSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	var obsolete, indexes int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('remote_subscription_connectors','connector_proxy_settings'))
		+ (SELECT COUNT(*) FROM pragma_table_info('remote_subscription_sources') WHERE name IN ('refresh_method','connector_id')),
		(SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('idx_remote_subscription_sources_owner','idx_remote_subscription_nodes_source'))`).
		Scan(&obsolete, &indexes); err != nil || obsolete != 0 || indexes != 2 {
		t.Fatalf("obsolete schema entries = %d, retained indexes = %d: %v", obsolete, indexes, err)
	}
}
