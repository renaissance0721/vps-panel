package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

func TestPersonalRemoteAPIOwnerMetadataAndPreview(t *testing.T) {
	db, _, admin, _ := setupAccountTest(t)
	defer db.Close()
	dir := t.TempDir()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{DataDir: dir})
	vip, _ := registerAccount(t, db, handler, admin, "vip", "remote-owner")
	other, _ := registerAccount(t, db, handler, admin, "vip", "remote-other")
	created := performRequest(t, handler, http.MethodPost, "/api/remote-subscriptions", map[string]any{"name": "Airport", "url": "https://example.com/?token=synthetic-url", "enabled": true, "interval_seconds": 300}, vip)
	var remote struct {
		Source struct {
			ID int64 `json:"id"`
		} `json:"source"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &remote) != nil {
		t.Fatalf("source: %d %s", created.Code, created.Body.String())
	}
	store := mailservice.NewRemoteSubscriptionSecretStore(filepath.Join(dir, mailservice.KeyFileName))
	name, err := store.EncryptExisting("Remote")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := store.EncryptExisting("trojan://synthetic-password@remote.example:443#Remote")
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO remote_subscription_nodes(source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) VALUES(?,?,?,?,?,?,?,?,?)`, remote.Source.ID, "synthetic-identity", name, name, "trojan", "uri", payload, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	sources := performRequest(t, handler, http.MethodGet, "/api/personal-subscriptions/sources?client_name=remote-owner", nil, vip)
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), "remote_node") || strings.Contains(sources.Body.String(), "synthetic-password") || strings.Contains(sources.Body.String(), "synthetic-url") {
		t.Fatalf("sources: %d %s", sources.Code, sources.Body.String())
	}
	otherSources := performRequest(t, handler, http.MethodGet, "/api/personal-subscriptions/sources?client_name=remote-other", nil, other)
	if strings.Contains(otherSources.Body.String(), "Airport") {
		t.Fatal("source leaked to other owner")
	}
	groupResult := performRequest(t, handler, http.MethodPost, "/api/personal-subscriptions", map[string]any{"name": "Daily", "client_name": "remote-owner", "enabled": true}, vip)
	var group struct {
		Personal personalSubscriptionResponse `json:"personal_subscription"`
	}
	if groupResult.Code != http.StatusCreated || json.Unmarshal(groupResult.Body.Bytes(), &group) != nil {
		t.Fatalf("group: %d %s", groupResult.Code, groupResult.Body.String())
	}
	path := fmt.Sprintf("/api/personal-subscriptions/%d", group.Personal.ID)
	saved := performRequest(t, handler, http.MethodPut, path+"/nodes", map[string]any{"nodes": []map[string]any{{"source_type": "remote_node", "source_id": nodeID, "display_name": "My node", "enabled": true}}}, vip)
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), "synthetic-password") || strings.Contains(saved.Body.String(), "synthetic-url") {
		t.Fatalf("saved: %d %s", saved.Code, saved.Body.String())
	}
	preview := performRequest(t, handler, http.MethodGet, path+"/mihomo-preview", nil, vip)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "synthetic-password") || preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	denied := performRequest(t, handler, http.MethodGet, path+"/mihomo-preview", nil, other)
	if denied.Code != http.StatusNotFound || strings.Contains(denied.Body.String(), "synthetic-password") {
		t.Fatalf("cross owner preview: %d", denied.Code)
	}
	bad, err := store.EncryptExisting("trojan://synthetic-password@remote.example:443?unsupported=synthetic-value#Remote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE remote_subscription_nodes SET payload_ciphertext=? WHERE id=?`, bad, nodeID); err != nil {
		t.Fatal(err)
	}
	preview = performRequest(t, handler, http.MethodGet, path+"/mihomo-preview", nil, vip)
	if preview.Code != http.StatusUnprocessableEntity || strings.Contains(preview.Body.String(), "synthetic-") {
		t.Fatalf("unsupported preview: %d %s", preview.Code, preview.Body.String())
	}
	address, err := url.Parse(group.Personal.SubscriptionBase64URL)
	if err != nil {
		t.Fatal(err)
	}
	public := performRequest(t, handler, http.MethodGet, address.Path, nil, nil)
	decoded, err := base64.StdEncoding.DecodeString(public.Body.String())
	if public.Code != http.StatusOK || err != nil || !strings.Contains(string(decoded), "unsupported=synthetic-value") {
		t.Fatalf("base64 did not preserve URI: %d %v", public.Code, err)
	}
}
