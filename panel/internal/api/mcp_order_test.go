package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAllOrdersMatchWebAndRespectPermissions(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	var owner, other, routing int64
	if err := f.db.QueryRow(`SELECT owner_user_id FROM mcp_tokens WHERE id=?`, f.tokenID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT id FROM users WHERE id!=? LIMIT 1`, owner).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT id FROM subscription_routing_presets WHERE is_default=1`).Scan(&routing); err != nil {
		t.Fatal(err)
	}
	fixture := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "fixture", "listen_port": 8999, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "client"}, true)
	fixtureID := objectID(t, fixture, "proxy")
	var config string
	if err := f.db.QueryRow(`SELECT config_json FROM proxies WHERE id=?`, fixtureID).Scan(&config); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO proxies(id,server_id,name,protocol,listen_port,enabled,config_json,created_at,updated_at) VALUES (101,?,'P1','vless',8001,1,?,1,1),(102,?,'P2','vless',8002,1,?,2,2),(103,?,'hidden','vless',8003,1,?,3,3)`, []any{f.serverID, config, f.serverID, config, f.hiddenID, config}},
		{`INSERT INTO landing_nodes(id,owner_user_id,name,visibility,protocol,host,port,uri,created_at,updated_at) VALUES (101,?,'L1','private','vless','example.test',443,'dummy-secret-one',1,1),(102,?,'L2','public','vless','example.test',443,'dummy-secret-two',2,2),(103,?,'hidden','private','vless','example.test',443,'dummy-secret-three',3,3)`, []any{owner, owner, other}},
		{`INSERT INTO relays(id,server_id,name,listen_port,target_type,target_host,target_port,network,created_at,updated_at) VALUES (101,?,'R1',9001,'manual','example.test',443,'tcp',1,1),(102,?,'R2',9002,'manual','example.test',443,'tcp',2,2),(103,?,'hidden',9003,'manual','example.test',443,'tcp',3,3)`, []any{f.serverID, f.serverID, f.hiddenID}},
		{`INSERT INTO relays(id,server_id,name,listen_port,target_type,target_landing_id,network,created_at,updated_at) VALUES (104,?,'private target',9004,'landing',103,'tcp',4,4)`, []any{f.serverID}},
		{`INSERT INTO personal_subscription_groups(id,owner_user_id,name,token,enabled,client_name,routing_preset_id,created_at,updated_at) VALUES (101,?,'S1','dummy-token-one',1,'admin',?,1,1),(102,?,'S2','dummy-token-two',1,'admin',?,2,2),(103,?,'hidden','dummy-token-three',1,'other',?,3,3)`, []any{owner, routing, owner, routing, other, routing}},
		{`INSERT INTO subscription_published_nodes(id,name,mode,target_proxy_id,enabled,created_at,updated_at) VALUES (101,'N1','direct',101,1,1,1),(102,'N2','direct',102,1,2,2)`, nil},
	} {
		if _, err := f.db.Exec(row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	read := phase1Session(t, f.handler, f.cookie)
	tests := []struct {
		list, write, field, route, key string
		hidden                         int64
	}{
		{"list_proxies", "set_proxy_order", "proxy_ids", "/api/proxies", "proxies", 103},
		{"list_relays", "set_relay_order", "relay_ids", "/api/relays", "relays", 104},
		{"list_landings", "set_landing_order", "landing_ids", "/api/landings", "landings", 103},
		{"list_users", "set_user_order", "user_ids", "/api/users", "users", 0},
		{"list_personal_subscriptions", "set_personal_subscription_order", "subscription_ids", "/api/personal-subscriptions", "personal_subscriptions", 103},
		{"list_published_nodes", "set_published_node_order", "node_ids", "/api/admin/subscription/nodes", "nodes", 0},
	}
	for _, tc := range tests {
		t.Run(tc.write, func(t *testing.T) {
			result, page := phase1Call(t, f.session, tc.list, map[string]any{})
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "dummy-secret") || strings.Contains(string(encoded), "dummy-token") || strings.Contains(string(encoded), "password_hash") {
				t.Fatal("sensitive content leaked")
			}
			if len(page.Items) < 2 {
				t.Fatalf("not enough fixtures: %s", tc.list)
			}
			ids := make([]int64, len(page.Items))
			for i, v := range page.Items {
				ids[i] = int64(v["id"].(float64))
			}
			for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
				ids[i], ids[j] = ids[j], ids[i]
			}
			args := map[string]any{tc.field: ids}
			r, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.write, Arguments: args})
			if err != nil || !r.IsError {
				t.Fatal("read token wrote order")
			}
			f.call(t, tc.write, map[string]any{tc.field: ids, "dry_run": true}, true)
			_, unchanged := phase1Call(t, f.session, tc.list, map[string]any{})
			if reflect.DeepEqual(unchanged.Items, page.Items) == false {
				t.Fatal("dry run changed order")
			}
			f.call(t, tc.write, map[string]any{tc.field: ids[:len(ids)-1]}, false)
			duplicate := append(append([]int64{}, ids...), ids[0])
			f.call(t, tc.write, map[string]any{tc.field: duplicate}, false)
			if tc.hidden != 0 {
				bad := append([]int64{}, ids...)
				bad[0] = tc.hidden
				f.call(t, tc.write, map[string]any{tc.field: bad}, false)
			}
			f.call(t, tc.write, args, true)
			if tc.list == "list_personal_subscriptions" {
				_, combined := phase1Call(t, f.session, "list_subscriptions", map[string]any{})
				for i, id := range ids {
					if int64(combined.Items[i]["id"].(float64)) != id {
						t.Fatal("combined subscription order mismatch")
					}
				}
			}
			for offset, id := range ids {
				_, part := phase1Call(t, f.session, tc.list, map[string]any{"limit": 1, "offset": offset})
				if part.Total != len(ids) || len(part.Items) != 1 || int64(part.Items[0]["id"].(float64)) != id {
					t.Fatalf("page %d mismatch", offset)
				}
			}
			web := performRequest(t, f.handler, "GET", tc.route, nil, f.cookie)
			if web.Code != 200 {
				t.Fatalf("web: %d", web.Code)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(web.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			var items []struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(payload[tc.key], &items); err != nil {
				t.Fatal(err)
			}
			actual := make([]int64, 0, len(items))
			for _, item := range items {
				actual = append(actual, item.ID)
			}
			if !reflect.DeepEqual(actual, ids) {
				t.Fatal(fmt.Sprintf("web/MCP mismatch: %v vs %v", actual, ids))
			}
		})
	}
}
