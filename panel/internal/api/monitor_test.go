package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	input := map[string]any{"name": "TCP", "type": "tcp", "target": "example.com:443", "default_on": false, "server_ids": []int64{created.ID}}
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
	input["target"] = "example.com"
	if response := performRequest(t, handler, "POST", "/api/monitor/probes", input, cookie); response.Code != 201 {
		t.Fatalf("assignment awaiting capability = %d", response.Code)
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Type != "tcp" || list.Version != 3 {
		t.Fatalf("unsupported task dispatched = %+v", list)
	}
	// A supplied server_id is ignored; task IDs must belong to the authenticated server.
	other, err := servers.Create(t.Context(), "other")
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	otherTask, err := monitor.NewService(db).Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "other task", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, ServerIDs: []int64{other.ID}})
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
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Name != "renamed" || list.Version != 4 {
		t.Fatalf("updated = %+v", list)
	}
	conn.CloseNow()
	waitForServerStatus(t, servers, created.ID, "offline")
	if response := performRequest(t, handler, "PATCH", path, map[string]any{"name": "offline edit"}, cookie); response.Code != 200 {
		t.Fatalf("offline capable assignment rejected: %s", response.Body.String())
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

func TestProbeAPIEndpointsAndDefaults(t *testing.T) {
	_, handler, cookie := monitorTestAPI(t)
	for _, tc := range []struct {
		kind, target, host, message string
		port                        int
	}{
		{"tcp", "example.com:443", "example.com", "", 443},
		{"tcp", "1.1.1.1:80", "1.1.1.1", "", 80},
		{"tcp", "[2400:3200::1]:443", "2400:3200::1", "", 443},
		{"icmp", "example.com", "example.com", "", 0},
		{"icmp", "1.1.1.1", "1.1.1.1", "", 0},
		{"icmp", "2400:3200::1", "2400:3200::1", "", 0},
		{"tcp", "example.com", "", "TCP 探测目标必须包含端口，例如 example.com:443", 0},
		{"tcp", "2001:db8::1:443", "", "IPv6 TCP 目标请使用 [IPv6]:端口 格式", 0},
		{"tcp", "example.com:0", "", "TCP 端口", 0},
		{"tcp", "example.com:65536", "", "TCP 端口", 0},
		{"tcp", "example.com:https", "", "TCP 端口", 0},
		{"tcp", "example.com:+443", "", "TCP 端口", 0},
		{"tcp", ":443", "", "有效的主机名或 IP", 0},
		{"icmp", "example.com:80", "", "不能包含端口", 0},
		{"icmp", "[2400:3200::1]:443", "", "不能包含端口", 0},
	} {
		t.Run(tc.kind+tc.target, func(t *testing.T) {
			response := performRequest(t, handler, "POST", "/api/monitor/probes", map[string]any{"name": "probe", "type": tc.kind, "target": tc.target}, cookie)
			if tc.message != "" {
				if response.Code != 400 || !strings.Contains(response.Body.String(), tc.message) {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
				return
			}
			var saved struct {
				Task monitor.Task `json:"task"`
			}
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &saved) != nil {
				t.Fatalf("create = %d %s", response.Code, response.Body.String())
			}
			if saved.Task.Target != tc.host || !saved.Task.DefaultOn || !saved.Task.Enabled || len(saved.Task.ServerIDs) != 0 {
				t.Fatalf("task = %+v", saved.Task)
			}
			if tc.port == 0 && saved.Task.Port != nil || tc.port != 0 && (saved.Task.Port == nil || *saved.Task.Port != tc.port) {
				t.Fatalf("port = %v", saved.Task.Port)
			}
			path := "/api/monitor/probes/" + strconv.FormatInt(saved.Task.ID, 10)
			response = performRequest(t, handler, "PATCH", path, map[string]any{"enabled": false}, cookie)
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.Task.Enabled || !saved.Task.DefaultOn || saved.Task.Target != tc.host {
				t.Fatalf("patch = %s", response.Body.String())
			}
		})
	}
}

func TestDefaultProbeNewConnectionAndCapabilityUpgrade(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	for _, kind := range []string{"tcp", "icmp"} {
		target := "example.com"
		if kind == "tcp" {
			target += ":443"
		}
		response := performRequest(t, handler, "POST", "/api/monitor/probes", map[string]any{"name": kind, "type": kind, "target": target}, cookie)
		if response.Code != 201 {
			t.Fatal(response.Body.String())
		}
	}
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "new server")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	panel := httptest.NewServer(handler)
	defer panel.Close()
	connect := func(capabilities string) *websocket.Conn {
		t.Helper()
		header := http.Header{}
		header.Set("Authorization", "Bearer "+registered.Token)
		header.Set("X-VPS-Panel-Agent-Implementation", "third-party")
		header.Set("X-VPS-Panel-Agent-Version", "v1.0.0")
		header.Set("X-VPS-Panel-Agent-API", "1")
		header.Set("X-VPS-Panel-Agent-Capabilities", capabilities)
		conn, _, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	// The task predates the server, which inherits assignments before registration.
	conn := connect("probe.icmp")
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Type != "icmp" {
		t.Fatalf("initial = %+v", list)
	}
	conn.CloseNow()
	waitForServerStatus(t, servers, created.ID, "offline")
	// Upgrade gains TCP support: the new full list includes both tasks immediately.
	conn = connect("probe.icmp,probe.tcp")
	if list := readProbeList(t, conn); len(list.Tasks) != 2 || list.Version != 1 {
		t.Fatalf("upgraded = %+v", list)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM monitor_probe_servers`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("materialized assignments = %d, %v", count, err)
	}
	response := performRequest(t, handler, "PATCH", "/api/monitor/probes/1", map[string]any{"enabled": false}, cookie)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Type != "icmp" {
		t.Fatalf("disabled = %+v", list)
	}
	response = performRequest(t, handler, "PATCH", "/api/monitor/probes/2", map[string]any{"default_on": false}, cookie)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if list := readProbeList(t, conn); len(list.Tasks) != 1 || list.Tasks[0].Type != "icmp" {
		t.Fatalf("turning off inheritance removed assignment = %+v", list)
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

func TestMonitorLatencyAPISummaryFollowsHours(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	for _, stmt := range []string{
		`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'probe', 'offline', 1, 1)`,
		`INSERT INTO agents (server_id, token_hash, version, capabilities_json, registered_at, created_at, updated_at) VALUES (1, 'test', 'v1', '["probe.tcp","probe.icmp"]', 1, 1, 1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"tcp", "icmp"} {
		target := "example.com"
		if kind == "tcp" {
			target += ":443"
		}
		response := performRequest(t, handler, "POST", "/api/monitor/probes", map[string]any{"name": kind, "type": kind, "target": target, "server_ids": []int64{1}}, cookie)
		if response.Code != 201 {
			t.Fatal(response.Body.String())
		}
	}
	now := time.Now()
	for _, id := range []int{1, 2} {
		for _, age := range []time.Duration{2 * time.Hour, 10 * time.Hour} {
			if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, ?, ?, 'timeout', NULL)`, id, now.Add(-age).UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO monitor_probe_records VALUES (1, ?, ?, 'success', 42)`, id, now.Add(-30*time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query        string
		hours, count int
		rate         float64
	}{
		{"", 1, 2, 0}, {"?hours=1", 1, 2, 0}, {"?hours=6", 6, 4, 50}, {"?hours=24", 24, 6, 200.0 / 3},
	} {
		response := performRequest(t, handler, "GET", "/api/monitor/servers/1/latency"+tc.query, nil, cookie)
		var h monitor.History
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &h) != nil || h.RangeHours != tc.hours || len(h.Samples) != tc.count || len(h.Tasks) != 2 {
			t.Fatalf("history = %d %s", response.Code, response.Body.String())
		}
		for _, task := range h.Tasks {
			if task.FailureRate == nil || math.Abs(*task.FailureRate-tc.rate) > .001 || task.LatestLatencyMS == nil || *task.LatestLatencyMS != 42 {
				t.Fatalf("%d hours summary = %+v", tc.hours, task)
			}
		}
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
	task, err := probes.Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "probe", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, ServerIDs: []int64{created.ID}})
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

func TestProbeAPIDefaultInheritancePreservesExplicitSelection(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	servers := serverstore.NewService(db)
	a, err := servers.Create(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := servers.Create(t.Context(), "B")
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, handler, "POST", "/api/monitor/probes", map[string]any{"name": "default", "type": "tcp", "target": "example.com:443", "default_on": true, "server_ids": []int64{a.ID}}, cookie)
	var saved struct {
		Task monitor.Task `json:"task"`
	}
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &saved) != nil || !saved.Task.DefaultOn || len(saved.Task.ServerIDs) != 1 || saved.Task.ServerIDs[0] != a.ID {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	probes := monitor.NewService(db)
	caps := map[string]bool{monitor.CapabilityTCP: true}
	if tasks, err := probes.Desired(t.Context(), b.ID, caps); err != nil || len(tasks) != 0 {
		t.Fatalf("existing unselected = %+v %v", tasks, err)
	}
	response = performRequest(t, handler, "POST", "/api/servers", map[string]any{"name": "C", "visibility": "public"}, cookie)
	if response.Code != 201 {
		t.Fatalf("new server = %d %s", response.Code, response.Body.String())
	}
	path := "/api/monitor/probes/" + strconv.FormatInt(saved.Task.ID, 10)
	// PATCH omitting server_ids must retain both manual and automatically inherited rows.
	response = performRequest(t, handler, "PATCH", path, map[string]any{"default_on": false}, cookie)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.Task.DefaultOn || len(saved.Task.ServerIDs) != 2 || saved.Task.ServerIDs[0] != a.ID {
		t.Fatalf("disable default = %d %s", response.Code, response.Body.String())
	}
	cID := saved.Task.ServerIDs[1]
	if cID == b.ID {
		t.Fatal("default applied to unselected existing server")
	}
	d, err := servers.Create(t.Context(), "D")
	if err != nil {
		t.Fatal(err)
	}
	if tasks, err := probes.Desired(t.Context(), d.ID, caps); err != nil || len(tasks) != 0 {
		t.Fatalf("disabled inheritance = %+v %v", tasks, err)
	}
	response = performRequest(t, handler, "PATCH", path, map[string]any{"server_ids": []int64{cID}, "default_on": true}, cookie)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &saved) != nil || !saved.Task.DefaultOn || len(saved.Task.ServerIDs) != 1 || saved.Task.ServerIDs[0] != cID {
		t.Fatalf("edit assignments = %d %s", response.Code, response.Body.String())
	}
	for _, id := range []int64{a.ID, b.ID, d.ID} {
		if tasks, err := probes.Desired(t.Context(), id, caps); err != nil || len(tasks) != 0 {
			t.Fatalf("unselected %d = %+v %v", id, tasks, err)
		}
	}
	e, err := servers.Create(t.Context(), "E")
	if err != nil {
		t.Fatal(err)
	}
	if tasks, err := probes.Desired(t.Context(), e.ID, caps); err != nil || len(tasks) != 1 {
		t.Fatalf("future inheritance = %+v %v", tasks, err)
	}
	response = performRequest(t, handler, "GET", "/api/monitor/probes", nil, cookie)
	var list struct {
		Tasks []monitor.Task `json:"tasks"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Tasks) != 1 || len(list.Tasks[0].ServerIDs) != 2 {
		t.Fatalf("list = %d %s", response.Code, response.Body.String())
	}
}
