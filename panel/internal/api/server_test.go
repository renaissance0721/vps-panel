package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestHealth(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	NewHandler(db, t.TempDir()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"database":"ok"`) {
		t.Fatalf("body = %q, want healthy database", response.Body.String())
	}
}

func TestSPAFallbackAndUnknownAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("panel shell"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	handler := NewHandler(db, webRoot)

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	pageBody, _ := io.ReadAll(pageResponse.Result().Body)
	if pageResponse.Code != http.StatusOK || string(pageBody) != "panel shell" {
		t.Fatalf("SPA response = (%d, %q)", pageResponse.Code, pageBody)
	}

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
}
