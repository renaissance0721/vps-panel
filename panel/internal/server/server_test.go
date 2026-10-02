package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestCreateStoresPendingServerAndHashedEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "  JP Native 01  ")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name != "JP Native 01" || created.Status != StatusPending {
		t.Fatalf("Create() server = %+v, want trimmed name and pending status", created.Server)
	}
	if created.OutboundPreference != OutboundAuto {
		t.Fatalf("new server outbound preference = %q, want auto", created.OutboundPreference)
	}
	if created.ExpiresAt != nil {
		t.Fatalf("Create() ExpiresAt = %v, want nil", created.ExpiresAt)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("Create() enrollment token is empty")
	}
	if created.EnrollmentExpiresAt.Sub(created.CreatedAt) != agentcontrol.EnrollmentLifetime {
		t.Fatalf("enrollment lifetime = %v, want %v", created.EnrollmentExpiresAt.Sub(created.CreatedAt), agentcontrol.EnrollmentLifetime)
	}

	var storedHash, purpose string
	if err := db.QueryRow(
		`SELECT token_hash, purpose FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&storedHash, &purpose); err != nil {
		t.Fatalf("read enrollment hash: %v", err)
	}
	if storedHash == created.EnrollmentToken || storedHash != token.Hash(created.EnrollmentToken) {
		t.Fatal("enrollment token was not stored as its hash")
	}
	if purpose != agentcontrol.PurposeInitial {
		t.Fatalf("enrollment purpose = %q, want %q", purpose, agentcontrol.PurposeInitial)
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	service, _ := newTestService(t)
	for _, name := range []string{"   ", strings.Repeat("a", maxNameLength+1)} {
		if _, err := service.Create(context.Background(), name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestUpdateName(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Original")
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return updatedAt }
	updated, err := service.UpdateName(context.Background(), created.ID, "  Renamed  ")
	if err != nil || updated.Name != "Renamed" || updated.ID != created.ID || !updated.UpdatedAt.Equal(updatedAt) || updated.Status != created.Status {
		t.Fatalf("UpdateName() = (%+v, %v)", updated, err)
	}
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Name != "Renamed" {
		t.Fatalf("List() = (%+v, %v)", listed, err)
	}
	for _, name := range []string{"   ", strings.Repeat("a", maxNameLength+1)} {
		if _, err := service.UpdateName(context.Background(), created.ID, name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("UpdateName(%q) error = %v", name, err)
		}
	}
	if _, err := service.UpdateName(context.Background(), created.ID+100, "Missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateName(missing) error = %v", err)
	}
}

func TestNewServerInheritsDefaultProbesBeforeRegistration(t *testing.T) {
	servers, db := newTestService(t)
	ctx := t.Context()
	a, err := servers.Create(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := servers.Create(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	probes := monitor.NewService(db)
	port := 443
	input := monitor.Task{ProbeTask: monitor.ProbeTask{Name: "TCP", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, DefaultOn: true, ServerIDs: []int64{a.ID}}
	tcp, err := probes.Save(ctx, 0, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Type, input.Port, input.Name, input.Enabled = "icmp", nil, "ICMP", false
	icmp, err := probes.Save(ctx, 0, input)
	if err != nil {
		t.Fatal(err)
	}
	input.DefaultOn = false
	if _, err := probes.Save(ctx, 0, input); err != nil {
		t.Fatal(err)
	}
	d, err := servers.CreateForUser(ctx, "D", VisibilityPublic, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusPending {
		t.Fatalf("status = %s", d.Status)
	}
	check := func(id int64, want int) {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_servers WHERE server_id = ?`, id).Scan(&count); err != nil || count != want {
			t.Fatalf("server %d assignments = %d, %v", id, count, err)
		}
	}
	check(a.ID, 3)
	check(b.ID, 0)
	check(d.ID, 2)
	caps := map[string]bool{monitor.CapabilityTCP: true}
	if tasks, err := probes.Desired(ctx, d.ID, nil); err != nil || len(tasks) != 0 {
		t.Fatalf("pending desired = %+v %v", tasks, err)
	}
	// Editing an inherited task retains the pending server even without capability.
	list, err := probes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	icmp = list[1]
	icmp.Enabled = true
	if _, err := probes.Save(ctx, icmp.ID, icmp); err != nil {
		t.Fatal(err)
	}
	if tasks, err := probes.Desired(ctx, d.ID, caps); err != nil || len(tasks) != 1 || tasks[0].ID != tcp.ID {
		t.Fatalf("TCP desired = %+v %v", tasks, err)
	}
	caps[monitor.CapabilityICMP] = true
	if tasks, err := probes.Desired(ctx, d.ID, caps); err != nil || len(tasks) != 2 {
		t.Fatalf("upgraded desired = %+v %v", tasks, err)
	}
	// Removal affects only the selected current server; future inheritance continues.
	tcp = list[0]
	tcp.ServerIDs = []int64{d.ID}
	if _, err := probes.Save(ctx, tcp.ID, tcp); err != nil {
		t.Fatal(err)
	}
	e, err := servers.Create(ctx, "E")
	if err != nil {
		t.Fatal(err)
	}
	check(e.ID, 2)
	list, err = probes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range list {
		task.DefaultOn = false
		if _, err := probes.Save(ctx, task.ID, task); err != nil {
			t.Fatal(err)
		}
	}
	check(d.ID, 2)
	check(e.ID, 2)
	f, err := servers.Create(ctx, "F")
	if err != nil {
		t.Fatal(err)
	}
	check(f.ID, 0)
}

func TestServerCreationRollsBackIfDefaultProbesExceedLimit(t *testing.T) {
	servers, db := newTestService(t)
	for i := 0; i < monitor.MaxProbeTasks; i++ {
		if _, err := db.Exec(`INSERT INTO monitor_probe_tasks (name,type,target,port,interval_seconds,enabled,default_on,created_at,updated_at) VALUES ('default','tcp','example.com',443,60,0,1,1,1)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := servers.Create(t.Context(), "at limit"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO monitor_probe_tasks (name,type,target,port,interval_seconds,enabled,default_on,created_at,updated_at) VALUES ('overflow','icmp','example.com',NULL,60,0,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := servers.Create(t.Context(), "overflow"); !errors.Is(err, monitor.ErrTaskLimit) {
		t.Fatalf("create = %v", err)
	}
	for table, want := range map[string]int{"servers": 1, "agent_enrollments": 1, "monitor_probe_servers": monitor.MaxProbeTasks} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s rollback = %d %v", table, count, err)
		}
	}
}
