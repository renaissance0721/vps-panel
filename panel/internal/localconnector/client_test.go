package localconnector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"
)

func TestClientPairsReconnectsAndReturnsAuthorizedDownload(t *testing.T) {
	code, credential, taskID := strings.Repeat("a", 43), strings.Repeat("b", 43), strings.Repeat("c", 43)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	received := make(chan Result, 1)
	firstStarted, firstCancelled := make(chan struct{}), make(chan struct{})
	var connections atomic.Int32
	const address = "https://provider.example/feed?token=synthetic"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PairPath:
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+code {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"credential":"` + credential + `"}`))
		case SocketPath:
			if r.Header.Get("Authorization") != "Bearer "+credential {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			socket, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer socket.CloseNow()
			if connections.Add(1) == 1 {
				if err := wsjson.Write(ctx, socket, Task{ID: taskID, URL: address, UserAgent: "interrupted"}); err != nil {
					return
				}
				select {
				case <-ctx.Done():
				case <-firstStarted:
				}
				return
			}
			if err := wsjson.Write(ctx, socket, Task{ID: taskID, URL: address, UserAgent: "custom-agent"}); err != nil {
				return
			}
			var result Result
			if wsjson.Read(ctx, socket, &result) == nil {
				received <- result
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewClient()
	client.http.Transport = server.Client().Transport
	client.fetch = func(ctx context.Context, url, ua string) (subscriptionfetch.Result, error) {
		if url != address {
			t.Error("connector got the wrong URL")
		}
		if ua == "interrupted" {
			close(firstStarted)
			<-ctx.Done()
			close(firstCancelled)
			return subscriptionfetch.Result{}, ctx.Err()
		}
		if ua != "custom-agent" {
			t.Error("connector did not pass on custom User-Agent")
		}
		return subscriptionfetch.Result{Status: 200, Body: []byte("synthetic feed"), SubscriptionInfo: "upload=10; total=20; secret=hidden"}, nil
	}
	value, err := client.Pair(ctx, server.URL, code)
	if err != nil || value != credential {
		t.Fatalf("pairing failed: %v", err)
	}
	exit := make(chan error, 1)
	go func() { exit <- client.Run(ctx, server.URL, value, func(string) {}) }()
	select {
	case result := <-received:
		if result.ID != taskID || result.Status != 200 || result.Error != "" || string(result.Body) != "synthetic feed" ||
			result.SubscriptionInfo != "upload=10;total=20;" {
			t.Fatal("connector did not return the correct response and safe metadata")
		}
	case <-ctx.Done():
		t.Fatal("connector did not reconnect and return the next result")
	}
	select {
	case <-firstCancelled:
	default:
		t.Fatal("network disconnect did not cancel the interrupted fetch")
	}
	cancel()
	if err := <-exit; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestClientRejectsInsecureAddressesRedirectsAndInvalidCredentials(t *testing.T) {
	for _, address := range []string{"http://localhost", "https://name@panel.example", "https://panel.example/a", "https://panel.example?x=1"} {
		if _, err := PanelURL(address); err == nil {
			t.Errorf("unsafe Panel URL accepted: %s", address)
		}
	}
	redirected := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a credential-bearing request followed a redirect")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer redirected.Close()
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PairPath {
			http.Redirect(w, r, redirected.URL, http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer panel.Close()
	client := NewClient()
	client.http.Transport = panel.Client().Transport
	token := strings.Repeat("x", 43)
	if _, err := client.Pair(t.Context(), panel.URL, token); err == nil {
		t.Fatal("pairing accepted redirect")
	}
	if err := client.Run(t.Context(), panel.URL, token, func(string) {}); !errors.Is(err, errCredential) {
		t.Fatalf("invalid credential handling: %v", err)
	}
	result := client.download(t.Context(), Task{ID: token, URL: "http://169.254.169.254/"})
	if result.Error != Blocked || len(result.Body) != 0 {
		t.Fatal("connector accepted an unsafe subscription destination")
	}
}
