package monitor

import (
	"math"
	"testing"
	"time"
)

func TestHistorySummaryUsesExistingFailureSemantics(t *testing.T) {
	s, db, caps := probeTestService(t)
	task, err := s.Save(t.Context(), 0, probeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		age     time.Duration
		outcome string
		latency any
	}{{30 * time.Minute, "success", 20}, {20 * time.Minute, "success", 40}, {10 * time.Minute, "timeout", nil}, {5 * time.Minute, "permission_error", nil}} {
		if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1,?,?,?,?)`, task.ID, s.now().Add(-sample.age).UnixMilli(), sample.outcome, sample.latency); err != nil {
			t.Fatal(err)
		}
	}
	for _, hours := range []int{1, 6, 24} {
		got, err := s.HistorySummary(t.Context(), 1, hours, caps[1])
		if err != nil {
			t.Fatal(err)
		}
		tasks := got["tasks"].([]any)
		if len(tasks) != 1 {
			t.Fatalf("tasks: %v", tasks)
		}
		row := tasks[0].(map[string]any)
		if row["sample_count"] != int64(4) || row["average_latency_ms"] != float64(30) || math.Abs(row["failure_rate"].(float64)-100.0/3) > 0.001 {
			t.Fatalf("summary: %+v", row)
		}
		if row["latest"].(map[string]any)["outcome"] != "permission_error" {
			t.Fatalf("latest: %+v", row)
		}
	}
	if _, err := s.HistorySummary(t.Context(), 1, 2, caps[1]); err == nil {
		t.Fatal("invalid range accepted")
	}
	empty, err := s.HistorySummary(t.Context(), 2, 1, caps[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(empty["tasks"].([]any)) != 0 {
		t.Fatal("unexpected tasks")
	}
}
