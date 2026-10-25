package monitor

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func TestServerProbeAssignmentsIsolationCapabilitiesAndHistory(t *testing.T) {
	s, db, caps := probeTestService(t)
	a := probeInput()
	a.DefaultOn, a.ServerIDs = true, []int64{1, 2}
	a, err := s.Save(t.Context(), 0, a)
	if err != nil {
		t.Fatal(err)
	}
	b := probeInput()
	b.Name, b.Enabled = "globally disabled", false
	b, err = s.Save(t.Context(), 0, b)
	if err != nil {
		t.Fatal(err)
	}
	c := probeInput()
	c.Name, c.Type, c.Port, c.ServerIDs = "ICMP", "icmp", nil, []int64{2}
	c, err = s.Save(t.Context(), 0, c)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	latency := 12.0
	if err := s.Ingest(t.Context(), 1, caps[1], ProbeResult{TaskID: a.ID, Outcome: "success", LatencyMS: &latency}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, []int64{a.ID, c.ID}); err != nil {
		t.Fatal(err)
	}
	after, err := s.List(t.Context())
	if err != nil || !reflect.DeepEqual(after[0].ServerIDs, []int64{1, 2}) || len(after[1].ServerIDs) != 0 || !reflect.DeepEqual(after[2].ServerIDs, []int64{1, 2}) {
		t.Fatalf("server-centric replacement changed other assignments: %v", err)
	}
	for i := range before {
		before[i].ServerIDs = after[i].ServerIDs
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("assignment edit changed global task fields or timestamps")
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, []int64{a.ID, b.ID, c.ID}); err != nil {
		t.Fatal(err)
	}
	assigned, err := s.ListServerProbeAssignments(t.Context(), 1)
	if err != nil || len(assigned) != 3 || !assigned[1].Assigned || assigned[1].Enabled {
		t.Fatalf("disabled task assignment not preserved: %v", err)
	}
	for _, tc := range []struct {
		capabilities map[string]bool
		count        int
	}{{nil, 0}, {caps[2], 1}, {caps[1], 2}} {
		wanted, err := s.Desired(t.Context(), 1, tc.capabilities)
		if err != nil || len(wanted) != tc.count {
			t.Fatalf("desired count=%d, want %d: %v", len(wanted), tc.count, err)
		}
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, []int64{}); err != nil {
		t.Fatal(err)
	}
	for _, hours := range []int{1, 6, 24} {
		history, err := s.History(t.Context(), 1, hours, caps[1])
		if err != nil || len(history.Tasks) != 0 || len(history.Samples) != 0 {
			t.Fatalf("history should follow current assignments: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records WHERE server_id = 1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unassignment deleted history: count=%d, %v", count, err)
	}
	// Restoring an assignment makes retained history visible again.
	if err := s.SetServerProbeAssignments(t.Context(), 1, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	for _, hours := range []int{1, 6, 24} {
		history, err := s.History(t.Context(), 1, hours, caps[1])
		if err != nil || len(history.Samples) != 1 || *history.Samples[0].LatencyMS != latency {
			t.Fatalf("restored assignment lost history: %v", err)
		}
	}
	// A task-centric edit is reflected by the server-centric reader.
	c.ServerIDs = []int64{1}
	if _, err := s.Save(t.Context(), c.ID, c); err != nil {
		t.Fatal(err)
	}
	assigned, err = s.ListServerProbeAssignments(t.Context(), 2)
	if err != nil || !assigned[0].Assigned || assigned[1].Assigned || assigned[2].Assigned {
		t.Fatalf("task-centric edit not reflected: %v", err)
	}
}

func TestServerProbeAssignmentsValidationAndRollback(t *testing.T) {
	s, db, _ := probeTestService(t)
	a, err := s.Save(t.Context(), 0, probeInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		serverID  int64
		ids       []int64
		lifecycle string
		want      error
	}{
		{name: "zero task", serverID: 1, ids: []int64{0}, want: ErrInvalid},
		{name: "negative task", serverID: 1, ids: []int64{-1}, want: ErrInvalid},
		{name: "duplicate task", serverID: 1, ids: []int64{a.ID, a.ID}, want: ErrInvalid},
		{name: "missing task", serverID: 1, ids: []int64{a.ID, 9999}, want: ErrInvalid},
		{name: "missing server", serverID: 9999, ids: []int64{a.ID}, want: ErrInvalid},
		{name: "archived server", serverID: 1, ids: []int64{}, lifecycle: `UPDATE servers SET archived_at = 1 WHERE id = 1`, want: ErrInvalid},
		{name: "decommissioning server", serverID: 1, ids: []int64{}, lifecycle: `UPDATE servers SET decommission_status = 'pending' WHERE id = 1`, want: ErrInvalid},
		{name: "task limit", serverID: 1, ids: make([]int64, MaxProbeTasks+1), want: ErrTaskLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lifecycle != "" {
				if _, err := db.Exec(tc.lifecycle); err != nil {
					t.Fatal(err)
				}
			}
			var beforeCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_servers WHERE server_id = 1 AND task_id = ?`, a.ID).Scan(&beforeCount); err != nil {
				t.Fatal(err)
			}
			if err := s.SetServerProbeAssignments(t.Context(), tc.serverID, tc.ids); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if _, err := db.Exec(`UPDATE servers SET archived_at = NULL, decommission_status = '' WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_servers WHERE server_id = 1 AND task_id = ?`, a.ID).Scan(&count); err != nil || count != beforeCount {
				t.Fatalf("failed validation changed assignments: %v", err)
			}
			// The existing archive trigger removes assignments before the method runs.
			if _, err := db.Exec(`INSERT OR IGNORE INTO monitor_probe_servers (task_id, server_id) VALUES (?, 1)`, a.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	b := probeInput()
	b.ServerIDs = nil
	b, err = s.Save(t.Context(), 0, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_assignment BEFORE INSERT ON monitor_probe_servers WHEN NEW.task_id = ` + strconv.FormatInt(b.ID, 10) + `
		BEGIN SELECT RAISE(ABORT, 'assignment rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, []int64{b.ID}); err == nil {
		t.Fatal("insertion failure was accepted")
	}
	assigned, err := s.ListServerProbeAssignments(t.Context(), 1)
	if err != nil || !assigned[0].Assigned || assigned[1].Assigned {
		t.Fatalf("insertion failure did not roll back deletion: %v", err)
	}
}

func TestServerProbeAssignmentsAccepts64Tasks(t *testing.T) {
	s, db, _ := probeTestService(t)
	ids := make([]int64, 0, MaxProbeTasks)
	for range MaxProbeTasks {
		value := probeInput()
		value.Enabled, value.ServerIDs = false, nil
		task, err := s.Save(t.Context(), 0, value)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	if _, err := db.Exec(`UPDATE servers SET status = 'offline' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, ids); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServerProbeAssignments(t.Context(), 1, append(ids, 99999)); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("65 tasks accepted: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_servers WHERE server_id = 1`).Scan(&count); err != nil || count != MaxProbeTasks {
		t.Fatalf("64 offline disabled assignments not preserved: %d, %v", count, err)
	}
}
