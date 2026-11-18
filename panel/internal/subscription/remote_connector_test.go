package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	"github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"
)

type connectorRefreshFixture struct {
	remote     *RemoteService
	personal   *Service
	owner      PersonalSubscriptionActor
	other      PersonalSubscriptionActor
	connector  RemoteConnector
	credential string
	source     RemoteSource
	input      RemoteInput
	now        atomic.Int64
}

func newConnectorRefreshFixture(t *testing.T) *connectorRefreshFixture {
	t.Helper()
	db, personal := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 1, "owner", "vip")
	insertPersonalTestUser(t, db, 2, "other", "admin")
	r := &connectorRefreshFixture{
		remote: NewRemoteService(db, filepath.Join(t.TempDir(), "secret.key")), personal: personal,
		owner: PersonalSubscriptionActor{UserID: 1, Role: "vip"}, other: PersonalSubscriptionActor{UserID: 2, Role: "admin"},
		input: RemoteInput{Name: "Airport", URL: "https://provider.example/feed?token=synthetic", UserAgent: "custom-UA",
			Enabled: true, IntervalSeconds: 300},
	}
	r.now.Store(1700000000)
	r.remote.now = func() time.Time { return time.Unix(r.now.Load(), 0) }
	r.personal.remoteSecrets = r.remote.secrets
	r.remote.fetcher = &subscriptionfetch.Fetcher{} // Any attempted Panel download would fail this test.
	t.Cleanup(r.remote.stopLocalConnectors)
	connector, code, err := r.remote.CreateConnectorPairing(t.Context(), r.owner, "Test PC")
	if err != nil {
		t.Fatal(err)
	}
	r.connector = connector
	if _, err := r.remote.AuthenticateConnector(t.Context(), code); !errors.Is(err, ErrLocalConnectorAuth) {
		t.Fatalf("unredeemed code authenticated as connector: %v", err)
	}
	r.credential, err = r.remote.PairConnector(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.remote.PairConnector(t.Context(), code); !errors.Is(err, ErrLocalConnectorAuth) {
		t.Fatalf("reused pairing code: %v", err)
	}
	if _, err := r.remote.AuthenticateConnector(t.Context(), strings.Repeat("x", 43)); !errors.Is(err, ErrLocalConnectorAuth) {
		t.Fatalf("unexpected invalid credential result: %v", err)
	}
	if got, err := r.remote.ListConnectors(t.Context(), r.other); err != nil || len(got) != 0 {
		t.Fatalf("cross-owner connector list: %+v %v", got, err)
	}
	if err := r.remote.RevokeConnector(t.Context(), r.other, connector.ID); !errors.Is(err, ErrLocalConnectorNotFound) {
		t.Fatalf("cross-owner revoke: %v", err)
	}
	source, err := r.remote.Create(t.Context(), r.owner, r.input)
	if err != nil {
		t.Fatal(err)
	}
	var originalCipher string
	if err := db.QueryRow(`SELECT url_ciphertext FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&originalCipher); err != nil {
		t.Fatal(err)
	}
	r.input.RefreshMethod, r.input.ConnectorID = RemoteRefreshLocal, &connector.ID
	if _, err := r.remote.Create(t.Context(), r.other, r.input); !errors.Is(err, ErrLocalConnectorInvalid) {
		t.Fatalf("cross-owner assignment: %v", err)
	}
	r.input.URL = ""
	r.source, err = r.remote.Update(t.Context(), r.owner, source.ID, r.input)
	if err != nil {
		t.Fatal(err)
	}
	var savedCipher string
	if err := db.QueryRow(`SELECT url_ciphertext FROM remote_subscription_sources WHERE id=?`, source.ID).Scan(&savedCipher); err != nil ||
		savedCipher != originalCipher || strings.Contains(savedCipher, "synthetic") {
		t.Fatalf("original URL ciphertext was lost or exposed: %v", err)
	}
	return r
}

func (r *connectorRefreshFixture) connect(t *testing.T) *websocket.Conn {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		socket, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer socket.CloseNow()
		r.remote.ServeConnector(req.Context(), r.credential, socket)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https"),
		&websocket.DialOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { socket.CloseNow() })
	waitRemoteCondition(t, func() bool { return r.remote.checkConnectorAvailable(r.connector.ID) == nil })
	return socket
}

func waitRemoteCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for connector or refresh status")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *connectorRefreshFixture) current(t *testing.T) RemoteSource {
	t.Helper()
	s, err := r.remote.Get(t.Context(), r.owner, r.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *connectorRefreshFixture) settled(t *testing.T) RemoteSource {
	t.Helper()
	waitRemoteCondition(t, func() bool { return !r.current(t).Refreshing })
	return r.current(t)
}

func readConnectorTask(t *testing.T, socket *websocket.Conn) localconnector.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	var task localconnector.Task
	if err := wsjson.Read(ctx, socket, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func sendConnectorResult(t *testing.T, socket *websocket.Conn, result localconnector.Result) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, socket, result); err != nil {
		t.Fatal(err)
	}
}

func (r *connectorRefreshFixture) start(t *testing.T, socket *websocket.Conn) localconnector.Task {
	t.Helper()
	if err := r.remote.StartLocalRefresh(t.Context(), r.owner, r.source.ID); err != nil {
		t.Fatal(err)
	}
	return readConnectorTask(t, socket)
}

func TestLocalConnectorRefreshPreservesCacheAndPersonalBindings(t *testing.T) {
	r := newConnectorRefreshFixture(t)
	if err := r.remote.StartLocalRefresh(t.Context(), r.owner, r.source.ID); !errors.Is(err, ErrLocalConnectorOffline) {
		t.Fatalf("expected actionable offline error, got %v", err)
	}
	if err := r.remote.StartLocalRefresh(t.Context(), r.other, r.source.ID); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("cross-owner refresh: %v", err)
	}
	socket := r.connect(t)
	r.remote.refreshDue(t.Context())
	if _, err := r.remote.Refresh(t.Context(), r.owner, r.source.ID); !errors.Is(err, ErrRemoteChanged) || r.current(t).LastAttempt != nil {
		t.Fatalf("local source reached Panel refresh: %v", err)
	}
	task := r.start(t, socket)
	if task.URL != "https://provider.example/feed?token=synthetic" || task.UserAgent != "custom-UA" || !r.current(t).Refreshing {
		t.Fatal("wrong URL, User-Agent or refresh state")
	}
	if err := r.remote.StartLocalRefresh(t.Context(), r.owner, r.source.ID); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("duplicate refresh: %v", err)
	}
	if _, err := r.remote.Import(t.Context(), r.owner, r.source.ID, []byte("trojan://synthetic@one.example:443#Node")); !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("concurrent import: %v", err)
	}
	body := "trojan://synthetic@one.example:443#Node\ntrojan://synthetic@one.example:443#Node"
	sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: 200, SubscriptionInfo: "total=500;download=2",
		Body: []byte(base64.StdEncoding.EncodeToString([]byte(body)))})
	first := r.settled(t)
	if first.NodeCount != 1 || len(first.Warnings) == 0 || first.LastAttempt == nil || first.LastSuccess == nil ||
		first.LastError != "" || first.Total == nil || *first.Total != 500 {
		t.Fatalf("cache after refresh: %+v", first)
	}
	nodes, err := r.remote.Nodes(t.Context(), r.owner, r.source.ID)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes: %+v %v", nodes, err)
	}
	nodeID := nodes[0].ID
	group, err := r.personal.CreatePersonalSubscription(t.Context(), r.owner, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = r.personal.SetPersonalSubscriptionNodes(t.Context(), r.owner, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceRemoteNode, SourceID: nodeID, DisplayName: "Pinned", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := r.personal.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	if _, err := r.personal.SetPersonalSubscriptionRoutingBindings(t.Context(), r.owner, group.ID, RoutingBindings{key: {group.Nodes[0].ID}}); err != nil {
		t.Fatal(err)
	}

	r.now.Add(60)
	task = r.start(t, socket)
	sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: 200, Body: []byte("trojan://synthetic@one.example:443#Renamed")})
	success := r.settled(t)
	nodes, err = r.remote.Nodes(t.Context(), r.owner, r.source.ID)
	if err != nil || len(nodes) != 1 || nodes[0].ID != nodeID || nodes[0].Name != "Renamed" || !success.LastSuccess.After(*first.LastSuccess) {
		t.Fatalf("identity or last success changed incorrectly: %+v, %+v, %v", nodes, success, err)
	}
	data, err := r.personal.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil {
		t.Fatal(err)
	}
	_, bindings := expandedPersonalBindings(data)
	if len(data.Nodes) != 1 || data.Nodes[0].Name != "Pinned" || len(bindings[key]) != 1 {
		t.Fatalf("personal source or routing reference lost: %+v", bindings)
	}

	for _, failure := range []struct {
		status int
		body   []byte
		code   string
		want   error
	}{
		{403, nil, localconnector.HTTPError, errLocalStatus},
		{200, []byte("<html>synthetic provider secret</html>"), "", errRemoteContent},
		{200, make([]byte, subscriptionfetch.MaxBytes+1), "", errRemoteTooLarge},
	} {
		r.now.Add(60)
		task := r.start(t, socket)
		sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: failure.status, Body: failure.body, Error: failure.code})
		failed := r.settled(t)
		if failed.LastError != failure.want.Error() || failed.NodeCount != 1 || !failed.LastSuccess.Equal(*success.LastSuccess) {
			t.Fatalf("failure replaced cache or was not sanitized: %+v", failed)
		}
	}

	r.now.Add(60)
	task = r.start(t, socket)
	r.input.Name = "Edited"
	if _, err := r.remote.Update(t.Context(), r.owner, r.source.ID, r.input); err != nil {
		t.Fatal(err)
	}
	sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: 200, Body: []byte("trojan://new@different.example:443#Stale")})
	if s := r.settled(t); s.NodeCount != 1 || s.LastError != ErrRemoteChanged.Error() || !s.LastSuccess.Equal(*success.LastSuccess) {
		t.Fatalf("stale job modified the source: %+v", s)
	}
	sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: 200, Body: []byte("trojan://replay@different.example:443#Replay")})
	waitRemoteCondition(t, func() bool {
		return errors.Is(r.remote.checkConnectorAvailable(r.connector.ID), ErrLocalConnectorOffline)
	})
	if s := r.current(t); !s.LastSuccess.Equal(*success.LastSuccess) {
		t.Fatal("replay modified the cache")
	}
}

func TestLocalConnectorPreviewDisconnectExpiryAndRevocation(t *testing.T) {
	r := newConnectorRefreshFixture(t)
	socket := r.connect(t)
	preview := make(chan RemotePreview, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := r.remote.TestSaved(t.Context(), r.owner, r.source.ID, "ignored-override")
		preview <- value
		errs <- err
	}()
	task := readConnectorTask(t, socket)
	if task.UserAgent != "custom-UA" {
		t.Fatal("preview did not use stored connector User-Agent")
	}
	sendConnectorResult(t, socket, localconnector.Result{ID: task.ID, Status: 200, Body: []byte("proxies:\n - {name: Test, type: trojan, server: server.example, port: 443, password: synthetic}\n")})
	if err := <-errs; err != nil || !(<-preview).Usable || r.current(t).NodeCount != 0 || r.current(t).LastAttempt != nil {
		t.Fatalf("preview was invalid or modified the cache: %v", err)
	}

	request := remoteRefreshRequest{revision: 2, connectorID: r.connector.ID, address: task.URL, userAgent: task.UserAgent}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_, err := r.remote.connectorRoundTrip(ctx, r.owner, r.source.ID, request)
		errs <- err
	}()
	late := readConnectorTask(t, socket)
	cancel()
	// A result received after cancellation must be rejected even before socket cleanup.
	r.remote.connectorMu.Lock()
	connection := r.remote.connectors[r.connector.ID]
	r.remote.connectorMu.Unlock()
	if connection != nil && r.remote.acceptConnectorResult(r.connector.ID, connection, localconnector.Result{ID: late.ID, Status: 200}) {
		t.Fatal("accepted an expired result")
	}
	if err := <-errs; !errors.Is(err, ErrLocalConnectorTimeout) {
		t.Fatalf("timeout: %v", err)
	}

	socket = r.connect(t)
	_ = r.start(t, socket)
	_ = socket.CloseNow()
	if s := r.settled(t); s.LastError != ErrLocalConnectorOffline.Error() || s.NodeCount != 0 {
		t.Fatalf("disconnect did not fail safely: %+v", s)
	}

	r.now.Add(60)
	socket = r.connect(t)
	r.start(t, socket)
	if err := r.remote.RevokeConnector(t.Context(), r.owner, r.connector.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.remote.AuthenticateConnector(t.Context(), r.credential); !errors.Is(err, ErrLocalConnectorAuth) {
		t.Fatalf("revoked credential authenticated: %v", err)
	}
	if s := r.settled(t); s.ConnectorID != nil || s.RefreshMethod != RemoteRefreshLocal || s.LastError == "" || s.NodeCount != 0 {
		t.Fatalf("revoke broke local-only settings: %+v", s)
	}
	_, code, err := r.remote.CreateConnectorPairing(t.Context(), r.owner, "Expired PC")
	if err != nil {
		t.Fatal(err)
	}
	r.now.Add(int64(connectorPairLifetime.Seconds()))
	if _, err := r.remote.PairConnector(t.Context(), code); !errors.Is(err, ErrLocalConnectorAuth) {
		t.Fatalf("expired pairing code: %v", err)
	}
}
