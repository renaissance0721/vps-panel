package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestSubscriptionRelayServersAndPublishedNodeCreation(t *testing.T) {
	fixture := newProxyDeleteFixture(t)
	sourceServerID := fixture.createServer(t, "CoreNet HK")
	targetServerID := fixture.createServer(t, "Legend SG")
	targetProxy := fixture.createProxy(t, targetServerID, 443, "SG 原生落地")
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

	creation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/subscription/nodes", map[string]any{
		"name": "HK Realm", "mode": "relay", "source_server_id": sourceServerID,
		"target_proxy_id": targetProxy.ID,
	}, fixture.cookie)
	var created struct {
		Node subscriptionPublishedNodeResponse `json:"node"`
	}
	if creation.Code != http.StatusCreated || json.Unmarshal(creation.Body.Bytes(), &created) != nil {
		t.Fatalf("create relay published node = %d, %s", creation.Code, creation.Body.String())
	}
	if created.Node.SourceServerID == nil || *created.Node.SourceServerID != sourceServerID ||
		created.Node.SourceServerName != "CoreNet HK" || strings.Contains(creation.Body.String(), "source_proxy") {
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
