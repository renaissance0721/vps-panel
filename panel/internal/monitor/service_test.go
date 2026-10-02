package monitor

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func probeTestService(t *testing.T) (*Service, *sql.DB, map[int64]map[string]bool) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []int64{1, 2} {
		if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (?, 'server', 'online', 1, 1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }
	return s, db, map[int64]map[string]bool{1: {CapabilityTCP: true, CapabilityICMP: true}, 2: {CapabilityTCP: true}}
}

func probeInput() Task {
	port := 443
	return Task{ProbeTask: ProbeTask{Name: "test", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, ServerIDs: []int64{1}}
}

func TestProbeCRUDValidationAssignmentsAndCapabilities(t *testing.T) {
	s, _, caps := probeTestService(t)
	ctx := t.Context()
	for _, mutate := range []func(*Task){
		func(v *Task) { v.Type = "http" }, func(v *Task) { v.Name = " " },
		func(v *Task) { v.Target = "https://example.com" }, func(v *Task) { v.Target = "example.com:443" },
		func(v *Task) { v.Target = "[::1]" }, func(v *Task) { v.Target = "bad host" },
		func(v *Task) { v.Port = nil }, func(v *Task) { port := 0; v.Port = &port },
		func(v *Task) { port := 65536; v.Port = &port }, func(v *Task) { v.Type = "icmp" },
		func(v *Task) { v.IntervalSeconds = 4 }, func(v *Task) { v.IntervalSeconds = 86401 },
		func(v *Task) { v.ServerIDs = []int64{1, 1} },
	} {
		value := probeInput()
		mutate(&value)
		if _, err := s.Save(ctx, 0, value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid input %+v: %v", value, err)
		}
	}
	value := probeInput()
	value.ServerIDs = []int64{1, 2}
	value.Target = "2001:db8::1"
	created, err := s.Save(ctx, 0, value)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || len(list[0].ServerIDs) != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	created.Name = "updated"
	created.ServerIDs = []int64{1}
	updated, err := s.Save(ctx, created.ID, created)
	if err != nil || updated.ID != created.ID {
		t.Fatalf("update: %+v %v", updated, err)
	}
	for _, tc := range []struct {
		server int64
		caps   map[string]bool
		count  int
	}{{1, caps[1], 1}, {2, caps[2], 0}, {1, nil, 0}, {1, map[string]bool{CapabilityICMP: true}, 0}} {
		tasks, err := s.Desired(ctx, tc.server, tc.caps)
		if err != nil || len(tasks) != tc.count {
			t.Fatalf("desired = %+v %v", tasks, err)
		}
	}
	updated.Enabled = false
	if _, err := s.Save(ctx, updated.ID, updated); err != nil {
		t.Fatal(err)
	}
	if tasks, err := s.Desired(ctx, 1, caps[1]); err != nil || len(tasks) != 0 {
		t.Fatalf("disabled desired = %v %v", tasks, err)
	}
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := s.Save(ctx, created.ID, created); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestProbeTaskLimitAndResultValidation(t *testing.T) {
	s, _, caps := probeTestService(t)
	var task Task
	for range MaxProbeTasks {
		var err error
		task, err = s.Save(t.Context(), 0, probeInput())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(t.Context(), 0, probeInput()); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatalf("edit at limit: %v", err)
	}
	latency := 12.5
	result := ProbeResult{TaskID: task.ID, Outcome: "success", LatencyMS: &latency}
	if err := s.Ingest(t.Context(), 1, caps[1], result); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		server       int64
		capabilities map[string]bool
		result       ProbeResult
	}{
		{2, caps[2], result}, {1, nil, result}, {1, caps[1], ProbeResult{TaskID: 999, Outcome: "timeout"}},
		{1, caps[1], ProbeResult{TaskID: task.ID, Outcome: "bogus"}},
		{1, caps[1], ProbeResult{TaskID: task.ID, Outcome: "success"}},
	} {
		if err := s.Ingest(t.Context(), tc.server, tc.capabilities, tc.result); !errors.Is(err, ErrInvalidResult) {
			t.Fatalf("bad result accepted: %+v %v", tc, err)
		}
	}
	for _, number := range []float64{-1, math.NaN(), math.Inf(1)} {
		result.LatencyMS = &number
		if err := s.Ingest(t.Context(), 1, caps[1], result); !errors.Is(err, ErrInvalidResult) {
			t.Fatalf("invalid latency %v: %v", number, err)
		}
	}
	// Failure latency is ignored, never stored as zero or a negative sentinel.
	result.Outcome = "timeout"
	if err := s.Ingest(t.Context(), 1, caps[1], result); err != nil {
		t.Fatal(err)
	}
	task.Enabled = false
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(t.Context(), 1, caps[1], result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("disabled result accepted")
	}
	if err := s.Delete(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(t.Context(), 1, caps[1], result); !errors.Is(err, ErrInvalidResult) {
		t.Fatal("deleted result accepted")
	}
}

func TestProbeHistoryRangesSummaryAndCleanup(t *testing.T) {
	s, db, caps := probeTestService(t)
	ctx := context.Background()
	for _, kind := range []string{"tcp", "icmp"} {
		input := probeInput()
		input.Type = kind
		if kind == "icmp" {
			input.Port = nil
		}
		task, err := s.Save(ctx, 0, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range []struct {
			age     time.Duration
			outcome string
			latency any
		}{
			{8 * 24 * time.Hour, "success", 5}, {7 * 24 * time.Hour, "success", 5}, {25 * time.Hour, "success", 5},
			{23 * time.Hour, "success", 10}, {5 * time.Hour, "timeout", nil}, {30 * time.Minute, "dns_error", nil},
			{20 * time.Minute, "success", 40}, {10 * time.Minute, "permission_error", nil}, {5 * time.Minute, "cancelled", nil},
		} {
			if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, ?, ?, ?, ?)`, task.ID, s.now().Add(-sample.age).UnixMilli(), sample.outcome, sample.latency); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		hours, count      int
		tcpRate, icmpRate float64
	}{{1, 8, 50, 0}, {6, 10, 200.0 / 3, 50}, {24, 12, 50, 100.0 / 3}} {
		h, err := s.History(ctx, 1, tc.hours, caps[1])
		if err != nil || len(h.Samples) != tc.count || len(h.Tasks) != 2 || h.RangeHours != tc.hours {
			t.Fatalf("history %d = %+v %v", tc.hours, h, err)
		}
		if h.Tasks[0].FailureRate == nil || math.Abs(*h.Tasks[0].FailureRate-tc.tcpRate) > .001 {
			t.Fatalf("TCP failure rate = %+v", h.Tasks[0])
		}
		if h.Tasks[1].FailureRate == nil || math.Abs(*h.Tasks[1].FailureRate-tc.icmpRate) > .001 {
			t.Fatalf("ICMP loss includes permission/DNS: %+v", h.Tasks[1])
		}
		if h.Tasks[0].LatestLatencyMS != nil || h.Tasks[0].LatestOutcome != "cancelled" {
			t.Fatal("latest failure hidden by older success")
		}
	}
	for _, hours := range []int{0, -1, 7, 168} {
		if _, err := s.History(ctx, 1, hours, caps[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid range accepted: %d", hours)
		}
	}
	if h, err := s.History(ctx, 1, 6, caps[2]); err != nil || len(h.Tasks) != 1 {
		t.Fatalf("unsupported history: %+v %v", h, err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var old, boundary int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records WHERE ts < ?`, s.now().Add(-7*24*time.Hour).UnixMilli()).Scan(&old); err != nil || old != 0 {
		t.Fatalf("old records = %d %v", old, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records WHERE ts = ?`, s.now().Add(-7*24*time.Hour).UnixMilli()).Scan(&boundary); err != nil || boundary != 2 {
		t.Fatalf("boundary records = %d %v", boundary, err)
	}
	latency := 42.5
	if err := s.Ingest(ctx, 1, caps[1], ProbeResult{TaskID: 1, Outcome: "success", LatencyMS: &latency}); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(ctx, 1, 1, caps[1])
	if err != nil || h.Tasks[0].LatestLatencyMS == nil || *h.Tasks[0].LatestLatencyMS != latency {
		t.Fatalf("latest success: %+v %v", h, err)
	}
}

func TestProbeHistoryRangeBoundariesAndEmptySummary(t *testing.T) {
	s, db, caps := probeTestService(t)
	task, err := s.Save(t.Context(), 0, probeInput())
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	for _, sample := range []struct {
		at      time.Time
		latency int
	}{
		{now.Add(-24*time.Hour - time.Millisecond), 99},
		{now.Add(-24 * time.Hour), 24},
		{now.Add(-6 * time.Hour), 6},
		{now.Add(-time.Hour - time.Millisecond), 2},
		{now.Add(time.Millisecond), 100},
	} {
		if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, ?, ?, 'success', ?)`, task.ID, sample.at.UnixMilli(), sample.latency); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ hours, count int }{{1, 0}, {6, 2}, {24, 3}} {
		h, err := s.History(t.Context(), 1, tc.hours, caps[1])
		if err != nil || len(h.Samples) != tc.count || len(h.Tasks) != 1 {
			t.Fatalf("history = %+v %v", h, err)
		}
		summary := h.Tasks[0]
		if tc.count == 0 {
			if summary.LatestLatencyMS != nil || summary.FailureRate != nil || summary.LatestOutcome != "" {
				t.Fatalf("empty range leaked summary: %+v", summary)
			}
		} else if summary.LatestLatencyMS == nil || *summary.LatestLatencyMS != 2 || summary.FailureRate == nil || *summary.FailureRate != 0 {
			t.Fatalf("range summary = %+v", summary)
		}
	}
	if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, ?, ?, 'success', 1)`, task.ID, now.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(t.Context(), 1, 1, caps[1])
	if err != nil || len(h.Samples) != 1 || h.Tasks[0].LatestLatencyMS == nil || *h.Tasks[0].LatestLatencyMS != 1 {
		t.Fatalf("inclusive boundary = %+v %v", h, err)
	}
}

func TestDefaultProbeMatchingIngestionAndHistory(t *testing.T) {
	s, db, caps := probeTestService(t)
	value := probeInput()
	value.DefaultOn = true
	task, err := s.Save(t.Context(), 0, value)
	if err != nil || !task.DefaultOn || len(task.ServerIDs) != 1 || task.ServerIDs[0] != 1 {
		t.Fatalf("save = %+v %v", task, err)
	}
	list, err := s.List(t.Context())
	if err != nil || len(list) != 1 || !list[0].DefaultOn || len(list[0].ServerIDs) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	for _, tc := range []struct {
		id   int64
		caps map[string]bool
		want int
	}{
		{1, caps[1], 1}, {2, caps[2], 0}, {1, nil, 0}, {1, map[string]bool{CapabilityICMP: true}, 0}, {999, caps[1], 0},
	} {
		got, err := s.Desired(t.Context(), tc.id, tc.caps)
		if err != nil || len(got) != tc.want {
			t.Fatalf("desired = %+v %v", got, err)
		}
	}
	result := ProbeResult{TaskID: task.ID, Outcome: "timeout"}
	if err := s.Ingest(t.Context(), 2, caps[2], result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unassigned ingest = %v", err)
	}
	if err := s.Ingest(t.Context(), 1, caps[1], result); err != nil {
		t.Fatal(err)
	}
	// Even pre-existing history cannot make an unassigned task appear in History.
	if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (2, ?, ?, 'timeout', NULL)`, task.ID, s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]int{1: 1, 2: 0} {
		h, err := s.History(t.Context(), id, 6, caps[id])
		if err != nil || len(h.Tasks) != want || len(h.Samples) != want {
			t.Fatalf("history %d = %+v %v", id, h, err)
		}
	}
	if err := s.Ingest(t.Context(), 1, nil, result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unsupported ingest = %v", err)
	}
	task.ServerIDs = []int64{1, 2}
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	task.ServerIDs = []int64{2}
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Desired(t.Context(), 1, caps[1]); err != nil || len(got) != 0 {
		t.Fatalf("removed assignment = %+v %v", got, err)
	}
	if err := s.Ingest(t.Context(), 1, caps[1], result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("removed ingest = %v", err)
	}
	task.DefaultOn = false
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Desired(t.Context(), 2, caps[2]); err != nil || len(got) != 1 {
		t.Fatalf("disabled inheritance lost assignment = %+v %v", got, err)
	}
	task.Enabled = false
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(t.Context(), 2, caps[2], result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("disabled ingest = %v", err)
	}
	task.Enabled = true
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	for _, update := range []string{`UPDATE servers SET decommission_status = 'pending' WHERE id = 2`, `UPDATE servers SET decommission_status = '', archived_at = 1 WHERE id = 2`} {
		if _, err := db.Exec(update); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Desired(t.Context(), 2, caps[2]); err != nil || len(got) != 0 {
			t.Fatalf("unavailable desired = %+v %v", got, err)
		}
		if err := s.Ingest(t.Context(), 2, caps[2], result); !errors.Is(err, ErrInvalidResult) {
			t.Fatalf("unavailable ingest = %v", err)
		}
	}
}

func TestDefaultProbeCapacityIndependentOfAssignments(t *testing.T) {
	s, db, caps := probeTestService(t)
	value := probeInput()
	value.DefaultOn, value.Enabled, value.ServerIDs = true, false, nil
	var task Task
	for range MaxProbeTasks {
		var err error
		task, err = s.Save(t.Context(), 0, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(t.Context(), 0, value); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("default limit = %v", err)
	}
	// 64 unassigned defaults do not consume any capacity on existing servers.
	var manual Task
	for range MaxProbeTasks {
		var err error
		manual, err = s.Save(t.Context(), 0, probeInput())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(t.Context(), 0, probeInput()); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("assignment limit = %v", err)
	}
	task.ServerIDs = []int64{1}
	if _, err := s.Save(t.Context(), task.ID, task); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("default assignment bypassed limit = %v", err)
	}
	manual.DefaultOn = true
	if _, err := s.Save(t.Context(), manual.ID, manual); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("conversion bypassed default limit = %v", err)
	}
	task.ServerIDs = []int64{2}
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatalf("separate server capacity = %v", err)
	}
	if _, err := s.Save(t.Context(), task.ID, task); err != nil {
		t.Fatalf("edit at limit = %v", err)
	}
	if got, err := s.Desired(t.Context(), 1, caps[1]); err != nil || len(got) != MaxProbeTasks {
		t.Fatalf("rollback = %d %v", len(got), err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_tasks`).Scan(&count); err != nil || count != 2*MaxProbeTasks {
		t.Fatalf("task rollback = %d %v", count, err)
	}
}

func TestProbeAssignmentsWithoutCapabilitiesAndInvalidServers(t *testing.T) {
	s, db, caps := probeTestService(t)
	value := probeInput()
	value.Type, value.Port, value.DefaultOn, value.ServerIDs = "icmp", nil, true, []int64{2}
	task, err := s.Save(t.Context(), 0, value)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Desired(t.Context(), 2, caps[2]); err != nil || len(got) != 0 {
		t.Fatalf("unsupported desired = %+v %v", got, err)
	}
	if err := s.Ingest(t.Context(), 2, caps[2], ProbeResult{TaskID: task.ID, Outcome: "timeout"}); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unsupported ingest = %v", err)
	}
	if got, err := s.Desired(t.Context(), 2, caps[1]); err != nil || len(got) != 1 {
		t.Fatalf("upgraded desired = %+v %v", got, err)
	}
	if _, err := db.Exec(`UPDATE servers SET archived_at = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]int64{{999}, {1}, {2, 999}} {
		value.ServerIDs = ids
		if _, err := s.Save(t.Context(), task.ID, value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid assignment %v = %v", ids, err)
		}
	}
	list, err := s.List(t.Context())
	if err != nil || len(list) != 1 || len(list[0].ServerIDs) != 1 || list[0].ServerIDs[0] != 2 {
		t.Fatalf("failed edit changed assignments = %+v %v", list, err)
	}
}
