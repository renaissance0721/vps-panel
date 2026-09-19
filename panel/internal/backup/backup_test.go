package backup_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

const testVersion = "v0.23.0"
const testDomain = "panel.example.com"

func TestFullSnapshotRoundTripAndReplace(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	seedFullDatabase(t, source)
	want := rowsByTable(t, source)
	var journal string
	if err := source.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal=%q: %v", journal, err)
	}
	archive, cleanup, err := backup.CreateArchive(context.Background(), source, sourceDir, testVersion, testDomain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	archiveReader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveReader.File) != 3 {
		t.Fatalf("ZIP entries=%d", len(archiveReader.File))
	}
	archiveReader.Close()
	targetDir := t.TempDir()
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, target, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (999,'discard','old','admin',1,1)`)
	target.Close()
	if err := backup.StageImport(context.Background(), archive, targetDir, testVersion, testDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "restore", "pending.json")); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(targetDir)
	if err != nil || attempt == nil {
		t.Fatalf("apply=%v, %v", attempt, err)
	}
	restored, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := backup.ValidateDatabase(context.Background(), filepath.Join(targetDir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Commit(); err != nil {
		t.Fatal(err)
	}
	got := rowsByTable(t, restored)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("database rows changed after restore\nwant: %#v\ngot: %#v", want, got)
	}
	var count int
	if err := restored.QueryRow(`SELECT count(*) FROM users WHERE id=999`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("target-only user survived: %d, %v", count, err)
	}
	var agentHash, credential, config string
	if err := restored.QueryRow(`SELECT token_hash FROM agents WHERE id=31`).Scan(&agentHash); err != nil || agentHash != token.Hash("agent-secret") {
		t.Fatalf("agent identity changed: %q %v", agentHash, err)
	}
	agent, err := agentcontrol.NewService(restored, time.Now).AuthenticateAgent(context.Background(), "agent-secret")
	if err != nil || agent.ID != 31 || agent.ServerID != 31 {
		t.Fatalf("original Agent token cannot reconnect: %+v, %v", agent, err)
	}
	if err := restored.QueryRow(`SELECT credential_json FROM clients WHERE id=51`).Scan(&credential); err != nil || credential != `{"id":"client-uuid"}` {
		t.Fatalf("client credential changed: %q %v", credential, err)
	}
	if err := restored.QueryRow(`SELECT config_json FROM proxies WHERE id=41`).Scan(&config); err != nil || config != `{"privateKey":"reality-key"}` {
		t.Fatalf("proxy config changed: %q %v", config, err)
	}
}

func TestImportRejectsInvalidArchives(t *testing.T) {
	sourceDir := t.TempDir()
	db, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	archive, cleanup, err := backup.CreateArchive(context.Background(), db, sourceDir, testVersion, testDomain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	base := zipContents(t, archive)
	t.Run("older formal version imports into newer Panel", func(t *testing.T) {
		if err := backup.StageImport(context.Background(), archive, t.TempDir(), "v0.24.0", testDomain); err != nil {
			t.Fatal(err)
		}
	})
	tests := []struct {
		name    string
		mutate  func(map[string][]byte)
		domain  string
		version string
	}{
		{"checksum", func(m map[string][]byte) { m["data/panel.db"][100] ^= 1 }, testDomain, testVersion},
		{"format version", func(m map[string][]byte) { changeManifest(t, m, "format_version", 99) }, testDomain, testVersion},
		{"newer panel", func(m map[string][]byte) { changeManifest(t, m, "panel_version", "v99.0.0") }, testDomain, testVersion},
		{"domain", func(map[string][]byte) {}, "other.example.com", testVersion},
		{"dev compatibility", func(map[string][]byte) {}, testDomain, "dev"},
		{"zip slip", func(m map[string][]byte) { m["../escape"] = []byte("bad") }, testDomain, testVersion},
		{"unexpected file", func(m map[string][]byte) { m["extra.txt"] = []byte("bad") }, testDomain, testVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contents := cloneContents(base)
			tc.mutate(contents)
			path := writeZip(t, contents, nil)
			if err := backup.StageImport(context.Background(), path, t.TempDir(), tc.version, tc.domain); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	t.Run("corrupt zip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.zip")
		if err := os.WriteFile(path, []byte("not a ZIP"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := backup.StageImport(context.Background(), path, t.TempDir(), testVersion, testDomain); err == nil {
			t.Fatal("corrupt ZIP accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		path := writeZip(t, cloneContents(base), map[string]os.FileMode{"data/panel.db": os.ModeSymlink | 0o777})
		if err := backup.StageImport(context.Background(), path, t.TempDir(), testVersion, testDomain); err == nil {
			t.Fatal("symlink accepted")
		}
	})
	t.Run("corrupt SQLite with valid checksums", func(t *testing.T) {
		contents := cloneContents(base)
		replaceDatabase(t, contents, []byte("SQLite format 3\x00broken"))
		if err := backup.StageImport(context.Background(), writeZip(t, contents, nil), t.TempDir(), testVersion, testDomain); err == nil {
			t.Fatal("corrupt SQLite accepted")
		}
	})
	t.Run("foreign key error with valid checksums", func(t *testing.T) {
		if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `INSERT INTO sessions(user_id,token_hash,expires_at,created_at) VALUES (999,'orphan',1,1)`)
		snapshot := filepath.Join(t.TempDir(), "invalid.db")
		if _, err := db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		contents := cloneContents(base)
		replaceDatabase(t, contents, data)
		if err := backup.StageImport(context.Background(), writeZip(t, contents, nil), t.TempDir(), testVersion, testDomain); err == nil {
			t.Fatal("foreign key error accepted")
		}
	})
	t.Run("declared zip bomb", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bomb.zip")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		header := &zip.FileHeader{Name: "data/panel.db", Method: zip.Store, UncompressedSize64: 1<<30 + 1}
		header.SetMode(0o600)
		_, err = writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		writer.Close()
		file.Close()
		if err := backup.StageImport(context.Background(), path, t.TempDir(), testVersion, testDomain); err == nil {
			t.Fatal("zip bomb accepted")
		}
	})
}

func TestArchiveIncludesReadableDeploymentFiles(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	environment := filepath.Join(t.TempDir(), "environment")
	caddy := filepath.Join(t.TempDir(), "vps-panel.caddy")
	if err := os.WriteFile(environment, []byte("PANEL_DOMAIN=panel.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caddy, []byte("panel.example.com { reverse_proxy localhost:8080 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive, cleanup, err := backup.CreateArchive(context.Background(), db, dataDir, testVersion, testDomain, environment, caddy)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	contents := zipContents(t, archive)
	if string(contents["deployment/environment"]) != "PANEL_DOMAIN=panel.example.com\n" ||
		string(contents["deployment/vps-panel.caddy"]) != "panel.example.com { reverse_proxy localhost:8080 }\n" {
		t.Fatalf("deployment files missing: %v", contents)
	}
	if err := backup.StageImport(context.Background(), archive, t.TempDir(), testVersion, testDomain); err != nil {
		t.Fatal(err)
	}
}

func replaceDatabase(t *testing.T, contents map[string][]byte, data []byte) {
	t.Helper()
	contents["data/panel.db"] = data
	hash := sha256.Sum256(data)
	changeManifest(t, contents, "database_sha256", hex.EncodeToString(hash[:]))
	sums := strings.Split(string(contents["SHA256SUMS"]), "\n")
	for i, line := range sums {
		if strings.HasSuffix(line, "  data/panel.db") {
			sums[i] = hex.EncodeToString(hash[:]) + "  data/panel.db"
		}
	}
	contents["SHA256SUMS"] = []byte(strings.Join(sums, "\n"))
}

func TestStagedDatabaseTamperingLeavesOriginalDatabase(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	archive, cleanup, err := backup.CreateArchive(context.Background(), source, sourceDir, testVersion, testDomain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	targetDir := t.TempDir()
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, target, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (88,'original','hash','admin',1,1)`)
	target.Close()
	if err := backup.StageImport(context.Background(), archive, targetDir, testVersion, testDomain); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "restore", "panel.db"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.ApplyPendingRestore(targetDir); err == nil {
		t.Fatal("tampered stage accepted")
	}
	current, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var name string
	if err := current.QueryRow(`SELECT username FROM users WHERE id=88`).Scan(&name); err != nil || name != "original" {
		t.Fatalf("old DB unavailable: %q %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "restore", "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("pending marker remained: %v", err)
	}
}

func TestRollbackRestoresOriginalSQLiteFiles(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	archive, cleanup, err := backup.CreateArchive(context.Background(), source, sourceDir, testVersion, testDomain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	targetDir := t.TempDir()
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, target, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (88,'original','hash','admin',1,1)`)
	target.Close()
	if err := backup.StageImport(context.Background(), archive, targetDir, testVersion, testDomain); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(targetDir)
	if err != nil || attempt == nil {
		t.Fatalf("apply=%v: %v", attempt, err)
	}
	if err := attempt.Rollback(); err != nil {
		t.Fatal(err)
	}
	current, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var username string
	if err := current.QueryRow(`SELECT username FROM users WHERE id=88`).Scan(&username); err != nil || username != "original" {
		t.Fatalf("rollback data=%q: %v", username, err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "restore", "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("pending marker remained: %v", err)
	}
}

func seedFullDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (11,'admin','password-hash','admin',100,101),(12,'vip','another-hash','vip',102,103)`,
		`INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at) VALUES (21,11,'session-hash',900,110)`,
		`INSERT INTO admin_invitations(id,token_hash,created_by,expires_at,created_at) VALUES (22,'invitation-hash',11,900,111)`,
		`INSERT INTO servers(id,name,status,visibility,outbound_preference,desired_state_version,created_at,updated_at) VALUES (31,'server','offline','private','prefer_ipv6',7,120,121)`,
		`INSERT INTO server_access(server_id,user_id) VALUES (31,12)`,
		`INSERT INTO user_server_order(user_id,server_id,position) VALUES (12,31,3)`,
		`INSERT INTO agent_enrollments(id,server_id,token_hash,expires_at,created_at) VALUES (32,31,'enrollment-hash',900,122)`,
		fmt.Sprintf(`INSERT INTO agents(id,server_id,token_hash,version,registered_at,created_at,updated_at) VALUES (31,31,'%s','v0.23.0',123,123,124)`, token.Hash("agent-secret")),
		`INSERT INTO server_system_info(server_id,hostname,os_name,os_version,kernel,arch,ipv4,ipv6,agent_version,reported_at) VALUES (31,'host','Linux','1','kernel','amd64','1.2.3.4','::1','v0.23.0',125)`,
		`INSERT INTO server_metrics(server_id,cpu_percent,memory_used_bytes,memory_total_bytes,disk_used_bytes,disk_total_bytes,uptime_seconds,updated_at) VALUES (31,12.5,2,4,5,10,30,126)`,
		`INSERT INTO proxies(id,server_id,name,protocol,listen_port,config_json,created_at,updated_at) VALUES (41,31,'proxy','vless',443,'{"privateKey":"reality-key"}',130,131)`,
		`INSERT INTO user_proxy_order(user_id,proxy_id,position) VALUES (12,41,4)`,
		`INSERT INTO clients(id,proxy_id,name,credential_json,created_at,updated_at) VALUES (51,41,'client','{"id":"client-uuid"}',140,141)`,
		`INSERT INTO client_metrics(client_id,xray_uplink_bytes,xray_downlink_bytes,cycle_started_at,updated_at) VALUES (51,345,678,142,143)`,
		`INSERT INTO relays(id,server_id,name,listen_port,target_type,target_proxy_id,target_client_id,network,created_at,updated_at) VALUES (61,31,'relay',8443,'proxy',41,51,'tcp',150,151)`,
		`INSERT INTO user_relay_order(user_id,relay_id,position) VALUES (12,61,5)`,
	} {
		mustExec(t, db, statement)
	}
}

func mustExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func rowsByTable(t *testing.T, db *sql.DB) map[string][][]string {
	t.Helper()
	names, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	names.Close()
	result := make(map[string][][]string)
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + table + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			line := make([]string, len(values))
			for i, value := range values {
				if data, ok := value.([]byte); ok {
					line[i] = hex.EncodeToString(data)
				} else {
					line[i] = fmt.Sprint(value)
				}
			}
			result[table] = append(result[table], line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return result
}

func zipContents(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	contents := make(map[string][]byte)
	for _, entry := range reader.File {
		part, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		part.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[entry.Name] = data
	}
	return contents
}

func cloneContents(source map[string][]byte) map[string][]byte {
	result := make(map[string][]byte)
	for key, value := range source {
		result[key] = bytes.Clone(value)
	}
	return result
}

func writeZip(t *testing.T, contents map[string][]byte, modes map[string]os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		mode := os.FileMode(0o600)
		if modes != nil && modes[name] != 0 {
			mode = modes[name]
		}
		header.SetMode(mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func changeManifest(t *testing.T, contents map[string][]byte, key string, value any) {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest[key] = value
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	contents["manifest.json"] = data
	sums := strings.Split(string(contents["SHA256SUMS"]), "\n")
	for i, line := range sums {
		if strings.HasSuffix(line, "  manifest.json") {
			hash := sha256.Sum256(data)
			sums[i] = hex.EncodeToString(hash[:]) + "  manifest.json"
		}
	}
	contents["SHA256SUMS"] = []byte(strings.Join(sums, "\n"))
}
