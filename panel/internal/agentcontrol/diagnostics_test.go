package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
)

func TestDiagnosticPendingIgnoresUnknownResultAndFailsOnDisconnect(t *testing.T) {
	service, connection, agentSocket, cleanup := diagnosticConnectionPair(t)
	defer cleanup()
	result := make(chan error, 1)
	go func() {
		_, err := service.RequestDiagnostics(context.Background(), 7)
		result <- err
	}()
	_, payload, err := agentSocket.Read(t.Context())
	var request diagnostic.Request
	if err != nil || json.Unmarshal(payload, &request) != nil || !diagnostic.ValidRequestID(request.RequestID) {
		t.Fatalf("diagnostic request = %q, %v", payload, err)
	}
	unknownID, _ := diagnostic.NewRequestID()
	if service.ResolveDiagnostics(connection, diagnostic.Result{RequestID: unknownID}) {
		t.Fatal("unknown diagnostic request ID was resolved")
	}
	service.CloseConnections(7)
	select {
	case err := <-result:
		if !errors.Is(err, ErrAgentOffline) {
			t.Fatalf("disconnect error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not fail pending diagnostic")
	}
	connection.diagnosticsMu.Lock()
	pending := len(connection.pendingDiagnostics)
	connection.diagnosticsMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending diagnostics after disconnect = %d", pending)
	}
}

func TestDiagnosticContextCancellationAndTimeoutCleanPending(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{"cancel", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, context.Canceled},
		{"timeout", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 20*time.Millisecond)
		}, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, connection, agentSocket, cleanup := diagnosticConnectionPair(t)
			defer cleanup()
			ctx, cancel := test.context()
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := service.RequestDiagnostics(ctx, 7)
				result <- err
			}()
			if _, _, err := agentSocket.Read(t.Context()); err != nil {
				t.Fatal(err)
			}
			if test.name == "cancel" {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, test.want) {
					t.Fatalf("request error = %v, want %v", err, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("diagnostic request did not stop")
			}
			connection.diagnosticsMu.Lock()
			pending := len(connection.pendingDiagnostics)
			connection.diagnosticsMu.Unlock()
			if pending != 0 {
				t.Fatalf("pending diagnostics after %s = %d", test.name, pending)
			}
		})
	}
}

func TestCapabilitiesAreExplicitAndDiagnosticsAllowOnlyOnePendingRequest(t *testing.T) {
	capabilities := ParseCapabilities("other, diagnostics_v1")
	if !capabilities[diagnostic.CapabilityV1] || ParseCapabilities("")[diagnostic.CapabilityV1] {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	connection := &Connection{}
	firstID, _ := diagnostic.NewRequestID()
	secondID, _ := diagnostic.NewRequestID()
	if err := connection.registerDiagnostic(firstID, make(chan diagnosticResponse, 1)); err != nil {
		t.Fatal(err)
	}
	if err := connection.registerDiagnostic(secondID, make(chan diagnosticResponse, 1)); !errors.Is(err, ErrDiagnosticsInProgress) {
		t.Fatalf("second diagnostic error = %v", err)
	}
}

func diagnosticConnectionPair(t *testing.T) (*Service, *Connection, *websocket.Conn, func()) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- connection
		<-release
		connection.CloseNow()
	}))
	agentSocket, _, err := websocket.Dial(t.Context(), server.URL, nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	connection := &Connection{
		Socket:       agentSocketPeer(t, accepted),
		Capabilities: map[string]bool{diagnostic.CapabilityV1: true},
	}
	service := NewService(nil, time.Now)
	service.TrackConnection(7, connection)
	cleanup := func() {
		agentSocket.CloseNow()
		close(release)
		server.Close()
	}
	return service, connection, agentSocket, cleanup
}

func agentSocketPeer(t *testing.T, accepted <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case connection := <-accepted:
		return connection
	case <-time.After(time.Second):
		t.Fatal("WebSocket server did not accept connection")
		return nil
	}
}
