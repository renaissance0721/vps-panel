package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestBackupAPIRequiresAdminAndSignalsRestore(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restart := make(chan struct{}, 1)
	handler := NewHandlerWithBackup(db, t.TempDir(), "v0.23.0", BackupConfig{
		DataDir: dataDir, Domain: "panel.example.com", RestoreRequested: restart,
	})
	for _, methodAndPath := range [][2]string{{"GET", "/api/admin/backup/export"}, {"POST", "/api/admin/backup/import"}} {
		response := performRequest(t, handler, methodAndPath[0], methodAndPath[1], nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s status=%d", methodAndPath[1], response.Code)
		}
	}
	adminLogin := performRequest(t, handler, "POST", "/api/auth/initialize", map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if adminLogin.Code != http.StatusCreated {
		t.Fatalf("initialize=%d: %s", adminLogin.Code, adminLogin.Body.String())
	}
	adminCookie := adminLogin.Result().Cookies()[0]
	invitation := performRequest(t, handler, "POST", "/api/admin/invitations", nil, adminCookie)
	if invitation.Code != http.StatusCreated {
		t.Fatalf("invite=%d", invitation.Code)
	}
	var invite invitationResponse
	if err := json.Unmarshal(invitation.Body.Bytes(), &invite); err != nil {
		t.Fatal(err)
	}
	vipLogin := performRequest(t, handler, "POST", "/api/auth/register", map[string]string{"username": "vip", "password": "strong-password", "token": invite.Token}, nil)
	if vipLogin.Code != http.StatusCreated {
		t.Fatalf("register=%d", vipLogin.Code)
	}
	vipCookie := vipLogin.Result().Cookies()[0]
	for _, methodAndPath := range [][2]string{{"GET", "/api/admin/backup/export"}, {"POST", "/api/admin/backup/import"}} {
		response := performRequest(t, handler, methodAndPath[0], methodAndPath[1], nil, vipCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("VIP %s status=%d", methodAndPath[1], response.Code)
		}
	}
	export := performRequest(t, handler, "GET", "/api/admin/backup/export", nil, adminCookie)
	if export.Code != http.StatusOK || export.Header().Get("Cache-Control") != "no-store" || export.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export=%d, headers=%v, body=%s", export.Code, export.Header(), export.Body.String())
	}
	if !bytes.HasPrefix(export.Body.Bytes(), []byte("PK")) {
		t.Fatal("export is not a ZIP")
	}
	upload := func(confirmation string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("backup", "backup.zip")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(part, bytes.NewReader(export.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("confirmation", confirmation); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/api/admin/backup/import", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.AddCookie(adminCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := upload("WRONG"); response.Code != http.StatusBadRequest {
		t.Fatalf("wrong confirmation=%d", response.Code)
	}
	select {
	case <-restart:
		t.Fatal("restart signaled without confirmation")
	default:
	}
	if response := upload("RESTORE"); response.Code != http.StatusAccepted || !bytes.Contains(response.Body.Bytes(), []byte("restore_pending")) {
		t.Fatalf("import=%d: %s", response.Code, response.Body.String())
	}
	select {
	case <-restart:
	default:
		t.Fatal("restore restart was not signaled")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restore", "pending.json")); err != nil {
		t.Fatal(err)
	}
}
