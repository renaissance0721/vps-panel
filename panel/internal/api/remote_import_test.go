package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteImportAPIEnforcesOwnerSizeAndRedaction(t *testing.T) {
	db, _, admin, _ := setupAccountTest(t)
	defer db.Close()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{DataDir: t.TempDir()})
	vip, _ := registerAccount(t, db, handler, admin, "vip", "file-owner")
	other, _ := registerAccount(t, db, handler, admin, "vip", "file-other")
	carpool, _ := registerAccount(t, db, handler, admin, "carpool", "file-carpool")
	created := performRequest(t, handler, http.MethodPost, "/api/remote-subscriptions", map[string]any{"mode": "manual", "name": "Manual", "enabled": true, "interval_seconds": 300}, vip)
	var data struct {
		Source struct {
			ID   int64  `json:"id"`
			Mode string `json:"mode"`
		} `json:"source"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &data) != nil || data.Source.Mode != "manual" {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	path := fmt.Sprintf("/api/remote-subscriptions/%d/import", data.Source.ID)
	request := func(cookie *http.Cookie, content string, more bool) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "synthetic-secret.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if more {
			if err = writer.WriteField("extra", "value"); err != nil {
				t.Fatal(err)
			}
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if cookie != nil {
			req.AddCookie(cookie)
			req.Header.Set("Origin", requestBaseURL(req))
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	fixture := "trojan://synthetic-credential@example.com:443#Node\nvmess://unsupported#Skip\n"
	for _, test := range []struct {
		cookie *http.Cookie
		status int
	}{{other, http.StatusNotFound}, {carpool, http.StatusForbidden}, {nil, http.StatusUnauthorized}} {
		out := request(test.cookie, fixture, false)
		if out.Code != test.status || strings.Contains(out.Body.String(), "synthetic") {
			t.Fatalf("ownership: %d %s", out.Code, out.Body.String())
		}
	}
	out := request(vip, fixture, false)
	if out.Code != http.StatusOK || strings.Contains(out.Body.String(), "synthetic") || !strings.Contains(out.Body.String(), "warnings") {
		t.Fatalf("import: %d %s", out.Code, out.Body.String())
	}
	for _, test := range []struct {
		body   string
		more   bool
		status int
	}{{"bad synthetic-credential", false, http.StatusBadRequest}, {fixture, true, http.StatusBadRequest}, {strings.Repeat("x", (4<<20)+1), false, http.StatusRequestEntityTooLarge}} {
		out = request(vip, test.body, test.more)
		if out.Code != test.status || strings.Contains(out.Body.String(), "synthetic") {
			t.Fatalf("invalid: %d %s", out.Code, out.Body.String())
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_nodes WHERE source_id=?`, data.Source.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cache changed: %d %v", count, err)
	}
	var leaks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE summary LIKE '%synthetic%'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatal("audit data leaked")
	}
}
