package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRoutingINIWorkflow(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	source := "[custom]\ncustom_proxy_group=Group`select`[]DIRECT\nruleset=Group,[]FINAL\n"
	if response := performRequest(t, f.handler, "POST", "/api/admin/subscription/routing-ini", map[string]any{"name": "Unauthenticated", "source": source}, nil); response.Code != 401 {
		t.Fatal("unauthenticated web write was accepted")
	}
	if response := performRequest(t, f.handler, "POST", "/api/admin/subscription/routing-ini", map[string]any{"name": "VIP", "source": source}, f.memberCookie); response.Code != 403 {
		t.Fatal("non-admin web write was accepted")
	}
	reader := phase1Session(t, f.handler, f.cookie)
	denied, err := reader.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_routing_from_ini", Arguments: map[string]any{"name": "INI", "source": source}})
	if err != nil || !denied.IsError {
		t.Fatal("read token created routing preset")
	}
	validated := f.call(t, "validate_routing_ini", map[string]any{"source": source}, true)
	if len(validated["diagnostics"].([]any)) != 0 {
		t.Fatal("valid INI rejected")
	}
	created := f.call(t, "create_routing_from_ini", map[string]any{"name": "INI", "source": source}, true)
	id := int64(created["id"].(float64))
	exported := f.call(t, "get_routing_ini", map[string]any{"id": id}, true)
	revision := exported["revision"].(string)
	f.call(t, "preview_routing_ini", map[string]any{"source": source, "preset_id": id}, true)
	changed := "[custom]\ncustom_proxy_group=Group`select`[]REJECT\nruleset=Group,[]FINAL\n"
	f.call(t, "update_routing_from_ini", map[string]any{"id": id, "revision": revision, "source": changed}, true)
	f.call(t, "update_routing_from_ini", map[string]any{"id": id, "revision": revision, "source": source}, false)
	if w := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true, "mcp_write_enabled": false}, f.cookie); w.Code != 200 {
		t.Fatal("disable write switch")
	}
	f.call(t, "create_routing_from_ini", map[string]any{"name": "Blocked", "source": source}, false)
	f.call(t, "validate_routing_ini", map[string]any{"source": source}, true)
	var auditCount int
	if err := f.db.QueryRow(`SELECT count(*) FROM mcp_audit_logs WHERE tool_name='update_routing_from_ini'`).Scan(&auditCount); err != nil || auditCount < 2 {
		t.Fatal("INI tool calls were not audited")
	}
}

func TestMCPRoutingINIPersonalPreviewIsReadOnlyAndPrivate(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	landing := f.call(t, "create_landing", map[string]any{"name": "Fixture", "visibility": "private", "uri": "vless://00000000-0000-0000-0000-000000000001@node.example.test:443?type=tcp&security=tls"}, true)
	lid := objectID(t, landing, "landing")
	personal := f.call(t, "create_personal_subscription", map[string]any{"name": "Preview", "client_name": "client"}, true)
	id := objectID(t, personal, "personal_subscription")
	f.call(t, "set_personal_subscription_nodes", map[string]any{"id": id, "nodes": []any{map[string]any{"source_type": "landing", "source_id": lid, "display_name": "Japan One", "enabled": true}}}, true)
	source := "[custom]\ncustom_proxy_group=Japan`select`[]DIRECT`(Japan|JP)\nruleset=Japan,[]FINAL\n"
	var before string
	if err := f.db.QueryRow(`SELECT groups_json FROM subscription_routing_presets WHERE is_default=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	out := f.call(t, "preview_routing_ini", map[string]any{"source": source, "personal_subscription_id": id}, true)
	rendering := out["rendering"].(map[string]any)
	if rendering["mihomo"] != "success" || rendering["shadowrocket"] != "success" {
		t.Fatalf("rendering: %+v", rendering)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "00000000-0000") || strings.Contains(string(raw), "node.example.test") {
		t.Fatal("preview exposed credentials or endpoint")
	}
	var after string
	if err := f.db.QueryRow(`SELECT groups_json FROM subscription_routing_presets WHERE is_default=1`).Scan(&after); err != nil || before != after {
		t.Fatal("preview modified preset")
	}
	zero := strings.ReplaceAll(source, "(Japan|JP)", "NoMatch")
	out = f.call(t, "preview_routing_ini", map[string]any{"source": zero, "personal_subscription_id": id}, true)
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), "未匹配任何可用节点") {
		t.Fatal("missing zero-match warning")
	}
	other := performRequest(t, f.handler, "POST", "/api/personal-subscriptions", map[string]any{"name": "Private", "client_name": "client"}, f.memberCookie)
	var body struct {
		Personal struct {
			ID int64 `json:"id"`
		} `json:"personal_subscription"`
	}
	if other.Code != 201 || json.Unmarshal(other.Body.Bytes(), &body) != nil {
		t.Fatal("private fixture failed")
	}
	f.call(t, "preview_routing_ini", map[string]any{"source": source, "personal_subscription_id": body.Personal.ID}, false)
}

func TestMCPRoutingINISharedPreviewDoesNotReconcile(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	proxy := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "Proxy", "listen_port": 8999, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "client"}, true)
	pid := objectID(t, proxy, "proxy")
	node := f.call(t, "create_published_node", map[string]any{"name": "Shared JP", "mode": "direct", "target_proxy_id": pid}, true)
	nid := objectID(t, node, "node")
	plan := f.call(t, "create_subscription_plan", map[string]any{"name": "Plan"}, true)
	planID := objectID(t, plan, "plan")
	f.call(t, "set_subscription_plan_nodes", map[string]any{"id": planID, "node_ids": []int64{nid}}, true)
	for _, query := range []string{`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (100,'subscriber-test','hash','subscriber',1,1)`, `INSERT INTO subscriber_profiles (user_id,enabled,subscription_token,created_at,updated_at) VALUES (100,1,'fixture-token',1,1)`, `INSERT INTO subscriber_usage (user_id,cycle_started_at,updated_at) VALUES (100,1,1)`} {
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	f.call(t, "update_subscription_user", map[string]any{"id": 100, "changes": map[string]any{"plan_id": planID}}, true)
	const snapshot = `SELECT (SELECT count(*) FROM subscriber_clients), (SELECT desired_state_version FROM servers WHERE id=?)`
	var countBefore, versionBefore, countAfter, versionAfter int64
	if err := f.db.QueryRow(snapshot, f.serverID).Scan(&countBefore, &versionBefore); err != nil {
		t.Fatal(err)
	}
	source := "[custom]\ncustom_proxy_group=Group`select`[]DIRECT`(JP)\nruleset=Group,[]FINAL\n"
	out := f.call(t, "preview_routing_ini", map[string]any{"source": source, "subscriber_user_id": 100}, true)
	rendering := out["rendering"].(map[string]any)
	if rendering["mihomo"] != "success" || rendering["shadowrocket"] != "success" {
		t.Fatalf("shared rendering: %+v", rendering)
	}
	if err := f.db.QueryRow(snapshot, f.serverID).Scan(&countAfter, &versionAfter); err != nil || countAfter != countBefore || versionAfter != versionBefore {
		t.Fatal("shared preview changed client or desired state")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "fixture-token") || strings.Contains(string(raw), "node.example.com") {
		t.Fatal("shared preview exposed credentials or endpoint")
	}
}
