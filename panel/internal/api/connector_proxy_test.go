package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

const proxySettingsPath = "/api/admin/settings/reverse-proxy"

func TestConnectorProxySettingsPermissionsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	config := HandlerConfig{LegacyConnectorProxyIPs: "172.30.50.2"}
	handler, err := NewHandlerWithConfig(db, t.TempDir(), "test", config)
	if err != nil {
		t.Fatal(err)
	}
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize",
		map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if initialized.Code != 201 {
		t.Fatalf("initialize: %d", initialized.Code)
	}
	admin := initialized.Result().Cookies()[0]
	read := func(h http.Handler, source string, expected ...string) {
		t.Helper()
		response := performRequest(t, h, http.MethodGet, proxySettingsPath, nil, admin)
		var settings struct {
			Source string   `json:"source"`
			IPs    []string `json:"trusted_proxy_ips"`
		}
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" ||
			json.Unmarshal(response.Body.Bytes(), &settings) != nil || settings.Source != source ||
			settings.IPs == nil || !slices.Equal(settings.IPs, expected) {
			t.Fatalf("settings %d: %+v", response.Code, settings)
		}
	}
	read(handler, "environment", "172.30.50.2")
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM connector_proxy_settings`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("loading legacy defaults created a database override: %d, %v", rows, err)
	}
	clear := map[string]any{"trusted_proxy_ips": []string{}}
	for _, path := range []string{proxySettingsPath, proxySettingsPath + "/diagnostics"} {
		if response := performRequest(t, handler, http.MethodGet, path, nil, nil); response.Code != 401 {
			t.Fatalf("anonymous read of %s: %d", path, response.Code)
		}
	}
	if response := performRequest(t, handler, http.MethodPut, proxySettingsPath, clear, nil); response.Code != 401 {
		t.Fatalf("anonymous write: %d", response.Code)
	}
	for _, role := range []string{"vip", "carpool", "subscriber"} {
		cookie, _ := registerAccount(t, db, handler, admin, role, "test-"+role)
		for _, path := range []string{proxySettingsPath, proxySettingsPath + "/diagnostics"} {
			if response := performRequest(t, handler, http.MethodGet, path, nil, cookie); response.Code != 403 {
				t.Fatalf("%s read of %s: %d", role, path, response.Code)
			}
		}
		if response := performRequest(t, handler, http.MethodPut, proxySettingsPath, clear, cookie); response.Code != 403 {
			t.Fatalf("%s write: %d", role, response.Code)
		}
	}
	crossOrigin := jsonRequest(t, http.MethodPut, proxySettingsPath, clear)
	crossOrigin.AddCookie(admin)
	crossOrigin.Header.Set("Origin", "https://untrusted.example")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, crossOrigin)
	if denied.Code != 403 {
		t.Fatalf("cross-origin admin write: %d", denied.Code)
	}
	tooMany := make([]string, maxConnectorProxyIPs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("192.0.2.%d", i+1)
	}
	for _, body := range []map[string]any{
		{}, {"trusted_proxy_ips": nil}, {"trusted_proxy_ips": []string{""}},
		{"trusted_proxy_ips": []string{"not-an-ip"}}, {"trusted_proxy_ips": []string{"10.0.0.0/8"}},
		{"trusted_proxy_ips": []string{"*"}}, {"trusted_proxy_ips": []string{"192.0.2.1:80"}},
		{"trusted_proxy_ips": []string{"0.0.0.0"}}, {"trusted_proxy_ips": []string{"ff02::1"}},
		{"trusted_proxy_ips": tooMany},
	} {
		if response := performRequest(t, handler, http.MethodPut, proxySettingsPath, body, admin); response.Code != 400 {
			t.Fatalf("invalid list accepted: %d", response.Code)
		}
	}
	read(handler, "environment", "172.30.50.2")
	if response := performRequest(t, handler, http.MethodPut, proxySettingsPath,
		map[string][]string{"trusted_proxy_ips": {"192.0.2.23", "::ffff:192.0.2.23", "2001:DB8:0::23", "2001:db8::23"}}, admin); response.Code != 200 {
		t.Fatalf("valid normalized list: %d %s", response.Code, response.Body.String())
	}
	read(handler, "database", "192.0.2.23", "2001:db8::23")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = NewHandlerWithConfig(db, t.TempDir(), "restart", config)
	if err != nil {
		t.Fatal(err)
	}
	read(handler, "database", "192.0.2.23", "2001:db8::23")
	if response := performRequest(t, handler, http.MethodPut, proxySettingsPath, clear, admin); response.Code != 200 {
		t.Fatalf("clear list: %d", response.Code)
	}
	read(handler, "database")
	for _, legacy := range []string{"172.30.50.2", "stale-invalid-value"} {
		h, err := NewHandlerWithConfig(db, t.TempDir(), "restart", HandlerConfig{LegacyConnectorProxyIPs: legacy})
		if err != nil {
			t.Fatal(err)
		}
		read(h, "database")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='connector_proxy.update' AND summary NOT LIKE '%192.0.2.23%'`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("expected two audit entries without addresses: %d, %v", rows, err)
	}
}

func TestConcurrentConnectorProxyUpdatesPublishCompletePersistedLists(t *testing.T) {
	db, handler, admin, _ := setupAccountTest(t)
	defer db.Close()
	a := []string{"192.0.2.10", "2001:db8::10"}
	b := []string{"192.0.2.20", "2001:db8::20"}
	write := func(ips []string) {
		response := performRequest(t, handler, http.MethodPut, proxySettingsPath, map[string][]string{"trusted_proxy_ips": ips}, admin)
		if response.Code != 200 {
			t.Errorf("settings update: %d", response.Code)
		}
	}
	read := func() []netip.Addr {
		response := performRequest(t, handler, http.MethodGet, proxySettingsPath, nil, admin)
		var config connectorProxyConfig
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &config) != nil || config.Source != "database" {
			t.Errorf("settings read: %d", response.Code)
		}
		return config.TrustedProxyIPs
	}
	write(a)
	var workers sync.WaitGroup
	for _, ips := range [][]string{a, b} {
		workers.Go(func() {
			for range 8 {
				write(ips)
			}
		})
	}
	workers.Go(func() {
		for range 16 {
			ips := read()
			if len(ips) != 2 || !(ips[0].String() == a[0] && ips[1].String() == a[1] ||
				ips[0].String() == b[0] && ips[1].String() == b[1]) {
				t.Errorf("incomplete runtime snapshot: %v", ips)
			}
			response := performRequest(t, handler, http.MethodGet, proxySettingsPath+"/diagnostics", nil, admin)
			if response.Code != 200 {
				t.Errorf("diagnostics during update: %d", response.Code)
			}
		}
	})
	workers.Wait()
	persisted, err := loadConnectorProxyConfig(db, "")
	if err != nil || !slices.Equal(read(), persisted.TrustedProxyIPs) {
		t.Fatalf("settings runtime and storage diverged: %v, %v", persisted, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_proxy_update BEFORE UPDATE ON connector_proxy_settings BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, handler, http.MethodPut, proxySettingsPath, map[string][]string{"trusted_proxy_ips": {}}, admin)
	if response.Code != 500 || !slices.Equal(read(), persisted.TrustedProxyIPs) {
		t.Fatal("failed database write changed the active list")
	}
}
