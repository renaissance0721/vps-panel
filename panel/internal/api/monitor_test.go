package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func monitorTestAPI(t *testing.T) (*sql.DB, http.Handler, *http.Cookie) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler := NewHandler(db, t.TempDir())
	response := performRequest(t, handler, "POST", "/api/auth/initialize", map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	return db, handler, response.Result().Cookies()[0]
}

func readProbeList(t *testing.T, connection *websocket.Conn) monitor.DesiredTasks {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var list monitor.DesiredTasks
	if err := json.Unmarshal(payload, &list); err != nil || list.Type != "probe_tasks" {
		t.Fatalf("list = %s, %v", payload, err)
	}
	return list
}

func TestProbeAPIWebSocketDesiredListsAndIngestion(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "probe server")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	header.Set("X-VPS-Panel-Agent-Implementation", "third-party")
	header.Set("X-VPS-Panel-Agent-Version", "v1.0.0")
	header.Set("X-VPS-Panel-Agent-API", "1")
	header.Set("X-VPS-Panel-Agent-Capabilities", "probe.tcp")
	connect := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	conn := connect()
	if initial := readProbeList(t, conn); len(initial.Tasks) != 0 || initial.Version != 1 {
		t.Fatalf("initial = %+v", initial)
	}
	input := map[string]any{"name": "TCP", "type": "tcp", "target": "example.com", "port": 443, "server_ids": []int64{created.ID}}
	response := performRequest(t, handler, "POST", "/api/monitor/probes", input, cookie)
	if response.Code != 201 {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	var saved struct {
		Task monitor.Task `json:"task"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	task := saved.Task
	if task.IntervalSeconds != 60 || !task.Enabled {
		t.Fatalf("defaults = %+v", task)
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].ID != task.ID || list.Version != 2 {
		t.Fatalf("created list = %+v", list)
	}
	input["type"] = "icmp"
	input["port"] = nil
	if response := performRequest(t, handler, "POST", "/api/monitor/probes", input, cookie); response.Code != 400 {
		t.Fatalf("unsupported create = %d", response.Code)
	}
	// A supplied server_id is ignored; task IDs must belong to the authenticated server.
	other, err := servers.Create(t.Context(), "other")
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	otherTask, err := monitor.NewService(db).Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "other task", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, ServerIDs: []int64{other.ID}}, map[int64]map[string]bool{other.ID: {monitor.CapabilityTCP: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]any{
		{"type": "probe_result", "task_id": otherTask.ID, "server_id": other.ID, "outcome": "success", "latency_ms": 99},
		{"type": "future_unknown_message"},
		{"type": "probe_result", "task_id": task.ID, "outcome": "timeout", "latency_ms": -1},
		{"type": "probe_result", "task_id": task.ID, "outcome": "success", "latency_ms": 42},
	} {
		encoded, _ := json.Marshal(payload)
		if err := conn.Write(t.Context(), websocket.MessageText, encoded); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record count = %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, hours := range []string{"1", "6", "24"} {
		response := performRequest(t, handler, "GET", "/api/monitor/servers/"+strconv.FormatInt(created.ID, 10)+"/latency?hours="+hours, nil, cookie)
		var history monitor.History
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil || len(history.Samples) != 2 || history.Tasks[0].LatestLatencyMS == nil || *history.Tasks[0].LatestLatencyMS != 42 || *history.Tasks[0].FailureRate != 50 {
			t.Fatalf("history = %d %s", response.Code, response.Body.String())
		}
		if history.Samples[0].LatencyMS != nil {
			t.Fatal("timeout latency was not null")
		}
	}
	path := "/api/monitor/probes/" + strconv.FormatInt(task.ID, 10)
	if response := performRequest(t, handler, "PATCH", path, map[string]any{"name": "renamed"}, cookie); response.Code != 200 {
		t.Fatalf("patch = %s", response.Body.String())
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Name != "renamed" || list.Version != 3 {
		t.Fatalf("updated = %+v", list)
	}
	conn.CloseNow()
	waitForServerStatus(t, servers, created.ID, "offline")
	input["type"] = "tcp"
	input["port"] = 443
	if response := performRequest(t, handler, "POST", "/api/monitor/probes", input, cookie); response.Code != 400 {
		t.Fatal("stored capabilities allowed offline assignment")
	}
	conn = connect()
	if list := readProbeList(t, conn); list.Version != 1 || len(list.Tasks) != 1 {
		t.Fatalf("reconnected = %+v", list)
	}
	if response := performRequest(t, handler, "PATCH", path, map[string]any{"server_ids": []int64{}}, cookie); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 0 {
		t.Fatal("removed assignment still dispatched")
	}
	if response := performRequest(t, handler, "DELETE", path, nil, cookie); response.Code != 204 {
		t.Fatal(response.Body.String())
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 0 {
		t.Fatal("deleted task still dispatched")
	}
	if response := performRequest(t, handler, "GET", "/api/monitor/probes", nil, cookie); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
}

func TestMonitorAPIPermissionsAndRanges(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	if _, err := db.Exec(`INSERT INTO servers (id, name, status, visibility, created_at, updated_at) VALUES (1, 'public', 'offline', 'public', 1, 1), (2, 'private', 'offline', 'private', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "0", "2", "168", "-1", "bad", "6&hours=24"} {
		if response := performRequest(t, handler, "GET", "/api/monitor/servers/1/latency?hours="+raw, nil, cookie); response.Code != 400 {
			t.Fatalf("range %q = %d", raw, response.Code)
		}
	}
	for _, role := range []string{"vip", "user", "subscriber"} {
		if _, err := db.Exec(`UPDATE users SET role = ? WHERE username = 'admin'`, role); err != nil {
			t.Fatal(err)
		}
		for _, req := range []struct{ method, path string }{{"GET", "/api/monitor/probes"}, {"POST", "/api/monitor/probes"}, {"PATCH", "/api/monitor/probes/1"}, {"DELETE", "/api/monitor/probes/1"}} {
			if response := performRequest(t, handler, req.method, req.path, map[string]string{}, cookie); response.Code != 403 {
				t.Fatalf("%s %s = %d for %s", req.method, req.path, response.Code, role)
			}
		}
		response := performRequest(t, handler, "GET", "/api/monitor/servers/1/latency?hours=6", nil, cookie)
		want := 403
		if role == "vip" {
			want = 200
		}
		if response.Code != want {
			t.Fatalf("latency role %s = %d", role, response.Code)
		}
		if role == "vip" {
			if response := performRequest(t, handler, "GET", "/api/monitor/servers/2/latency?hours=6", nil, cookie); response.Code != 404 {
				t.Fatal("private server history disclosed")
			}
		}
	}
	if response := performRequest(t, handler, "GET", "/api/monitor/servers/1/latency", nil, nil); response.Code != 401 {
		t.Fatal("anonymous history access")
	}
}

func TestOldProbeConnectionCannotWrite(t *testing.T) {
	db, _, _ := monitorTestAPI(t)
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	agents := agentcontrol.NewService(db, time.Now)
	probes := monitor.NewService(db)
	s := &server{agents: agents, monitor: probes}
	caps := map[string]bool{monitor.CapabilityTCP: true}
	old, current := &agentcontrol.Connection{Capabilities: caps}, &agentcontrol.Connection{Capabilities: caps}
	agents.TrackConnection(created.ID, old)
	port := 443
	task, err := probes.Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "probe", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, ServerIDs: []int64{created.ID}}, map[int64]map[string]bool{created.ID: caps})
	if err != nil {
		t.Fatal(err)
	}
	agents.TrackConnection(created.ID, current)
	if active, err := s.reportCurrentProbe(created.ID, old, monitor.ProbeResult{TaskID: task.ID, Outcome: "timeout"}); active || err != nil {
		t.Fatalf("old connection = %v %v", active, err)
	}
	if active, err := s.reportCurrentProbe(created.ID, current, monitor.ProbeResult{TaskID: task.ID, Outcome: "timeout"}); !active || err != nil {
		t.Fatalf("current connection = %v %v", active, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_records`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count = %d %v", count, err)
	}
}
