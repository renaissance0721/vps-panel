package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRemoteSubscriptionAPIRequiresManagerAndIsolatesOwners(t *testing.T) {
	db, _, admin, _ := setupAccountTest(t)
	defer db.Close()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{DataDir: t.TempDir()})
	vip, _ := registerAccount(t, db, handler, admin, "vip", "remote-vip")
	carpool, _ := registerAccount(t, db, handler, admin, "carpool", "remote-carpool")
	subscriber, _ := registerAccount(t, db, handler, admin, "subscriber", "remote-subscriber")
	for _, cookie := range []*http.Cookie{carpool, subscriber} {
		response := performRequest(t, handler, http.MethodGet, "/api/remote-subscriptions", nil, cookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("non-manager status: %d", response.Code)
		}
		response = performRequest(t, handler, http.MethodPost, "/api/remote-subscriptions/test", map[string]string{"url": "http://127.0.0.1/"}, cookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("non-manager test status: %d", response.Code)
		}
	}
	secret := "synthetic-url-secret"
	input := map[string]any{"name": "Private", "url": "https://example.com/path?token=" + secret, "user_agent": "synthetic-agent", "enabled": true, "interval_seconds": 300}
	created := performRequest(t, handler, http.MethodPost, "/api/remote-subscriptions", input, vip)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), secret) || strings.Contains(created.Body.String(), "example.com") {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		Source struct {
			ID int64 `json:"id"`
		} `json:"source"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil || payload.Source.ID == 0 {
		t.Fatalf("decode create: %v", err)
	}
	path := fmt.Sprintf("/api/remote-subscriptions/%d", payload.Source.ID)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		response := performRequest(t, handler, method, path, nil, admin)
		if response.Code != http.StatusNotFound {
			t.Fatalf("cross-owner %s status=%d", method, response.Code)
		}
	}
	listed := performRequest(t, handler, http.MethodGet, "/api/remote-subscriptions", nil, vip)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), secret) || strings.Contains(listed.Body.String(), "example.com") {
		t.Fatalf("unsafe list: %d %s", listed.Code, listed.Body.String())
	}
	nodes := performRequest(t, handler, http.MethodGet, path+"/nodes", nil, vip)
	if nodes.Code != http.StatusOK || strings.Contains(nodes.Body.String(), secret) {
		t.Fatalf("node response: %d %s", nodes.Code, nodes.Body.String())
	}
	input["url"] = "http://127.0.0.1/private"
	bad := performRequest(t, handler, http.MethodPut, path, input, vip)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("private URL accepted: %d", bad.Code)
	}
	input["url"] = ""
	input["name"] = "Renamed"
	updated := performRequest(t, handler, http.MethodPut, path, input, vip)
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	var saved, auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_sources WHERE id=? AND url_ciphertext NOT LIKE ?`, payload.Source.ID, "%"+secret+"%").Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("encrypted source count=%d err=%v", saved, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE summary LIKE ?`, "%"+secret+"%").Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatal("audit secret leak")
	}
	deleted := performRequest(t, handler, http.MethodDelete, path, nil, vip)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
}
