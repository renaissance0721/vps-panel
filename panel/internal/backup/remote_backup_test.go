package backup_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/mail"
	"github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestRemoteBackupRestoresKeyAndRejectsMissingOrWrongKey(t *testing.T) {
	const version, domain = "v1.2.3", "panel.example.com"
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'admin','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, mail.KeyFileName)
	service := subscription.NewRemoteService(db, key)
	actor := subscription.PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	input := subscription.RemoteInput{Name: "Airport", URL: "https://example.com/?token=synthetic", Enabled: true, IntervalSeconds: 300}
	source, err := service.Create(t.Context(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	store := mail.NewRemoteSubscriptionSecretStore(key)
	name, err := store.Encrypt("Node")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := store.Encrypt("trojan://synthetic@example.com:443#Node")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO remote_subscription_nodes(source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) VALUES(?,?,?,?,?,?,?,?,?)`, source.ID, "synthetic-id", name, name, "trojan", "uri", payload, 0, 1); err != nil {
		t.Fatal(err)
	}
	manual, err := service.Create(t.Context(), actor, subscription.RemoteInput{Mode: "manual", Name: "Manual", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Import(t.Context(), actor, manual.ID, []byte("trojan://synthetic-manual@example.com:443#ManualNode\n")); err != nil {
		t.Fatal(err)
	}
	archive, cleanup, err := backup.CreateArchive(t.Context(), db, dir, version, domain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	contents := zipContents(t, archive)
	keyName := "data/" + mail.KeyFileName
	contents[keyName] = []byte(strings.Repeat("x", 32))
	wrongHash := sha256.Sum256(contents[keyName])
	lines := strings.Split(string(contents["SHA256SUMS"]), "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "  "+keyName) {
			lines[i] = fmt.Sprintf("%x  %s", wrongHash, keyName)
		}
	}
	contents["SHA256SUMS"] = []byte(strings.Join(lines, "\n"))
	wrongArchive := writeZip(t, contents, nil)
	if err := backup.StageImport(t.Context(), wrongArchive, t.TempDir(), version, domain); err == nil {
		t.Fatal("restore accepted wrong key")
	}
	delete(contents, keyName)
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasSuffix(line, "  "+keyName) {
			kept = append(kept, line)
		}
	}
	contents["SHA256SUMS"] = []byte(strings.Join(kept, "\n"))
	missingArchive := writeZip(t, contents, nil)
	if err := backup.StageImport(t.Context(), missingArchive, t.TempDir(), version, domain); err == nil {
		t.Fatal("restore accepted missing key")
	}
	targetDir := t.TempDir()
	if err := backup.StageImport(t.Context(), archive, targetDir, version, domain); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	restored := subscription.NewRemoteService(target, filepath.Join(targetDir, mail.KeyFileName))
	nodes, err := restored.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(nodes) != 1 || nodes[0].Name != "Node" {
		t.Fatalf("restored nodes: %+v %v", nodes, err)
	}
	manualState, err := restored.Get(t.Context(), actor, manual.ID)
	if err != nil || manualState.Mode != "manual" || !manualState.Enabled {
		t.Fatalf("restored manual source: %+v %v", manualState, err)
	}
	manualNodes, err := restored.Nodes(t.Context(), actor, manual.ID)
	if err != nil || len(manualNodes) != 1 || manualNodes[0].Name != "ManualNode" {
		t.Fatalf("restored manual nodes: %+v %v", manualNodes, err)
	}
	input.URL = ""
	if _, err := restored.Update(t.Context(), actor, source.ID, input); err != nil {
		t.Fatalf("restored URL key: %v", err)
	}
	if err := attempt.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := backup.CreateArchive(t.Context(), db, dir, version, domain, "", ""); err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("backup accepted wrong key")
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := backup.CreateArchive(t.Context(), db, dir, version, domain, "", ""); err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("backup accepted missing key")
	}
}
