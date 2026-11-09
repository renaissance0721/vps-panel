package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPPersonalSubscriptionCRUDAndSecrets(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	other := performRequest(t, f.handler, "POST", "/api/personal-subscriptions", map[string]any{"name": "Other owner", "client_name": "client"}, f.memberCookie)
	var otherPayload struct {
		Personal struct {
			ID int64 `json:"id"`
		} `json:"personal_subscription"`
	}
	if other.Code != 201 || json.Unmarshal(other.Body.Bytes(), &otherPayload) != nil {
		t.Fatal("other owner fixture failed")
	}
	f.call(t, "get_personal_subscription", map[string]any{"id": otherPayload.Personal.ID}, false)
	f.call(t, "update_personal_subscription", map[string]any{"id": otherPayload.Personal.ID, "changes": map[string]any{"name": "not allowed"}}, false)
	read := phase1Session(t, f.handler, f.cookie)
	args := map[string]any{"name": "Personal MCP", "client_name": "client", "enabled": true}
	denied, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_personal_subscription", Arguments: args})
	if err != nil || !denied.IsError {
		t.Fatal("read token created subscription")
	}
	created := f.call(t, "create_personal_subscription", args, true)
	id := objectID(t, created, "personal_subscription")
	var token string
	if err := f.db.QueryRow(`SELECT token FROM personal_subscription_groups WHERE id=?`, id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	for _, out := range []map[string]any{created, f.call(t, "get_personal_subscription", map[string]any{"id": id}, true)} {
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), token) || strings.Contains(string(raw), "subscription_mihomo_url") {
			t.Fatal("ordinary response leaked subscription credential")
		}
	}
	updated := f.call(t, "update_personal_subscription", map[string]any{"id": id, "changes": map[string]any{"name": "Changed MCP", "mihomo_template_id": nil}}, true)
	if updated["personal_subscription"].(map[string]any)["name"] != "Changed MCP" {
		t.Fatal("update failed")
	}
	var after string
	if err := f.db.QueryRow(`SELECT token FROM personal_subscription_groups WHERE id=?`, id).Scan(&after); err != nil || after != token {
		t.Fatal("ordinary edit changed token")
	}
	f.call(t, "set_personal_subscription_nodes", map[string]any{"id": id, "nodes": []any{}}, true)
	f.call(t, "list_personal_subscription_sources", map[string]any{"client_name": "client"}, true)
	regenerated := f.call(t, "regenerate_personal_subscription_token", map[string]any{"id": id, "expected_name": "Changed MCP"}, true)
	if err := f.db.QueryRow(`SELECT token FROM personal_subscription_groups WHERE id=?`, id).Scan(&after); err != nil || after == token {
		t.Fatal("token was not regenerated")
	}
	if regenerated["subscription_token"] != after {
		t.Fatal("regenerated token missing from explicit response")
	}
	f.call(t, "delete_personal_subscription", map[string]any{"id": id, "expected_name": "Changed MCP"}, true)
}

func TestMCPSharedPlanLifecycle(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	created := f.call(t, "create_subscription_plan", map[string]any{"name": "Shared MCP"}, true)
	id := objectID(t, created, "plan")
	f.call(t, "get_subscription_plan", map[string]any{"id": id}, true)
	f.call(t, "list_subscription_plans", map[string]any{"limit": 1}, true)
	updated := f.call(t, "update_subscription_plan", map[string]any{"id": id, "changes": map[string]any{"name": "Changed Shared", "traffic_limit_bytes": nil}}, true)
	if updated["plan"].(map[string]any)["name"] != "Changed Shared" {
		t.Fatal("plan update failed")
	}
	f.call(t, "set_subscription_plan_nodes", map[string]any{"id": id, "node_ids": []any{}}, true)
	f.call(t, "set_subscription_plan_routing_bindings", map[string]any{"id": id, "routing_bindings": map[string]any{}}, true)
	f.call(t, "delete_subscription_plan", map[string]any{"id": id, "expected_name": "Changed Shared"}, true)
}

func TestMCPRoutingAndTemplateLifecycle(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	created := f.call(t, "create_routing_preset", map[string]any{"name": "MCP routing", "groups": []any{map[string]any{"name": "Custom", "type": "select", "proxies": []string{"DIRECT"}}}, "rule_providers": []any{}, "rules": []string{"MATCH,Custom"}}, true)
	id := objectID(t, created, "routing_preset")
	f.call(t, "list_routing_presets", map[string]any{}, true)
	before := f.call(t, "get_routing_preset", map[string]any{"id": id}, true)
	updated := f.call(t, "update_routing_preset", map[string]any{"id": id, "changes": map[string]any{"name": "Renamed routing"}}, true)
	if len(updated["routing_preset"].(map[string]any)["groups"].([]any)) != len(before["routing_preset"].(map[string]any)["groups"].([]any)) {
		t.Fatal("routing groups lost")
	}
	f.call(t, "delete_routing_preset", map[string]any{"id": id, "expected_name": "incorrect"}, false)
	f.call(t, "delete_routing_preset", map[string]any{"id": id, "expected_name": "Renamed routing"}, true)
	tpl := f.call(t, "create_subscription_template", map[string]any{"type": "mihomo", "name": "MCP template", "content": "dns:\n  enable: false"}, true)
	tid := objectID(t, tpl, "template")
	f.call(t, "get_subscription_template", map[string]any{"id": tid}, true)
	list := f.call(t, "list_subscription_templates", map[string]any{}, true)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "enable: false") {
		t.Fatal("template content leaked in list")
	}
	effective := f.call(t, "get_effective_mihomo_configuration", map[string]any{"template_id": tid}, true)
	if !strings.Contains(effective["yaml"].(string), "enable: false") {
		t.Fatal("template not rendered")
	}
	f.call(t, "get_builtin_mihomo_configuration", map[string]any{}, true)
	f.call(t, "get_builtin_shadowrocket_configuration", map[string]any{}, true)
	f.call(t, "update_subscription_template", map[string]any{"id": tid, "changes": map[string]any{"name": "Changed template"}}, true)
	f.call(t, "create_subscription_template", map[string]any{"type": "unsupported", "name": "bad", "content": "x"}, false)
	f.call(t, "delete_subscription_template", map[string]any{"id": tid, "expected_name": "Changed template"}, true)
}

func TestMCPPublishedNodeAndSubscriber(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	proxy := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "P1A proxy", "listen_port": 8999, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "client"}, true)
	pid := objectID(t, proxy, "proxy")
	node := f.call(t, "create_published_node", map[string]any{"name": "Published", "mode": "direct", "target_proxy_id": pid}, true)
	nid := objectID(t, node, "node")
	f.call(t, "get_published_node", map[string]any{"id": nid}, true)
	f.call(t, "update_published_node", map[string]any{"id": nid, "changes": map[string]any{"name": "Published changed"}}, true)
	f.call(t, "list_subscription_relay_servers", map[string]any{}, true)
	for _, query := range []string{`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (100,'subscriber-test','hash','subscriber',1,1)`, `INSERT INTO subscriber_profiles (user_id,enabled,subscription_token,created_at,updated_at) VALUES (100,1,'fixture-token',1,1)`, `INSERT INTO subscriber_usage (user_id,cycle_started_at,updated_at) VALUES (100,1,1)`} {
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	plan := f.call(t, "create_subscription_plan", map[string]any{"name": "Subscriber plan"}, true)
	planID := objectID(t, plan, "plan")
	f.call(t, "set_subscription_plan_nodes", map[string]any{"id": planID, "node_ids": []int64{nid}}, true)
	f.call(t, "update_subscription_user", map[string]any{"id": 100, "changes": map[string]any{"plan_id": planID}}, true)
	preview := f.call(t, "preview_subscription_user_mihomo", map[string]any{"id": 100}, true)
	if !strings.Contains(preview["yaml"].(string), "Published changed") || preview["agent_sync"] != "not_checked" {
		t.Fatal("subscriber preview or sync status incorrect")
	}
	for _, name := range []string{"list_subscription_users", "get_subscription_user"} {
		args := map[string]any{}
		if name == "get_subscription_user" {
			args["id"] = 100
		}
		out := f.call(t, name, args, true)
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "fixture-token") {
			t.Fatal("subscription token leaked")
		}
	}
	f.call(t, "update_subscription_user", map[string]any{"id": 100, "changes": map[string]any{"enabled": false}}, true)
	var token string
	if err := f.db.QueryRow(`SELECT subscription_token FROM subscriber_profiles WHERE user_id=100`).Scan(&token); err != nil || token != "fixture-token" {
		t.Fatal("ordinary subscriber update regenerated token")
	}
	f.call(t, "reset_subscription_user_traffic", map[string]any{"id": 100, "expected_name": "subscriber-test"}, true)
	f.call(t, "regenerate_subscription_user_token", map[string]any{"id": 100, "expected_name": "subscriber-test"}, true)
	if err := f.db.QueryRow(`SELECT subscription_token FROM subscriber_profiles WHERE user_id=100`).Scan(&token); err != nil || token == "fixture-token" {
		t.Fatal("subscriber token not regenerated")
	}
	f.call(t, "set_subscription_plan_nodes", map[string]any{"id": planID, "node_ids": []int64{}}, true)
	f.call(t, "delete_published_node", map[string]any{"id": nid, "expected_name": "Published changed"}, true)
}

func TestMCPSubscriptionAccessAndWriteSwitch(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	proxy := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "ACL proxy", "listen_port": 8999, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "client"}, true)
	pid := objectID(t, proxy, "proxy")
	if _, err := f.db.Exec(`INSERT INTO proxies(id,server_id,name,protocol,listen_port,enabled,config_json,created_at,updated_at) SELECT 500,?,'Hidden proxy',protocol,8998,enabled,config_json,created_at,updated_at FROM proxies WHERE id=?`, f.hiddenID, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO subscription_published_nodes(id,name,mode,target_proxy_id,enabled,created_at,updated_at) VALUES(500,'Hidden node','direct',500,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO subscription_published_nodes(id,name,mode,target_proxy_id,enabled,created_at,updated_at) VALUES(501,'Visible one','direct',?,1,2,2),(502,'Visible two','direct',?,1,3,3)`, pid, pid); err != nil {
		t.Fatal(err)
	}
	var owner int64
	if err := f.db.QueryRow(`SELECT owner_user_id FROM mcp_tokens WHERE id=?`, f.tokenID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO user_published_node_order(user_id,published_node_id,position) VALUES(?,501,1),(?,500,2),(?,502,3)`, owner, owner, owner); err != nil {
		t.Fatal(err)
	}
	plan := f.call(t, "create_subscription_plan", map[string]any{"name": "Hidden plan"}, true)
	id := objectID(t, plan, "plan")
	f.call(t, "set_subscription_plan_nodes", map[string]any{"id": id, "node_ids": []int64{500}}, false)
	if _, err := f.db.Exec(`INSERT INTO subscription_plan_nodes(plan_id,published_node_id,position) VALUES(?,500,1)`, id); err != nil {
		t.Fatal(err)
	}
	f.call(t, "get_published_node", map[string]any{"id": 500}, false)
	nodes := f.call(t, "list_published_nodes", map[string]any{}, true)
	encodedNodes, _ := json.Marshal(nodes)
	if strings.Contains(string(encodedNodes), "Hidden node") {
		t.Fatal("inaccessible node leaked")
	}
	f.call(t, "set_published_node_order", map[string]any{"node_ids": []int64{500}}, false)
	f.call(t, "set_published_node_order", map[string]any{"node_ids": []int64{502, 501}}, true)
	var order string
	if err := f.db.QueryRow(`SELECT group_concat(published_node_id, ',') FROM (SELECT published_node_id FROM user_published_node_order WHERE user_id=? ORDER BY position)`, owner).Scan(&order); err != nil || order != "502,500,501" {
		t.Fatalf("inaccessible order preference not retained: %s", order)
	}
	f.call(t, "get_subscription_plan", map[string]any{"id": id}, false)
	f.call(t, "update_subscription_plan", map[string]any{"id": id, "changes": map[string]any{"name": "not allowed"}}, false)
	list := f.call(t, "list_subscription_plans", map[string]any{}, true)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "Hidden plan") {
		t.Fatal("inaccessible plan leaked")
	}
	if w := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true, "mcp_write_enabled": false}, f.cookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.call(t, "create_subscription_plan", map[string]any{"name": "disabled"}, false)
	f.call(t, "list_subscription_plans", map[string]any{}, true)
}

func TestMCPPersonalSubscriptionActualPreview(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	landing := f.call(t, "create_landing", map[string]any{"name": "Landing", "visibility": "private", "uri": "vless://00000000-0000-0000-0000-000000000001@landing.example.com:443?type=tcp&security=tls"}, true)
	lid := objectID(t, landing, "landing")
	sub := f.call(t, "create_personal_subscription", map[string]any{"name": "Preview test", "client_name": "client"}, true)
	id := objectID(t, sub, "personal_subscription")
	f.call(t, "set_personal_subscription_nodes", map[string]any{"id": id, "nodes": []any{map[string]any{"source_type": "landing", "source_id": lid, "display_name": "Edited display", "enabled": true, "entry_host": "override.example.com", "entry_port": 8443}}}, true)
	for _, name := range []string{"preview_personal_subscription_mihomo", "preview_personal_subscription_shadowrocket"} {
		out := f.call(t, name, map[string]any{"id": id}, true)
		raw, _ := json.Marshal(out)
		if !strings.Contains(string(raw), "override.example.com") || !strings.Contains(string(raw), "Edited display") {
			t.Fatalf("%s did not render selected node", name)
		}
	}
	read := phase1Session(t, f.handler, f.cookie)
	result, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "preview_personal_subscription_mihomo", Arguments: map[string]any{"id": id}})
	if err != nil || !result.IsError {
		t.Fatal("read token accessed sensitive preview")
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM mcp_audit_logs WHERE tool_name IN ('preview_personal_subscription_mihomo','preview_personal_subscription_shadowrocket')`).Scan(&count); err != nil || count < 2 {
		t.Fatal("preview audit missing")
	}
}
