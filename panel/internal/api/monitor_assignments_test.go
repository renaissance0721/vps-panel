package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestServerProbeAssignmentsAPIIsolationPermissionsAndDefaults(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	servers, probes := serverstore.NewService(db), monitor.NewService(db)
	a, err := servers.Create(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := servers.Create(t.Context(), "B")
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	taskA, err := probes.Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "上海电信", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true, DefaultOn: true, ServerIDs: []int64{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := probes.Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "广州联通", Type: "icmp", Target: "example.net", IntervalSeconds: 30}, Enabled: false, ServerIDs: []int64{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/monitor/servers/%d/probe-assignments", a.ID)
	read := func(serverID int64) []monitor.ServerProbeAssignmentTask {
		t.Helper()
		response := performRequest(t, handler, "GET", fmt.Sprintf("/api/monitor/servers/%d/probe-assignments", serverID), nil, cookie)
		var body struct {
			Tasks []monitor.ServerProbeAssignmentTask `json:"tasks"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("GET = %d %s", response.Code, response.Body.String())
		}
		return body.Tasks
	}
	initial := read(a.ID)
	if len(initial) != 2 || !initial[0].Assigned || initial[1].Assigned || initial[1].Enabled || initial[0].Port == nil || *initial[0].Port != 443 {
		t.Fatal("incorrect assignment DTO")
	}
	put := func(body any, want int) {
		t.Helper()
		response := performRequest(t, handler, "PUT", path, body, cookie)
		if response.Code != want {
			t.Fatalf("PUT = %d, want %d: %s", response.Code, want, response.Body.String())
		}
	}
	put(map[string]any{"task_ids": []int64{taskB.ID}}, 204)
	current := read(a.ID)
	if current[0].Assigned || !current[1].Assigned || current[1].Enabled {
		t.Fatal("PUT did not preserve disabled assignment independently")
	}
	other := read(b.ID)
	if !other[0].Assigned || !other[1].Assigned {
		t.Fatal("PUT A changed B")
	}
	for _, body := range []map[string]any{
		{}, {"task_ids": nil}, {"task_ids": []int64{0}}, {"task_ids": []int64{-1}},
		{"task_ids": []int64{taskA.ID, taskA.ID}}, {"task_ids": []int64{taskA.ID, 99999}}, {"task_ids": make([]int64, 65)},
	} {
		put(body, 400)
	}
	if got := read(a.ID); !reflect.DeepEqual(got, current) {
		t.Fatal("invalid PUT changed assignments")
	}
	// A task-centric PATCH sees current assignments rather than a stale snapshot.
	response := performRequest(t, handler, "PATCH", fmt.Sprintf("/api/monitor/probes/%d", taskB.ID), map[string]any{"name": "广州联通更新"}, cookie)
	if response.Code != 200 || !read(a.ID)[1].Assigned {
		t.Fatal("global manager lost server-centric assignment")
	}
	response = performRequest(t, handler, "PATCH", fmt.Sprintf("/api/monitor/probes/%d", taskA.ID), map[string]any{"server_ids": []int64{a.ID, b.ID}}, cookie)
	if response.Code != 200 || !read(a.ID)[0].Assigned {
		t.Fatal("server reader did not reflect global manager")
	}
	put(map[string]any{"task_ids": []int64{}}, 204)
	for _, task := range read(a.ID) {
		if task.Assigned {
			t.Fatal("empty assignment not saved")
		}
	}
	newServer, err := servers.Create(t.Context(), "new default inheritor")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(newServer.ID); !got[0].Assigned || got[1].Assigned {
		t.Fatal("server override changed default_on inheritance")
	}
	for _, role := range []string{"vip", "carpool", "subscriber"} {
		roleCookie, _ := registerAccount(t, db, handler, cookie, role, "probe-"+role)
		for _, method := range []string{"GET", "PUT"} {
			response := performRequest(t, handler, method, path, map[string]any{"task_ids": []int64{}}, roleCookie)
			if response.Code != 403 {
				t.Fatalf("%s %s = %d", role, method, response.Code)
			}
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		if response := performRequest(t, handler, method, path, map[string]any{"task_ids": []int64{}}, nil); response.Code != 401 {
			t.Fatalf("anonymous %s = %d", method, response.Code)
		}
	}
	for _, tc := range []struct {
		name, change string
		want         int
	}{
		{"private", `UPDATE servers SET visibility = 'private' WHERE id = ?`, 404},
		{"archived", `UPDATE servers SET archived_at = 1 WHERE id = ?`, 404},
		{"decommission", `UPDATE servers SET decommission_status = 'pending' WHERE id = ?`, 409},
	} {
		if _, err := db.Exec(tc.change, a.ID); err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "PUT"} {
			response := performRequest(t, handler, method, path, map[string]any{"task_ids": []int64{taskA.ID}}, cookie)
			if response.Code != tc.want {
				t.Fatalf("%s %s = %d %s", tc.name, method, response.Code, response.Body.String())
			}
		}
		if _, err := db.Exec(`UPDATE servers SET visibility = 'public', archived_at = NULL, decommission_status = '' WHERE id = ?`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		response := performRequest(t, handler, method, "/api/monitor/servers/99999/probe-assignments", map[string]any{"task_ids": []int64{}}, cookie)
		if response.Code != 404 {
			t.Fatalf("missing server %s = %d", method, response.Code)
		}
	}
}

func TestServerProbeAssignmentsNotifyOnlyTargetAndSerializeTaskPatch(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	servers, probes := serverstore.NewService(db), monitor.NewService(db)
	agents := agentcontrol.NewService(db, time.Now)
	panel := httptest.NewServer(handler)
	defer panel.Close()
	serverIDs := []int64{}
	connections := []*websocket.Conn{}
	for _, name := range []string{"A", "B"} {
		created, err := servers.Create(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		registered, err := agents.RegisterAgent(t.Context(), created.EnrollmentToken, "test", false)
		if err != nil {
			t.Fatal(err)
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+registered.Token)
		header.Set("X-VPS-Panel-Agent-Implementation", "third-party")
		header.Set("X-VPS-Panel-Agent-Version", "test")
		header.Set("X-VPS-Panel-Agent-API", "1")
		header.Set("X-VPS-Panel-Agent-Capabilities", "probe.tcp")
		connection, _, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		defer connection.CloseNow()
		if list := readProbeList(t, connection); list.Version != 1 || len(list.Tasks) != 0 {
			t.Fatal("unexpected initial list")
		}
		serverIDs = append(serverIDs, created.ID)
		connections = append(connections, connection)
	}
	port := 443
	task, err := probes.Save(t.Context(), 0, monitor.Task{ProbeTask: monitor.ProbeTask{Name: "TCP", Type: "tcp", Target: "example.com", Port: &port, IntervalSeconds: 60}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for index, serverID := range serverIDs {
		path := fmt.Sprintf("/api/monitor/servers/%d/probe-assignments", serverID)
		response := performRequest(t, handler, "PUT", path, map[string]any{"task_ids": []int64{task.ID}}, cookie)
		if response.Code != 204 {
			t.Fatalf("PUT = %d %s", response.Code, response.Body.String())
		}
		// B's first post-connect message must be its own update, not A's earlier PUT.
		if list := readProbeList(t, connections[index]); list.Version != 2 || len(list.Tasks) != 1 || list.Tasks[0].ID != task.ID {
			t.Fatalf("non-target update or incorrect desired list: %+v", list)
		}
	}
	for _, connection := range connections {
		connection.CloseNow()
	}
	for range 8 {
		path := fmt.Sprintf("/api/monitor/servers/%d/probe-assignments", serverIDs[0])
		if response := performRequest(t, handler, "PUT", path, map[string]any{"task_ids": []int64{task.ID}}, cookie); response.Code != 204 {
			t.Fatal(response.Body.String())
		}
		responses := make(chan int, 2)
		go func() {
			responses <- performRequest(t, handler, "PUT", path, map[string]any{"task_ids": []int64{}}, cookie).Code
		}()
		go func() {
			responses <- performRequest(t, handler, "PATCH", fmt.Sprintf("/api/monitor/probes/%d", task.ID), map[string]any{"name": "renamed"}, cookie).Code
		}()
		for range 2 {
			code := <-responses
			if code != 200 && code != 204 {
				t.Fatalf("concurrent mutation = %d", code)
			}
		}
		got, err := probes.ListServerProbeAssignments(t.Context(), serverIDs[0])
		if err != nil || got[0].Assigned {
			t.Fatalf("task PATCH overwrote server assignment: %v", err)
		}
	}
}
