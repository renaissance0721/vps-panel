package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestParseConnectorTrustedProxyIPs(t *testing.T) {
	got, err := ParseConnectorTrustedProxyIPs(" 172.30.50.2, fd00::2, ::ffff:192.0.2.2 ")
	want := []netip.Addr{netip.MustParseAddr("172.30.50.2"), netip.MustParseAddr("fd00::2"), netip.MustParseAddr("192.0.2.2")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("parsed %v, want %v, error %v", got, want, err)
	}
	if got, err := ParseConnectorTrustedProxyIPs(""); err != nil || len(got) != 0 {
		t.Fatalf("empty config: %v, %v", got, err)
	}
	for _, entry := range []string{"", "*", "172.30.0.0/16", "npm", "172.30.50.2:8080", "fe80::1%eth0", "0.0.0.0", "::", "224.0.0.1", "255.255.255.255"} {
		if got, err := ParseConnectorTrustedProxyIPs("172.30.50.2," + entry); err == nil || len(got) != 0 {
			t.Errorf("invalid list entry %q returned %v, %v", entry, got, err)
		}
	}
}

func TestConnectorHTTPSForwardedHeaders(t *testing.T) {
	trusted := []netip.Addr{netip.MustParseAddr("172.30.50.2"), netip.MustParseAddr("fd00::2")}
	tests := []struct {
		name, peer               string
		directTLS, defaultConfig bool
		proto, scheme, forwarded []string
		want                     bool
	}{
		{name: "direct TLS uses transport", directTLS: true, peer: "198.51.100.1:9000", proto: []string{"http, https"}, want: true},
		{name: "loopback IPv4", defaultConfig: true, peer: "127.0.0.1:9000", proto: []string{"https"}, want: true},
		{name: "loopback IPv6", defaultConfig: true, peer: "[::1]:9000", proto: []string{"https"}, want: true},
		{name: "trusted Docker", peer: "172.30.50.2:9000", proto: []string{"https"}, scheme: []string{"https"}, want: true},
		{name: "trusted IPv6", peer: "[fd00::2]:9000", proto: []string{"HTTPS"}, want: true},
		{name: "mapped IPv4", peer: "[::ffff:172.30.50.2]:9000", proto: []string{"https"}, want: true},
		{name: "Docker disabled by default", defaultConfig: true, peer: "172.30.50.2:9000", proto: []string{"https"}},
		{name: "unlisted Docker neighbor", peer: "172.30.50.3:9000", proto: []string{"https"}},
		{name: "internet spoofing", peer: "198.51.100.1:9000", proto: []string{"https"}},
		{name: "invalid peer", peer: "invalid-peer", proto: []string{"https"}},
		{name: "trusted proxy used HTTP", peer: "172.30.50.2:9000", proto: []string{"http"}},
		{name: "missing proto", peer: "172.30.50.2:9000", scheme: []string{"https"}},
		{name: "empty proto", peer: "172.30.50.2:9000", proto: []string{""}},
		{name: "invalid scheme token", peer: "172.30.50.2:9000", proto: []string{"https://"}},
		{name: "non-ASCII scheme token", peer: "172.30.50.2:9000", proto: []string{"http\u017f"}},
		{name: "comma separated proto", peer: "172.30.50.2:9000", proto: []string{"https, http"}},
		{name: "duplicate proto", peer: "172.30.50.2:9000", proto: []string{"https", "https"}},
		{name: "conflicting proto fields", peer: "172.30.50.2:9000", proto: []string{"https", "http"}},
		{name: "conflicting forwarded scheme", peer: "172.30.50.2:9000", proto: []string{"https"}, scheme: []string{"http"}},
		{name: "duplicate forwarded scheme", peer: "172.30.50.2:9000", proto: []string{"https"}, scheme: []string{"https", "https"}},
		{name: "conflicting Forwarded", peer: "172.30.50.2:9000", proto: []string{"https"}, forwarded: []string{"proto=http"}},
		{name: "malformed Forwarded on loopback", peer: "127.0.0.1:9000", proto: []string{"https"}, forwarded: []string{"proto="}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &server{}
			if !test.defaultConfig {
				s.connectorProxy.TrustedProxyIPs = trusted
			}
			target := "http://panel.example"
			if test.directTLS {
				target = "https://panel.example"
			}
			req := httptest.NewRequest(http.MethodPost, target+localconnector.PairPath, nil)
			req.RemoteAddr = test.peer
			req.Header.Set("X-Forwarded-For", "172.30.50.2")
			req.Header.Set("X-Real-IP", "127.0.0.1")
			for name, values := range map[string][]string{
				"X-Forwarded-Proto": test.proto, "X-Forwarded-Scheme": test.scheme, "Forwarded": test.forwarded,
			} {
				for _, value := range values {
					req.Header.Add(name, value)
				}
			}
			response := httptest.NewRecorder()
			if got := s.requireConnectorHTTPS(response, req); got != test.want {
				t.Fatalf("HTTPS allowed = %v, want %v", got, test.want)
			}
			if (!test.want && response.Code != http.StatusForbidden) || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d, headers = %v", response.Code, response.Header())
			}
		})
	}
}

func TestLocalConnectorBehindTrustedTLSProxy(t *testing.T) {
	db, _, admin, _ := setupAccountTest(t)
	defer db.Close()
	handler, err := NewHandlerWithConfig(db, t.TempDir(), "dev", HandlerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Model the Docker bridge peer using real HTTP and WebSocket traffic.
		r.RemoteAddr = "172.30.50.2:9000"
		handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	upstream, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
		req.SetURL(upstream)
		req.Out.Host = req.In.Host
		req.SetXForwarded()
		req.Out.Header.Set("X-Forwarded-Scheme", req.Out.Header.Get("X-Forwarded-Proto"))
	}}
	secure := httptest.NewTLSServer(proxy)
	defer secure.Close()
	insecure := httptest.NewServer(proxy)
	defer insecure.Close()

	// Client-supplied headers are replaced by the proxy's own TLS state.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request := func(front *httptest.Server, method, path, body, bearer string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, front.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Add("X-Forwarded-Proto", "http")
		req.Header.Set("X-Forwarded-Scheme", "http")
		req.Header.Set("Forwarded", "proto=http")
		if bearer == "" {
			req.AddCookie(admin)
			req.Header.Set("Origin", front.URL)
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	const configPath = "/api/admin/settings/reverse-proxy"
	const pairManagerPath = "/api/local-connectors"
	const pairingBody = `{"name":"Docker test"}`
	check := func(wantTrusted bool) {
		t.Helper()
		response := request(secure, http.MethodGet, configPath+"/diagnostics", "", "")
		var diagnostic connectorProxyDiagnostics
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&diagnostic) != nil ||
			diagnostic.PeerIP != "172.30.50.2" || diagnostic.BackendTLS || diagnostic.PeerLoopback ||
			diagnostic.ForwardedStatus != "https" || diagnostic.PeerTrusted != wantTrusted || diagnostic.HTTPSAccepted != wantTrusted {
			t.Fatalf("unexpected proxied diagnostics: %+v HTTP %d", diagnostic, response.StatusCode)
		}
	}
	check(false)
	if response := request(secure, http.MethodPost, pairManagerPath, pairingBody, ""); response.StatusCode != 403 {
		t.Fatalf("untrusted peer generated a pairing code: %d", response.StatusCode)
	} else {
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) != nil || failure.Code != "connector_https_required" {
			t.Fatal("pairing did not return the HTTPS error code")
		}
	}
	if response := request(secure, http.MethodPut, configPath, `{"trusted_proxy_ips":["172.30.50.2"]}`, ""); response.StatusCode != 200 {
		t.Fatalf("save trusted proxy: HTTP %d", response.StatusCode)
	}
	check(true)
	pairing := request(secure, http.MethodPost, pairManagerPath, pairingBody, "")
	var pending struct {
		Code string `json:"pairing_code"`
	}
	if pairing.StatusCode != 201 || json.NewDecoder(pairing.Body).Decode(&pending) != nil || !localconnector.ValidToken(pending.Code) {
		t.Fatalf("proxied pairing creation: HTTP %d", pairing.StatusCode)
	}
	if response := request(insecure, http.MethodPost, localconnector.PairPath, "", pending.Code); response.StatusCode != 403 {
		t.Fatalf("HTTP header spoofing: got %d", response.StatusCode)
	}
	redemption := request(secure, http.MethodPost, localconnector.PairPath, "", pending.Code)
	var paired localconnector.PairResponse
	if redemption.StatusCode != 200 || json.NewDecoder(redemption.Body).Decode(&paired) != nil || !localconnector.ValidToken(paired.Credential) {
		t.Fatalf("proxied pairing redemption: HTTP %d", redemption.StatusCode)
	}
	wss := "wss" + strings.TrimPrefix(secure.URL, "https") + localconnector.SocketPath
	ws := "ws" + strings.TrimPrefix(insecure.URL, "http") + localconnector.SocketPath
	for _, test := range []struct {
		name, endpoint, credential, origin string
		want                               int
	}{
		{"plaintext spoof", ws, paired.Credential, "", 403},
		{"pairing code", wss, pending.Code, "", 401},
		{"browser origin", wss, paired.Credential, secure.URL, 403},
		{"authenticated WSS", wss, paired.Credential, "", 101},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{"Authorization": {"Bearer " + test.credential}, "X-Forwarded-Proto": {"https"}}
			if test.origin != "" {
				headers.Set("Origin", test.origin)
			}
			socket, response, err := websocket.Dial(ctx, test.endpoint, &websocket.DialOptions{
				HTTPClient: secure.Client(), HTTPHeader: headers,
			})
			if socket != nil {
				defer socket.CloseNow()
			}
			if response == nil || response.StatusCode != test.want || (err == nil) != (test.want == 101) {
				t.Fatalf("WebSocket status or authentication mismatch: %v", err)
			}
		})
	}
	if response := request(secure, http.MethodPut, configPath, `{"trusted_proxy_ips":[]}`, ""); response.StatusCode != 200 {
		t.Fatalf("remove trusted proxy: HTTP %d", response.StatusCode)
	}
	check(false)
	socket, reply, err := websocket.Dial(ctx, wss, &websocket.DialOptions{
		HTTPClient: secure.Client(), HTTPHeader: http.Header{"Authorization": {"Bearer " + paired.Credential}},
	})
	if socket != nil {
		_ = socket.CloseNow()
	}
	if err == nil || reply == nil || reply.StatusCode != 403 {
		t.Fatalf("WSS was not blocked after removing the trusted peer: %v", err)
	}
	if response := request(secure, http.MethodPost, pairManagerPath, pairingBody, ""); response.StatusCode != 403 {
		t.Fatalf("deleted proxy still allowed pairing: %d", response.StatusCode)
	}
}

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
