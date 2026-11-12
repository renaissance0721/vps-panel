package subscription

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteManualImportKeepsIdentityAndBindings(t *testing.T) {
	db, personal := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	insertPersonalTestUser(t, db, 2, "other", "admin")
	owner := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	other := PersonalSubscriptionActor{UserID: 2, Role: "admin"}
	keyPath := filepath.Join(t.TempDir(), "mail.key")
	s := NewRemoteService(db, keyPath)
	personal.remoteSecrets = s.secrets
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	source, err := s.Create(t.Context(), owner, RemoteInput{Mode: "manual", Name: "Manual", Enabled: true, IntervalSeconds: 300})
	if err != nil || source.Mode != "manual" || !source.Enabled {
		t.Fatalf("manual: %+v %v", source, err)
	}
	var cipher string
	if err = db.QueryRow(`SELECT url_ciphertext FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if decrypted, err := s.secrets.Decrypt(cipher); err != nil || decrypted != "" {
		t.Fatal("empty URL was not encrypted")
	}
	if _, err = s.Refresh(t.Context(), owner, source.ID); !errors.Is(err, ErrRemoteManual) {
		t.Fatalf("manual refresh: %v", err)
	}
	if _, err = db.Exec(`UPDATE remote_subscription_sources SET refresh_claim='stale',refresh_lease_until=? WHERE id=?`, now.Unix()-1, source.ID); err != nil {
		t.Fatal(err)
	}
	interrupted, err := s.Get(t.Context(), owner, source.ID)
	if err != nil || interrupted.LastError != "上次导入中断，请重新导入" {
		t.Fatalf("interrupted import: %+v %v", interrupted, err)
	}
	s.refreshDue(t.Context())
	checked, err := s.Get(t.Context(), owner, source.ID)
	if err != nil || checked.LastAttempt != nil || !checked.Enabled {
		t.Fatalf("manual scheduled: %+v %v", checked, err)
	}
	first := []byte("trojan://synthetic-one@one.example:443#One\ntrojan://synthetic-two@two.example:443#Two\n")
	if _, err = s.Import(t.Context(), other, source.ID, first); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("cross owner: %v", err)
	}
	imported, err := s.Import(t.Context(), owner, source.ID, first)
	if err != nil || imported.NodeCount != 2 || imported.LastSuccess == nil {
		t.Fatalf("import: %+v %v", imported, err)
	}
	nodes, err := s.Nodes(t.Context(), owner, source.ID)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes: %+v %v", nodes, err)
	}
	group, err := personal.CreatePersonalSubscription(t.Context(), owner, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = personal.SetPersonalSubscriptionNodes(t.Context(), owner, group.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceRemoteNode, SourceID: nodes[0].ID, DisplayName: "My node", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := personal.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	group, err = personal.SetPersonalSubscriptionRoutingBindings(t.Context(), owner, group.ID, RoutingBindings{key: {group.Nodes[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	second := "trojan://synthetic-two@two.example:443#Two\ntrojan://synthetic-one@one.example:443#Renamed\nvmess://unsupported#Skipped\n"
	imported, err = s.Import(t.Context(), owner, source.ID, []byte("\ufeff"+base64.StdEncoding.EncodeToString([]byte(second))))
	if err != nil || imported.NodeCount != 2 || len(imported.Warnings) != 1 {
		t.Fatalf("base64: %+v %v", imported, err)
	}
	updated, err := s.Nodes(t.Context(), owner, source.ID)
	if err != nil || updated[0].ID != nodes[1].ID || updated[1].ID != nodes[0].ID || updated[1].Name != "Renamed" {
		t.Fatalf("identity: %+v %v", updated, err)
	}
	data, err := personal.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "My node" || len(data.RoutingBindings[key]) != 1 {
		t.Fatalf("bindings: %+v %v", data, err)
	}
	for _, bad := range [][]byte{nil, []byte("bad data"), []byte("trojan://good@example.com:443#Good\nnot-a-valid-line\n"), []byte("proxies: []"), []byte(strings.Repeat("x", MaxRemoteImportBytes+1))} {
		if _, err = s.Import(t.Context(), owner, source.ID, bad); !errors.Is(err, ErrRemoteImport) {
			t.Fatalf("invalid import: %v", err)
		}
		after, err := s.Nodes(t.Context(), owner, source.ID)
		if err != nil || len(after) != 2 || after[1].ID != nodes[0].ID {
			t.Fatalf("cache lost: %+v %v", after, err)
		}
	}
	var stored string
	if err = db.QueryRow(`SELECT payload_ciphertext FROM remote_subscription_nodes WHERE id=?`, nodes[0].ID).Scan(&stored); err != nil || strings.Contains(stored, "synthetic") {
		t.Fatalf("plaintext stored: %v", err)
	}
	if _, err = s.Update(t.Context(), owner, source.ID, RemoteInput{Name: "Manual", URL: "https://example.com/", Enabled: true, IntervalSeconds: 300}); !errors.Is(err, ErrRemoteInvalid) {
		t.Fatalf("manual type changed: %v", err)
	}
	if err = os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Import(t.Context(), owner, source.ID, first); !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err = s.Create(t.Context(), owner, RemoteInput{Mode: "manual", Name: "Second", Enabled: true, IntervalSeconds: 300}); !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("key recreated: %v", err)
	}
	if _, err = os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key was recreated: %v", err)
	}
}

func TestRemoteImportCannotOverwriteConcurrentRefresh(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	key := filepath.Join(t.TempDir(), "mail.key")
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte("trojan://synthetic@example.com:443#Fetched\n"))
	}))
	defer server.Close()
	s := NewRemoteService(db, key)
	s.fetcher = remoteTestFetcher(server)
	other := NewRemoteService(db, key)
	source, err := s.Create(t.Context(), actor, RemoteInput{Name: "URL", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), actor, source.ID); done <- err }()
	<-started
	_, importErr := other.Import(t.Context(), actor, source.ID, []byte("trojan://synthetic@example.com:443#Imported\n"))
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(importErr, ErrRemoteBusy) {
		t.Fatalf("overlap: %v", importErr)
	}
	nodes, err := s.Nodes(t.Context(), actor, source.ID)
	if err != nil || len(nodes) != 1 || nodes[0].Name != "Fetched" {
		t.Fatalf("refresh result: %+v %v", nodes, err)
	}
}

func TestRemoteImportYAMLAndURLSource(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	s := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	source, err := s.Create(t.Context(), actor, RemoteInput{Name: "URL", URL: "https://example.com/?token=synthetic", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("proxies:\n  - {name: First, type: trojan, server: example.com, port: 443, password: synthetic}\n  - {name: Unknown, type: unsupported, server: example.com, port: 443}\n")
	value, err := s.Import(t.Context(), actor, source.ID, body)
	if err != nil || value.Mode != "url" || value.NodeCount != 1 || len(value.Warnings) != 1 {
		t.Fatalf("YAML: %+v %v", value, err)
	}
	var revision, generation int64
	var ciphertext string
	if err = db.QueryRow(`SELECT revision,cache_generation,url_ciphertext FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&revision, &generation, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if address, err := s.secrets.Decrypt(ciphertext); err != nil || address != "https://example.com/?token=synthetic" {
		t.Fatal("URL changed on import")
	}
	_, err = db.Exec(`UPDATE remote_subscription_sources SET refresh_claim='other',refresh_lease_until=? WHERE id=?`, s.now().Unix()+60, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Import(t.Context(), actor, source.ID, body); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("concurrent import: %v", err)
	}
	var after int64
	if err = db.QueryRow(`SELECT cache_generation FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&after); err != nil || after != generation {
		t.Fatalf("busy import changed cache: %v", err)
	}
}
