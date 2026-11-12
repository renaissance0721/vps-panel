package subscription

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteServiceRefreshPreservesCacheAndIdentity(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "admin", "admin")
	insertPersonalTestUser(t, db, 2, "vip", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	other := PersonalSubscriptionActor{UserID: 2, Role: "vip"}
	var mu sync.Mutex
	body := "trojan://synthetic-one@one.example:443#One\ntrojan://synthetic-two@two.example:443#Two\n"
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Subscription-Userinfo", "upload=11; download=22; total=33; expire=1700100000")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "mail.key")
	s := NewRemoteService(db, keyPath)
	s.fetcher = remoteTestFetcher(server)
	now := time.Unix(1700000000, 0).UTC()
	s.now = func() time.Time { return now }
	input := RemoteInput{Name: "Test", URL: "http://public.example/?token=synthetic-url-secret", Enabled: true, IntervalSeconds: 300}
	created, err := s.Create(t.Context(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), other, created.ID); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("cross owner read: %v", err)
	}
	if _, err := s.Update(t.Context(), other, created.ID, input); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("cross owner update: %v", err)
	}
	if err := s.Delete(t.Context(), other, created.ID); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("cross owner delete: %v", err)
	}
	first, err := s.Refresh(t.Context(), actor, created.ID)
	if err != nil || first.Refreshing || first.NodeCount != 2 || first.Upload == nil || *first.Upload != 11 || first.LastSuccess == nil {
		t.Fatalf("first refresh: %+v, %v", first, err)
	}
	nodes, err := s.Nodes(t.Context(), actor, created.ID)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes: %d, %v", len(nodes), err)
	}
	firstID, secondID := nodes[0].ID, nodes[1].ID
	var urlCipher, payloadCipher, originalCipher string
	if err := db.QueryRow(`SELECT url_ciphertext FROM remote_subscription_sources WHERE id=?`, created.ID).Scan(&urlCipher); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT payload_ciphertext,original_name FROM remote_subscription_nodes WHERE id=?`, firstID).Scan(&payloadCipher, &originalCipher); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(urlCipher, "synthetic-url-secret") || strings.Contains(payloadCipher, "synthetic-one") || strings.Contains(originalCipher, "One") {
		t.Fatal("stored plaintext")
	}
	if original, err := s.secrets.Decrypt(originalCipher); err != nil || original != "One" {
		t.Fatalf("original node name not preserved: %v", err)
	}
	if payload, err := s.secrets.Decrypt(payloadCipher); err != nil || payload != "trojan://synthetic-one@one.example:443#One" {
		t.Fatalf("node payload not preserved: %v", err)
	}
	if _, err := s.Refresh(t.Context(), actor, created.ID); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("cooldown: %v", err)
	}
	mu.Lock()
	body = "trojan://synthetic-two@two.example:443#Two\ntrojan://synthetic-one@one.example:443#Renamed\nvmess://unsupported#Other\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	second, err := s.Refresh(t.Context(), actor, created.ID)
	if err != nil || len(second.Warnings) != 1 {
		t.Fatalf("second refresh: %+v, %v", second, err)
	}
	nodes, _ = s.Nodes(t.Context(), actor, created.ID)
	if len(nodes) != 2 || nodes[0].ID != secondID || nodes[1].ID != firstID || nodes[1].Name != "Renamed" {
		t.Fatalf("identity or ordering: %+v", nodes)
	}
	input.URL = "http://public.example/?token=synthetic-replacement"
	edited, err := s.Update(t.Context(), actor, created.ID, input)
	if err != nil || edited.NodeCount != 2 || edited.LastError == "" {
		t.Fatalf("URL replacement lost cache: %+v %v", edited, err)
	}
	if err := db.QueryRow(`SELECT url_ciphertext FROM remote_subscription_sources WHERE id=?`, created.ID).Scan(&urlCipher); err != nil {
		t.Fatal(err)
	}
	if address, err := s.secrets.Decrypt(urlCipher); err != nil || address != input.URL {
		t.Fatalf("URL replacement not preserved: %v", err)
	}
	mu.Lock()
	status = http.StatusInternalServerError
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err := s.Refresh(t.Context(), actor, created.ID); err == nil {
		t.Fatal("failed refresh succeeded")
	}
	failed, _ := s.Get(t.Context(), actor, created.ID)
	if failed.NodeCount != 2 || failed.LastError == "" || !failed.LastSuccess.Equal(*second.LastSuccess) {
		t.Fatalf("failed refresh changed cache: %+v", failed)
	}
	mu.Lock()
	status = http.StatusOK
	body = "vmess://unsupported#Node\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err := s.Refresh(t.Context(), actor, created.ID); err == nil {
		t.Fatal("unsupported-only refresh succeeded")
	}
	failed, _ = s.Get(t.Context(), actor, created.ID)
	if failed.NodeCount != 2 || len(failed.Warnings) != 1 {
		t.Fatalf("unsupported warning or cache missing: %+v", failed)
	}
	mu.Lock()
	body = "proxies: []\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err := s.Refresh(t.Context(), actor, created.ID); err == nil {
		t.Fatal("empty refresh succeeded")
	}
	failed, _ = s.Get(t.Context(), actor, created.ID)
	if failed.NodeCount != 2 {
		t.Fatalf("empty refresh removed cache: %+v", failed)
	}
	mu.Lock()
	status = http.StatusOK
	body = "trojan://synthetic-two@two.example:443#Two\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err := s.Refresh(t.Context(), actor, created.ID); err != nil {
		t.Fatal(err)
	}
	nodes, _ = s.Nodes(t.Context(), actor, created.ID)
	if len(nodes) != 1 || nodes[0].ID != secondID {
		t.Fatalf("removed upstream node not pruned: %+v", nodes)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Nodes(t.Context(), actor, created.ID); !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := s.Create(t.Context(), actor, input); !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("missing key recreated: %v", err)
	}
	if err := s.Delete(t.Context(), actor, created.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_nodes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cache after deletion: %d, %v", count, err)
	}
}

func TestRemoteServiceSchedulerRespectsIntervalAndDisabled(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "admin", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "trojan://synthetic@example.com:443#One\n")
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "mail.key")
	s := NewRemoteService(db, keyPath)
	s.fetcher = remoteTestFetcher(server)
	now := time.Unix(1700000000, 0).UTC()
	s.now = func() time.Time { return now }
	input := RemoteInput{Name: "Enabled", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300}
	source, err := s.Create(t.Context(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "Disabled"
	input.Enabled = false
	if _, err := s.Create(t.Context(), actor, input); err != nil {
		t.Fatal(err)
	}
	s.refreshDue(t.Context())
	if requests.Load() != 1 {
		t.Fatalf("first scheduled request count=%d", requests.Load())
	}
	now = now.Add(299 * time.Second)
	s.refreshDue(t.Context())
	if requests.Load() != 1 {
		t.Fatalf("early scheduled request count=%d", requests.Load())
	}
	now = now.Add(time.Second)
	s.refreshDue(t.Context())
	if requests.Load() != 2 {
		t.Fatalf("due request count=%d", requests.Load())
	}
	updated, err := s.Get(t.Context(), actor, source.ID)
	if err != nil || updated.NodeCount != 1 || updated.LastSuccess == nil {
		t.Fatalf("scheduled state: %+v %v", updated, err)
	}
}

func TestRemoteServiceConcurrentAndScheduledRefresh(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "vip", "vip")
	insertPersonalTestUser(t, db, 2, "other", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "trojan://synthetic@example.com:443#One\n")
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "mail.key")
	s := NewRemoteService(db, keyPath)
	s.fetcher = remoteTestFetcher(server)
	s.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	source, err := s.Create(t.Context(), actor, RemoteInput{Name: "Source", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), actor, source.ID); done <- err }()
	<-started
	active, err := s.Get(t.Context(), actor, source.ID)
	if err != nil || !active.Refreshing {
		t.Fatalf("refresh status not observable: %+v %v", active, err)
	}
	if _, err := s.Refresh(t.Context(), PersonalSubscriptionActor{UserID: 2, Role: "admin"}, source.ID); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("active source leaked to another owner: %v", err)
	}
	if _, err := s.Refresh(t.Context(), actor, source.ID); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("overlap: %v", err)
	}
	otherInstance := NewRemoteService(db, keyPath)
	otherInstance.fetcher = remoteTestFetcher(server)
	otherInstance.now = func() time.Time { return time.Unix(1700000061, 0).UTC() }
	if _, err := otherInstance.Refresh(t.Context(), actor, source.ID); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("cross-instance overlap: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.refreshDue(t.Context()) // last attempt is not yet due
	select {
	case <-started:
		t.Fatal("scheduler ignored interval")
	default:
	}
	if _, err := s.Create(t.Context(), actor, RemoteInput{Name: "Bad", URL: "http://127.0.0.1/secret", IntervalSeconds: 300}); !errors.Is(err, ErrRemoteInvalid) {
		t.Fatalf("private URL: %v", err)
	}
}

func TestRemoteServiceRecoversExpiredRefreshLease(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "vip", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "trojan://synthetic@example.com:443#Node\n")
	}))
	defer server.Close()
	s := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	s.fetcher = remoteTestFetcher(server)
	now := time.Unix(1700000000, 0).UTC()
	s.now = func() time.Time { return now }
	source, err := s.Create(t.Context(), actor, RemoteInput{Name: "Source", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE remote_subscription_sources SET refresh_claim='abandoned',refresh_lease_until=?,last_attempt_at=? WHERE id=?`, now.Unix()+10, now.Unix()-61, source.ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.Get(t.Context(), actor, source.ID)
	if err != nil || !state.Refreshing {
		t.Fatalf("active lease not visible: %+v %v", state, err)
	}
	if _, err := s.Refresh(t.Context(), actor, source.ID); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("active lease ignored: %v", err)
	}
	now = now.Add(11 * time.Second)
	state, err = s.Get(t.Context(), actor, source.ID)
	if err != nil || state.Refreshing || state.LastError == "" {
		t.Fatalf("interrupted refresh not visible: %+v %v", state, err)
	}
	s.refreshDue(t.Context())
	state, err = s.Get(t.Context(), actor, source.ID)
	if err != nil || state.Refreshing || state.NodeCount != 1 {
		t.Fatalf("expired lease not recovered: %+v %v", state, err)
	}
}

func TestRemoteServiceRefreshRejectsKeyChangeDuringFetch(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "admin", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "trojan://synthetic@example.com:443#Node\n")
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "mail.key")
	s := NewRemoteService(db, keyPath)
	s.fetcher = remoteTestFetcher(server)
	source, err := s.Create(t.Context(), actor, RemoteInput{Name: "Source", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), actor, source.ID); done <- err }()
	<-started
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrRemoteKey) {
		t.Fatalf("changed key accepted: %v", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := s.Get(t.Context(), actor, source.ID)
	if err != nil || value.NodeCount != 0 || value.LastSuccess != nil || value.LastError == "" {
		t.Fatalf("failed refresh state: %+v %v", value, err)
	}
}

func TestRemoteServiceRefreshDoesNotOverwriteEditedSource(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "admin", "admin")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "trojan://synthetic@example.com:443#Node\n")
	}))
	defer server.Close()
	s := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	s.fetcher = remoteTestFetcher(server)
	input := RemoteInput{Name: "Source", URL: "http://public.example/", Enabled: true, IntervalSeconds: 300}
	source, err := s.Create(t.Context(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(t.Context(), actor, source.ID); done <- err }()
	<-started
	input.Name = "Edited"
	input.URL = ""
	input.Enabled = false
	if _, err := s.Update(t.Context(), actor, source.ID, input); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrRemoteChanged) {
		t.Fatalf("stale refresh: %v", err)
	}
	value, err := s.Get(t.Context(), actor, source.ID)
	if err != nil || value.Name != "Edited" || value.Enabled || value.NodeCount != 0 {
		t.Fatalf("edited source overwritten: %+v %v", value, err)
	}
}

func TestRemotePreviewRedactsCredentialInName(t *testing.T) {
	for _, input := range []string{
		"trojan://synthetic-password@example.com:443#name-synthetic-password\n",
		"proxies:\n  - {name: name-synthetic-password, type: trojan, server: example.com, port: 443, password: synthetic-password}\n",
		"ss://YWVzLTEyOC1nY206c3ludGhldGljLXBhc3N3b3Jk@example.com:443#name-synthetic-password\n",
	} {
		parsed, err := parseRemoteSubscription([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		preview := remotePreview(parsed, "", "https://example.com/subscription")
		if len(preview.Nodes) != 1 || strings.Contains(preview.Nodes[0].Name, "synthetic-password") {
			t.Fatalf("credential in node metadata: %+v", preview.Nodes)
		}
	}
}

func TestRemotePreviewRedactsOtherNodeAndSourceSecrets(t *testing.T) {
	parsed, err := parseRemoteSubscription([]byte("trojan://first-secret@one.example:443#source-query-secret\ntrojan://second-secret@two.example:443#first-secret\ntrojan://third-secret@three.example:443#Safe\n"))
	if err != nil {
		t.Fatal(err)
	}
	preview := remotePreview(parsed, "", "https://example.com/sub?token=source-query-secret")
	if len(preview.Nodes) != 3 || preview.Nodes[0].Name != "节点名称已隐藏" || preview.Nodes[1].Name != "节点名称已隐藏" || preview.Nodes[2].Name != "Safe" {
		t.Fatalf("unexpected preview names: %+v", preview.Nodes)
	}
}

func TestRemoteTestShowsUnsupportedWarningsWithoutCaching(t *testing.T) {
	db, _ := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "vip", "vip")
	actor := PersonalSubscriptionActor{UserID: 1, Role: "vip"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "vmess://synthetic#Node\n") }))
	defer server.Close()
	s := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	s.fetcher = remoteTestFetcher(server)
	preview, err := s.Test(t.Context(), actor, "http://public.example/?token=synthetic", "")
	if err != nil || preview.Usable || len(preview.Nodes) != 0 || len(preview.Warnings) != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := s.Test(t.Context(), actor, "http://public.example/", ""); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("preview cooldown: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_sources`).Scan(&count); err != nil || count != 0 {
		t.Fatal("preview stored a source")
	}
}
