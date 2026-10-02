package notification

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestTelegramErrorsNeverExposeCredentialsAndClassifyRetries(t *testing.T) {
	for _, tc := range []struct {
		code    int
		message string
		retry   bool
	}{
		{400, "Chat ID", false}, {401, "Token 无效", false}, {403, "权限", false}, {404, "Token 无效", false},
		{429, "过于频繁", true}, {500, "暂时不可用", true}, {503, "暂时不可用", true}, {302, "重定向", false},
	} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
			defer target.Close()
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("unexpected method/content type")
				}
				w.Header().Set("Location", target.URL+"/bot123:SECRET_TOKEN")
				w.WriteHeader(tc.code)
				fmt.Fprintf(w, `{"ok":false,"error_code":%d,"description":"leaked 123:SECRET_TOKEN","parameters":{"retry_after":12}}`, tc.code)
			}))
			defer host.Close()
			sender := newTelegramSender()
			sender.baseURL = host.URL
			err := sender.send(t.Context(), "123:SECRET_TOKEN", "-1001", "A_*<&>[]")
			var failure *sendError
			if !errors.As(err, &failure) || failure.retry != tc.retry || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "http") {
				t.Fatalf("unsafe/incorrect error: %v", err)
			}
			if tc.code == 429 && failure.retryAfter != 12*time.Second {
				t.Fatal(failure.retryAfter)
			}
		})
	}
}

func TestTelegramTimeoutNetworkAndMalformedResponse(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	sender := newTelegramSender()
	if sender.client.Timeout != 15*time.Second {
		t.Fatal(sender.client.Timeout)
	}
	sender.baseURL = host.URL
	sender.client.Timeout = time.Millisecond
	if err := sender.send(t.Context(), "123:SECRET_TOKEN", "1", "test"); err == nil || !strings.Contains(err.Error(), "超时") || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
	host.Close()
	if err := sender.send(t.Context(), "123:SECRET_TOKEN", "1", "test"); err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), host.URL) {
		t.Fatal(err)
	}
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "123:SECRET_TOKEN") }))
	defer invalid.Close()
	sender = newTelegramSender()
	sender.baseURL = invalid.URL
	if err := sender.send(t.Context(), "123:SECRET_TOKEN", "1", "test"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}
}

func TestWorkerRetryLimitAndUnsavedTestCredentials(t *testing.T) {
	for _, code := range []int{200, 400, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s, _, _ := fixture(t)
			var calls atomic.Int32
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/bot456:UNSAVED/sendMessage" {
					t.Error("test did not use unsaved credential")
				}
				w.WriteHeader(code)
				fmt.Fprintf(w, `{"ok":%t,"description":"456:UNSAVED"}`, code == 200)
			}))
			defer host.Close()
			s.sender.baseURL = host.URL
			result, err := s.SendTest(t.Context(), TestInput{TelegramBotToken: "456:UNSAVED", TelegramChatID: "-10099"})
			if err != nil || result.Status != "pending" || calls.Load() != 0 {
				t.Fatalf("not async: %+v %v", result, err)
			}
			if _, err = s.SendTest(t.Context(), TestInput{TelegramChatID: "1"}); !errors.Is(err, ErrTestBusy) {
				t.Fatal(err)
			}
			s.deliver(t.Context(), <-s.queue)
			result, err = s.TestResult(result.ID)
			want := int32(1)
			if code == 429 || code >= 500 {
				want = 3
			}
			if calls.Load() != want || err != nil || (code == 200 && result.Status != "success") || (code != 200 && (result.Status != "error" || strings.Contains(result.Error, "UNSAVED"))) {
				t.Fatalf("calls=%d result=%+v err=%v", calls.Load(), result, err)
			}
			stored, _ := s.Settings(t.Context())
			if stored.botToken != "123:TEST_TOKEN" || stored.TelegramChatID != "-100123456" {
				t.Fatal("test saved form unexpectedly")
			}
		})
	}
}

func TestRetryWaitIsCancelableAndRechecksConnection(t *testing.T) {
	s, now, _ := fixture(t)
	id := addServer(t, s, "reconnect during retry")
	sweep(t, s, false)
	*now = now.Add(3 * time.Minute)
	sweep(t, s, false)
	var calls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		s.agents.TrackConnection(id, &agentcontrol.Connection{})
		w.WriteHeader(500)
	}))
	defer host.Close()
	s.sender.baseURL = host.URL
	s.deliver(t.Context(), takeEvent(t, s, "offline", 1))
	sweep(t, s, false)
	noEvent(t, s)
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	// Canceled retry waits don't hold up panel shutdown.
	s.retryDelay = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstResponse := make(chan struct{})
	retrying := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		close(firstResponse)
	}))
	defer retrying.Close()
	s.sender.baseURL = retrying.URL
	done := make(chan struct{})
	go func() {
		s.deliver(ctx, event{kind: "test", testInput: TestInput{TelegramBotToken: "123:TEST", TelegramChatID: "1"}})
		close(done)
	}()
	select {
	case <-firstResponse:
	case <-time.After(time.Second):
		t.Fatal("request did not reach retry server")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop delivery")
	}
}
