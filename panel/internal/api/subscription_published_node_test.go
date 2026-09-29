package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestSubscriptionRelayServersAndPublishedNodeCreation(t *testing.T) {
	fixture := newProxyDeleteFixture(t)
	sourceServerID := fixture.createServer(t, "CoreNet HK")
	targetServerID := fixture.createServer(t, "Legend SG")
	targetProxy := fixture.createProxy(t, targetServerID, 443, "SG 原生落地")
	fixture.createProxy(t, sourceServerID, 24443, "Occupied Source Port")
	vipServerID := fixture.createServer(t, "VIP Server")
	unsupportedServerID := fixture.createServer(t, "Unsupported Server")
	decommissioningServerID := fixture.createServer(t, "Decommissioning Server")
	if _, err := fixture.db.Exec(`UPDATE servers SET created_by_role = 'vip' WHERE id = ?`, vipServerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE servers SET decommissioning_at = 1, decommission_status = 'pending' WHERE id = ?`, decommissioningServerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`INSERT INTO agents
		(server_id, token_hash, implementation, version, api_version, capabilities_json,
		 registered_at, created_at, updated_at)
		VALUES (?, 'unsupported-token', 'vps-panel-agent', 'test', ?, '[]', 1, 1, 1)`,
		unsupportedServerID, agentcontrol.CurrentAPIVersion); err != nil {
		t.Fatal(err)
	}

	response := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/subscription/relay-servers", nil, fixture.cookie)
	var listed struct {
		Servers []subscriptionRelayServerResponse `json:"servers"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &listed) != nil {
		t.Fatalf("list subscription relay servers = %d, %s", response.Code, response.Body.String())
	}
	listedIDs := make(map[int64]bool)
	for _, value := range listed.Servers {
		listedIDs[value.ID] = true
		if value.ID == sourceServerID && value.Name != "CoreNet HK" {
			t.Fatalf("source server option = %+v", value)
		}
	}
	if !listedIDs[sourceServerID] || !listedIDs[targetServerID] || listedIDs[vipServerID] ||
		listedIDs[unsupportedServerID] || listedIDs[decommissioningServerID] {
		t.Fatalf("relay server ids = %+v", listedIDs)
	}
	for _, invalid := range []struct {
		name     string
		body     map[string]any
		status   int
		contains string
	}{
		{name: "host", body: map[string]any{
			"name": "Invalid Host", "mode": "relay", "source_server_id": sourceServerID,
			"target_proxy_id": targetProxy.ID, "entry_host_mode": "manual", "entry_host": "https://example.com",
		}, status: http.StatusBadRequest, contains: "不能包含协议"},
		{name: "port range", body: map[string]any{
			"name": "Invalid Port", "mode": "relay", "source_server_id": sourceServerID,
			"target_proxy_id": targetProxy.ID, "entry_port_mode": "manual", "entry_port": 0,
		}, status: http.StatusBadRequest, contains: "1–65535"},
		{name: "port conflict", body: map[string]any{
			"name": "Conflict", "mode": "relay", "source_server_id": sourceServerID,
			"target_proxy_id": targetProxy.ID, "entry_port_mode": "manual", "entry_port": 24443,
		}, status: http.StatusConflict, contains: "入口端口已被代理节点、中转规则或保留端口占用"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			response := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/subscription/nodes", invalid.body, fixture.cookie)
			if response.Code != invalid.status || !strings.Contains(response.Body.String(), invalid.contains) {
				t.Fatalf("invalid endpoint response = %d, %s", response.Code, response.Body.String())
			}
		})
	}

	creation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/subscription/nodes", map[string]any{
		"name": "HK Realm", "mode": "relay", "source_server_id": sourceServerID,
		"target_proxy_id": targetProxy.ID, "entry_host_mode": "manual", "entry_host": "realm.example.com",
		"entry_port_mode": "manual", "entry_port": 24444,
	}, fixture.cookie)
	var created struct {
		Node subscriptionPublishedNodeResponse `json:"node"`
	}
	if creation.Code != http.StatusCreated || json.Unmarshal(creation.Body.Bytes(), &created) != nil {
		t.Fatalf("create relay published node = %d, %s", creation.Code, creation.Body.String())
	}
	if created.Node.SourceServerID == nil || *created.Node.SourceServerID != sourceServerID ||
		created.Node.SourceServerName != "CoreNet HK" || created.Node.EntryAddress != "realm.example.com" ||
		created.Node.EntryPort != 24444 || created.Node.EntryPortMode != "manual" || created.Node.RelayID == nil ||
		strings.Contains(creation.Body.String(), "source_proxy") {
		t.Fatalf("created relay published node = %+v", created.Node)
	}
	var relayServerID, relayTargetProxyID int64
	if err := fixture.db.QueryRow(`SELECT relays.server_id, relays.target_proxy_id
		FROM subscription_published_nodes AS nodes
		JOIN relays ON relays.id = nodes.relay_id WHERE nodes.id = ?`, created.Node.ID).
		Scan(&relayServerID, &relayTargetProxyID); err != nil {
		t.Fatal(err)
	}
	if relayServerID != sourceServerID || relayTargetProxyID != targetProxy.ID {
		t.Fatalf("subscription Relay = server %d target %d", relayServerID, relayTargetProxyID)
	}
	originalRelayID := *created.Node.RelayID
	conflictingUpdate := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/subscription/nodes/"+strconv.FormatInt(created.Node.ID, 10), map[string]any{
			"entry_host_mode": "manual", "entry_host": "realm.example.com",
			"entry_port_mode": "manual", "entry_port": 24443,
		}, fixture.cookie)
	if conflictingUpdate.Code != http.StatusConflict ||
		!strings.Contains(conflictingUpdate.Body.String(), "入口端口已被代理节点、中转规则或保留端口占用") {
		t.Fatalf("conflicting endpoint update = %d, %s", conflictingUpdate.Code, conflictingUpdate.Body.String())
	}
	update := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/subscription/nodes/"+strconv.FormatInt(created.Node.ID, 10), map[string]any{
			"entry_host_mode": "manual", "entry_host": "updated.example.com",
			"entry_port_mode": "manual", "entry_port": 24445,
		}, fixture.cookie)
	var updated struct {
		Node subscriptionPublishedNodeResponse `json:"node"`
	}
	if update.Code != http.StatusOK || json.Unmarshal(update.Body.Bytes(), &updated) != nil ||
		updated.Node.RelayID == nil || *updated.Node.RelayID != originalRelayID ||
		updated.Node.EntryAddress != "updated.example.com" || updated.Node.EntryPort != 24445 {
		t.Fatalf("update relay endpoint = %d, %s", update.Code, update.Body.String())
	}
}

func TestDecodeTrafficMultiplier(t *testing.T) {
	for input, want := range map[string]int{
		"0.1":  10,
		"0.5":  50,
		"1":    100,
		"1.25": 125,
		"5":    500,
	} {
		got, set, err := decodeTrafficMultiplier(json.RawMessage(input))
		if err != nil || !set || got != want {
			t.Fatalf("decode multiplier %s = %d/%v, error = %v; want %d/true", input, got, set, err, want)
		}
	}
	for _, input := range []string{"0", "0.09", "5.01", "-1", "1.234", "null", `"1"`} {
		if _, _, err := decodeTrafficMultiplier(json.RawMessage(input)); err == nil {
			t.Fatalf("decode invalid multiplier %s succeeded", input)
		}
	}
	if got, set, err := decodeTrafficMultiplier(nil); err != nil || set || got != 0 {
		t.Fatalf("decode missing multiplier = %d/%v, error = %v", got, set, err)
	}
}
