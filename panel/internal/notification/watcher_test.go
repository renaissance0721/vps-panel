package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func fixture(t *testing.T) (*Service, *time.Time, <-chan string) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	s := NewService(db, serverstore.NewService(db), agentcontrol.NewService(db, func() time.Time { return now }))
	s.now = func() time.Time { return now }
	s.retryDelay = time.Millisecond
	settings, err := s.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.TelegramChatID = "-100123456"
	if err := s.Save(t.Context(), Update{Settings: settings, TelegramBotToken: "123:TEST_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 100)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 2 || body["parse_mode"] != "" {
			t.Errorf("unexpected request fields: %v", body)
		}
		messages <- body["text"]
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(httpServer.Close)
	s.sender.baseURL = httpServer.URL
	return s, &now, messages
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func addServer(t *testing.T, s *Service, name string) int64 {
	t.Helper()
	created, err := s.servers.Create(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := s.agents.RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.agents.SetAgentConnected(t.Context(), registered.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func sweep(t *testing.T, s *Service, traffic bool) {
	t.Helper()
	var err error
	if traffic {
		err = s.sweepTraffic(t.Context())
	} else {
		err = s.sweepOnline(t.Context())
	}
	if err != nil {
		t.Fatal(err)
	}
}

func takeEvent(t *testing.T, s *Service, kind string, count int) event {
	t.Helper()
	select {
	case value := <-s.queue:
		if value.kind != kind || len(value.entries) != count {
			t.Fatalf("event=%s entries=%d, want %s/%d", value.kind, len(value.entries), kind, count)
		}
		return value
	default:
		t.Fatal("missing event")
		return event{}
	}
}

func noEvent(t *testing.T, s *Service) {
	t.Helper()
	if len(s.queue) != 0 {
		t.Fatalf("unexpected %d queued events", len(s.queue))
	}
}

func TestOfflineGraceReplacementDeliveryAndRecovery(t *testing.T) {
	s, now, messages := fixture(t)
	id := addServer(t, s, "Tokyo_*<&>[]")
	// Database says online, but only the current connection map is authoritative.
	execSQL(t, s.db, `UPDATE agents SET last_seen_at = ? WHERE server_id = ?`, now.Add(-10*time.Minute).Unix(), id)
	sweep(t, s, false)
	*now = now.Add(179 * time.Second)
	sweep(t, s, false)
	noEvent(t, s)
	old, current := &agentcontrol.Connection{}, &agentcontrol.Connection{}
	s.agents.TrackConnection(id, old)
	s.agents.TrackConnection(id, current)
	if s.agents.UntrackConnection(id, old) || !s.agents.IsConnected(id) {
		t.Fatal("old disconnect removed replacement")
	}
	sweep(t, s, false)
	noEvent(t, s)
	s.agents.UntrackConnection(id, current)
	sweep(t, s, false)
	*now = now.Add(179 * time.Second)
	sweep(t, s, false)
	noEvent(t, s)
	*now = now.Add(time.Second)
	sweep(t, s, false)
	item := takeEvent(t, s, "offline", 1)
	sweep(t, s, false)
	noEvent(t, s)
	s.deliver(t.Context(), item)
	text := <-messages
	if !strings.Contains(text, "Tokyo_*<&>[] 离线") || !strings.Contains(text, "UTC+8") {
		t.Fatal(text)
	}
	s.agents.TrackConnection(id, current)
	sweep(t, s, false)
	s.deliver(t.Context(), takeEvent(t, s, "recovery", 1))
	if text := <-messages; !strings.Contains(text, "恢复在线") || !strings.Contains(text, "3 分 0 秒") {
		t.Fatal(text)
	}
	sweep(t, s, false)
	noEvent(t, s)
}

func TestRestartGetsFullGraceAndPreservesConsumedOutages(t *testing.T) {
	s, now, _ := fixture(t)
	id := addServer(t, s, "restart")
	sweep(t, s, false)
	*now = now.Add(10 * time.Minute)
	restarted := NewService(s.db, s.servers, s.agents)
	restarted.now = s.now
	sweep(t, restarted, false)
	noEvent(t, restarted)
	*now = now.Add(179 * time.Second)
	sweep(t, restarted, false)
	noEvent(t, restarted)
	*now = now.Add(time.Second)
	sweep(t, restarted, false)
	takeEvent(t, restarted, "offline", 1)
	// A crash after enqueue must not continuously recreate a failed event.
	again := NewService(s.db, s.servers, s.agents)
	again.now = s.now
	sweep(t, again, false)
	noEvent(t, again)
	again.agents.TrackConnection(id, &agentcontrol.Connection{})
	sweep(t, again, false)
	noEvent(t, again) // never delivered => no recovery
}

func TestBatchExclusionsQueueOverflowAndFailedDelivery(t *testing.T) {
	s, now, _ := fixture(t)
	for i := 0; i < 25; i++ {
		addServer(t, s, fmt.Sprintf("server-%02d", i))
	}
	archived := addServer(t, s, "archived")
	execSQL(t, s.db, `UPDATE servers SET archived_at=1 WHERE id=?`, archived)
	deleting := addServer(t, s, "deleting")
	execSQL(t, s.db, `UPDATE servers SET decommissioning_at=1, decommission_status='pending' WHERE id=?`, deleting)
	never := addServer(t, s, "never-connected")
	execSQL(t, s.db, `UPDATE agents SET last_seen_at=NULL WHERE server_id=?`, never)
	pending := addServer(t, s, "pending")
	execSQL(t, s.db, `UPDATE servers SET status='pending' WHERE id=?`, pending)
	removed := addServer(t, s, "deleted")
	execSQL(t, s.db, `DELETE FROM servers WHERE id=?`, removed)
	sweep(t, s, false)
	*now = now.Add(3 * time.Minute)
	sweep(t, s, false)
	batch := takeEvent(t, s, "offline", 25)
	text := message("offline", batch.entries, *now)
	if !strings.Contains(text, "……另外 5 台") || strings.Contains(text, "never-connected") {
		t.Fatal(text)
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"description":"123:TEST_TOKEN"}`)
	}))
	defer failed.Close()
	s.sender.baseURL = failed.URL
	s.deliver(t.Context(), batch)
	for _, e := range batch.entries {
		s.agents.TrackConnection(e.server.ID, &agentcontrol.Connection{})
	}
	sweep(t, s, false)
	noEvent(t, s) // no false recoveries after failure
	// A separate outage consumes its marker even when the bounded queue is full.
	extra := addServer(t, s, "overflow")
	sweep(t, s, false)
	*now = now.Add(3 * time.Minute)
	for len(s.queue) < cap(s.queue) {
		s.queue <- event{kind: "test"}
	}
	sweep(t, s, false)
	for len(s.queue) > 0 {
		<-s.queue
	}
	sweep(t, s, false)
	noEvent(t, s)
	state, err := s.state(t.Context(), extra)
	if err != nil || !state.started.Valid || state.notified.Valid {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func setTraffic(t *testing.T, s *Service, id, rx, tx, adjust, cycle int64, mode string) {
	t.Helper()
	execSQL(t, s.db, `UPDATE servers SET monthly_traffic_limit_bytes=1000,traffic_count_mode=? WHERE id=?`, mode, id)
	execSQL(t, s.db, `INSERT INTO server_metrics (server_id,cpu_percent,memory_used_bytes,memory_total_bytes,disk_used_bytes,disk_total_bytes,uptime_seconds,cycle_rx_bytes,cycle_tx_bytes,traffic_adjustment_bytes,cycle_started_at,updated_at)
	VALUES (?,0,0,0,0,0,0,?,?,?,?,1) ON CONFLICT(server_id) DO UPDATE SET cycle_rx_bytes=excluded.cycle_rx_bytes,cycle_tx_bytes=excluded.cycle_tx_bytes,traffic_adjustment_bytes=excluded.traffic_adjustment_bytes,cycle_started_at=excluded.cycle_started_at`, id, rx, tx, adjust, cycle)
}

func TestTrafficStepsCyclesCorrectionsAndOfficialCounting(t *testing.T) {
	s, _, _ := fixture(t)
	id := addServer(t, s, "traffic")
	for _, tc := range []struct {
		tx   int64
		step int
	}{{790, 0}, {800, 80}, {820, 0}, {900, 90}, {990, 0}, {1000, 100}, {1200, 0}, {850, 0}, {600, 0}, {800, 80}, {780, 0}, {940, 90}} {
		setTraffic(t, s, id, 9999, tc.tx, 0, 1, "single")
		sweep(t, s, true)
		if tc.step == 0 {
			noEvent(t, s)
		} else if item := takeEvent(t, s, "traffic", 1); item.entries[0].step != tc.step {
			t.Fatalf("tx=%d step=%d", tc.tx, item.entries[0].step)
		}
	}
	setTraffic(t, s, id, 500, 300, 0, 2, "bidirectional")
	sweep(t, s, true)
	takeEvent(t, s, "traffic", 1)
	setTraffic(t, s, id, 500, 300, -200, 2, "bidirectional")
	sweep(t, s, true)
	noEvent(t, s)
	setTraffic(t, s, id, 500, 300, 100, 2, "bidirectional")
	sweep(t, s, true)
	if item := takeEvent(t, s, "traffic", 1); item.entries[0].step != 90 {
		t.Fatal(item.entries[0].step)
	}
	setTraffic(t, s, id, 500, 300, 100, 3, "bidirectional")
	sweep(t, s, true)
	takeEvent(t, s, "traffic", 1)
	execSQL(t, s.db, `UPDATE servers SET monthly_traffic_limit_bytes=NULL WHERE id=?`, id)
	sweep(t, s, true)
	noEvent(t, s)
}

func TestTrafficBoundariesBatchAndMessageLimit(t *testing.T) {
	settings := Settings{TrafficThresholdPercent: 80, TrafficStepPercent: 10, TrafficFullEnabled: true}
	for _, tc := range []struct {
		used, limit int64
		want        int
	}{{799, 1000, 0}, {800, 1000, 80}, {939, 1000, 90}, {1001, 1000, 100}, {0, 0, 0}, {math.MaxInt64 - 1, math.MaxInt64, 90}, {math.MaxInt64, math.MaxInt64, 100}, {80, 101, 0}, {81, 101, 80}} {
		if got := trafficStep(tc.used, tc.limit, settings); got != tc.want {
			t.Fatalf("%+v got=%d", tc, got)
		}
	}
	settings.TrafficFullEnabled = false
	if trafficStep(1200, 1000, settings) != 90 {
		t.Fatal("disabled full threshold")
	}
	settings.TrafficThresholdPercent = 100
	if trafficStep(1200, 1000, settings) != 0 {
		t.Fatal("100 threshold with full disabled")
	}
	settings.TrafficFullEnabled = true
	if trafficStep(999, 1000, settings) != 0 || trafficStep(1000, 1000, settings) != 100 {
		t.Fatal("100 threshold")
	}
	s, now, _ := fixture(t)
	for i := 0; i < 25; i++ {
		id := addServer(t, s, strings.Repeat("🚀", 48)+fmt.Sprint(i))
		setTraffic(t, s, id, 0, int64(800+i*10), 0, 1, "single")
	}
	sweep(t, s, true)
	batch := takeEvent(t, s, "traffic", 25)
	sweep(t, s, true)
	noEvent(t, s)
	text := message("traffic", batch.entries, *now)
	if len(utf16.Encode([]rune(text))) > 4096 || !strings.Contains(text, "另外") || !strings.Contains(text, "流量已用尽") {
		t.Fatal(text)
	}
}

func TestLateReconnectSuppressesUnsentOffline(t *testing.T) {
	s, now, messages := fixture(t)
	id := addServer(t, s, "late reconnect")
	sweep(t, s, false)
	*now = now.Add(3 * time.Minute)
	sweep(t, s, false)
	item := takeEvent(t, s, "offline", 1)
	s.agents.TrackConnection(id, &agentcontrol.Connection{})
	sweep(t, s, false)
	noEvent(t, s) // wait for pending delivery's actual outcome
	s.deliver(t.Context(), item)
	sweep(t, s, false)
	noEvent(t, s)
	if len(messages) != 0 {
		t.Fatal("sent stale offline notification")
	}
}

func TestRunStopsAndCancelsSending(t *testing.T) {
	s, _, _ := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer hanging.Close()
	defer close(release)
	s.sender.baseURL = hanging.URL
	_, err := s.SendTest(t.Context(), TestInput{TelegramChatID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestReconnectWhileSendingWaitsForConfirmedDelivery(t *testing.T) {
	s, now, _ := fixture(t)
	id := addServer(t, s, "in-flight reconnect")
	sweep(t, s, false)
	*now = now.Add(3 * time.Minute)
	sweep(t, s, false)
	item := takeEvent(t, s, "offline", 1)
	started, release := make(chan struct{}), make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer host.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s.sender.baseURL = host.URL
	delivered := make(chan struct{})
	go func() { s.deliver(t.Context(), item); close(delivered) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not start")
	}
	s.agents.TrackConnection(id, &agentcontrol.Connection{})
	checked := make(chan error, 1)
	go func() { checked <- s.sweepOnline(t.Context()) }()
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP delivery blocked the watcher")
	}
	noEvent(t, s)
	releaseOnce.Do(func() { close(release) })
	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not finish")
	}
	// A new Panel service must retain the delivery receipt and pair exactly once.
	restarted := NewService(s.db, s.servers, s.agents)
	restarted.now = s.now
	sweep(t, restarted, false)
	takeEvent(t, restarted, "recovery", 1)
	sweep(t, restarted, false)
	noEvent(t, restarted)
}
