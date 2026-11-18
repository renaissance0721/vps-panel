package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestLocalConnectorHTTPSAuthenticationAndRefreshAPI(t *testing.T) {
	db, _, admin, _ := setupAccountTest(t)
	defer db.Close()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{DataDir: t.TempDir()})
	vip, _ := registerAccount(t, db, handler, admin, "vip", "connector-vip")
	carpool, _ := registerAccount(t, db, handler, admin, "carpool", "connector-carpool")
	server := httptest.NewTLSServer(handler)
	defer server.Close()

	const connectors = "/api/local-connectors"
	if response := performRequest(t, handler, http.MethodGet, connectors, nil, carpool); response.Code != 403 {
		t.Fatalf("non-manager connector list: %d", response.Code)
	}
	name := map[string]string{"name": "Test Windows"}
	if response := performRequest(t, handler, http.MethodPost, connectors, name, vip); response.Code != 403 {
		t.Fatalf("plaintext pairing creation: %d", response.Code)
	}
	pairing := performRequest(t, handler, http.MethodPost, server.URL+connectors, name, vip)
	var pending struct {
		Connector subscriptionstore.RemoteConnector `json:"connector"`
		Code      string                            `json:"pairing_code"`
	}
	if pairing.Code != 201 || pairing.Header().Get("Cache-Control") != "no-store" ||
		json.Unmarshal(pairing.Body.Bytes(), &pending) != nil || !localconnector.ValidToken(pending.Code) {
		t.Fatalf("invalid pairing response, status %d", pairing.Code)
	}
	ownerList := performRequest(t, handler, http.MethodGet, connectors, nil, vip)
	otherList := performRequest(t, handler, http.MethodGet, connectors, nil, admin)
	if strings.Contains(ownerList.Body.String(), pending.Code) || !strings.Contains(ownerList.Body.String(), "Test Windows") ||
		strings.Contains(otherList.Body.String(), "Test Windows") {
		t.Fatal("connector listing disclosed a code or crossed ownership")
	}
	spoofed := httptest.NewRequest(http.MethodPost, localconnector.PairPath, nil)
	spoofed.Header.Set("X-Forwarded-Proto", "https")
	spoofed.Header.Set("Authorization", "Bearer "+pending.Code)
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, spoofed)
	if rejected.Code != 403 {
		t.Fatalf("untrusted forwarded scheme accepted: %d", rejected.Code)
	}
	pairReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+localconnector.PairPath, nil)
	pairReq.Header.Set("Authorization", "Bearer "+pending.Code)
	response, err := server.Client().Do(pairReq)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var paired localconnector.PairResponse
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&paired) != nil || !localconnector.ValidToken(paired.Credential) {
		t.Fatalf("redemption failed: %d", response.StatusCode)
	}
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM remote_subscription_connectors WHERE id=?`, pending.Connector.ID).Scan(&storedHash); err != nil ||
		storedHash != token.Hash(paired.Credential) {
		t.Fatal("the new credential was not stored as a hash")
	}
	for _, path := range []string{connectors, "/api/remote-subscriptions"} {
		request := httptest.NewRequest(http.MethodGet, server.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+paired.Credential)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != 401 {
			t.Fatalf("connector credential accessed manager path %s: %d", path, result.Code)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	dial := func(credential string) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+localconnector.SocketPath,
			&websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: http.Header{"Authorization": {"Bearer " + credential}}})
	}
	if _, result, err := dial(pending.Code); err == nil || result == nil || result.StatusCode != 401 {
		t.Fatal("one-time code authenticated on the task channel")
	}
	socket, _, err := dial(paired.Credential)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	for {
		listed := performRequest(t, handler, http.MethodGet, connectors, nil, vip)
		if strings.Contains(listed.Body.String(), `"status":"online"`) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("connector never registered online")
		}
		time.Sleep(5 * time.Millisecond)
	}
	input := subscriptionstore.RemoteInput{Name: "Test provider", URL: "https://provider.example/fixture?key=synthetic",
		UserAgent: "Custom-UA", Enabled: true, IntervalSeconds: 300,
		RefreshMethod: subscriptionstore.RemoteRefreshLocal, ConnectorID: &pending.Connector.ID}
	created := performRequest(t, handler, http.MethodPost, "/api/remote-subscriptions", input, vip)
	var source struct {
		Source subscriptionstore.RemoteSource `json:"source"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &source) != nil || strings.Contains(created.Body.String(), input.URL) {
		t.Fatalf("create local source: %d", created.Code)
	}
	refreshPath := fmt.Sprintf("/api/remote-subscriptions/%d/refresh", source.Source.ID)
	if result := performRequest(t, handler, http.MethodPost, refreshPath, nil, admin); result.Code != 404 {
		t.Fatalf("cross-owner refresh: %d", result.Code)
	}
	if result := performRequest(t, handler, http.MethodPost, refreshPath, nil, vip); result.Code != 202 {
		t.Fatalf("local refresh did not return early: %d %s", result.Code, result.Body.String())
	}
	var task localconnector.Task
	if err := wsjson.Read(ctx, socket, &task); err != nil || task.URL != input.URL || task.UserAgent != input.UserAgent {
		t.Fatalf("wrong connector task: %v", err)
	}
	if err := wsjson.Write(ctx, socket, localconnector.Result{ID: task.ID, Status: 200, Body: []byte("trojan://fake@test.example:443#One")}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/remote-subscriptions/%d", source.Source.ID)
	for {
		result := performRequest(t, handler, http.MethodGet, path, nil, vip)
		if result.Code == 200 && json.Unmarshal(result.Body.Bytes(), &source) == nil && !source.Source.Refreshing {
			if source.Source.NodeCount != 1 || source.Source.LastError != "" {
				t.Fatal("connector result did not commit through the source cache")
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("refresh never settled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	revokePath := fmt.Sprintf("%s/%d", connectors, pending.Connector.ID)
	if result := performRequest(t, handler, http.MethodDelete, revokePath, nil, admin); result.Code != 404 {
		t.Fatalf("cross-owner revoke: %d", result.Code)
	}
	if result := performRequest(t, handler, http.MethodDelete, revokePath, nil, vip); result.Code != 204 {
		t.Fatalf("revoke: %d", result.Code)
	}
	if _, result, err := dial(paired.Credential); err == nil || result == nil || result.StatusCode != 401 {
		t.Fatal("revoked credential authenticated on the task channel")
	}
}
