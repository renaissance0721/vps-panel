package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestLoginLimiterPairIPWindowAndMemoryBounds(t *testing.T) {
	now := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)
	limiter := newLoginLimiter()
	for index := 0; index < loginPairFailureLimit; index++ {
		username := "Admin"
		if index%2 == 1 {
			username = " admin "
		}
		if allowed, _ := limiter.Allow("198.51.100.10", username, now); !allowed {
			t.Fatalf("pair blocked before failure %d", index+1)
		}
		limiter.RecordFailure("198.51.100.10", username, now)
	}
	if allowed, retryAfter := limiter.Allow("198.51.100.10", "ADMIN", now); allowed || retryAfter != loginFailureWindow {
		t.Fatalf("normalized pair limit = (%t, %v)", allowed, retryAfter)
	}
	limiter.Reset("198.51.100.10", "admin")
	if allowed, _ := limiter.Allow("198.51.100.10", "ADMIN", now); !allowed {
		t.Fatal("successful login reset did not clear pair failures")
	}

	global := newLoginLimiter()
	for index := 0; index < loginIPFailureLimit; index++ {
		global.RecordFailure("198.51.100.20", fmt.Sprintf("random-%d", index), now)
	}
	if allowed, _ := global.Allow("198.51.100.20", "another-user", now); allowed {
		t.Fatal("IP-wide failure limit was bypassed with another username")
	}
	if allowed, _ := global.Allow("198.51.100.21", "another-user", now); !allowed {
		t.Fatal("failure limit leaked across client IPs")
	}
	if allowed, _ := global.Allow("198.51.100.20", "another-user", now.Add(loginFailureWindow)); !allowed {
		t.Fatal("failure window did not expire")
	}

	bounded := newLoginLimiter()
	for index := 0; index < maxLoginPairEntries+100; index++ {
		bounded.RecordFailure("198.51.100.30", fmt.Sprintf("random-%d", index), now)
	}
	for index := 0; index < maxLoginIPEntries+100; index++ {
		bounded.RecordFailure(fmt.Sprintf("client-%d", index), "user", now)
	}
	if len(bounded.pairFailures) > maxLoginPairEntries || len(bounded.ipFailures) > maxLoginIPEntries {
		t.Fatalf("limiter maps exceeded bounds: pair=%d ip=%d", len(bounded.pairFailures), len(bounded.ipFailures))
	}
	if allowed, _ := bounded.Allow("new-client", "new-user", now); allowed {
		t.Fatal("limiter accepted an untracked key while its maps were full")
	}
}

func TestLoginAPIEnforcesPairAndIPFailureLimits(t *testing.T) {
	t.Run("normalized pair and reset", func(t *testing.T) {
		db, err := database.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handler := NewHandler(db, t.TempDir())
		initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
			"username": "admin", "password": "strong-password",
		}, nil)
		if initialized.Code != http.StatusCreated {
			t.Fatalf("initialize = %d, %s", initialized.Code, initialized.Body.String())
		}
		for index := 0; index < loginPairFailureLimit-1; index++ {
			response := performLoginRequest(t, handler, "Admin", "wrong-password", "198.51.100.10:1234")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("initial failure %d = %d", index+1, response.Code)
			}
		}
		if response := performLoginRequest(t, handler, "ADMIN", "strong-password", "198.51.100.10:1234"); response.Code != http.StatusOK {
			t.Fatalf("successful login = %d, %s", response.Code, response.Body.String())
		}
		for index := 0; index < loginPairFailureLimit; index++ {
			response := performLoginRequest(t, handler, " admin ", "wrong-password", "198.51.100.10:1234")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("failure after reset %d = %d", index+1, response.Code)
			}
		}
		limited := performLoginRequest(t, handler, "ADMIN", "wrong-password", "198.51.100.10:1234")
		if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
			t.Fatalf("pair limit = %d, Retry-After %q, body %s", limited.Code, limited.Header().Get("Retry-After"), limited.Body.String())
		}
	})

	t.Run("IP-wide limit", func(t *testing.T) {
		db, err := database.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handler := NewHandler(db, t.TempDir())
		for index := 0; index < loginIPFailureLimit; index++ {
			response := performLoginRequest(t, handler, fmt.Sprintf("missing-%d", index), "wrong-password", "198.51.100.20:4321")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("IP failure %d = %d", index+1, response.Code)
			}
		}
		if response := performLoginRequest(t, handler, "new-name", "wrong-password", "198.51.100.20:4321"); response.Code != http.StatusTooManyRequests {
			t.Fatalf("IP-wide limit = %d, %s", response.Code, response.Body.String())
		}
		if response := performLoginRequest(t, handler, "new-name", "wrong-password", "198.51.100.21:4321"); response.Code != http.StatusUnauthorized {
			t.Fatalf("independent IP = %d, %s", response.Code, response.Body.String())
		}
	})

	t.Run("non-credential errors are not counted", func(t *testing.T) {
		db, err := database.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		handler := NewHandler(db, t.TempDir())
		for index := 0; index < loginIPFailureLimit+1; index++ {
			request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{"))
			request.RemoteAddr = "198.51.100.30:1234"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("malformed request %d = %d", index+1, response.Code)
			}
		}
		if response := performLoginRequest(t, handler, "missing", "wrong-password", "198.51.100.30:1234"); response.Code != http.StatusUnauthorized {
			t.Fatalf("malformed JSON counted as login failures: %d", response.Code)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < loginIPFailureLimit+1; index++ {
			response := performLoginRequest(t, handler, "missing", "wrong-password", "198.51.100.31:1234")
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("database error %d = %d", index+1, response.Code)
			}
		}
	})
}

func TestClientIPTrustsForwardingHeadersOnlyFromLoopback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xRealIP    string
		want       string
	}{
		{"public peer ignores forwarding", "198.51.100.40:8080", "1.2.3.4", "5.6.7.8", "198.51.100.40"},
		{"IPv4 loopback trusts first valid XFF", "127.0.0.1:8080", "invalid, 203.0.113.10, 203.0.113.11", "", "203.0.113.10"},
		{"IPv4 loopback falls back to real IP", "127.0.0.1:8080", "invalid", "203.0.113.12", "203.0.113.12"},
		{"IPv6 loopback trusts XFF", "[::1]:8080", "203.0.113.13", "", "203.0.113.13"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-For", test.xff)
			request.Header.Set("X-Real-IP", test.xRealIP)
			if got := clientIP(request); got != test.want {
				t.Fatalf("client IP = %q, want %q", got, test.want)
			}
		})
	}
}

func performLoginRequest(t *testing.T, handler http.Handler, username, password, remoteAddress string) *httptest.ResponseRecorder {
	t.Helper()
	request := jsonRequest(t, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username, "password": password,
	})
	request.RemoteAddr = remoteAddress
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
