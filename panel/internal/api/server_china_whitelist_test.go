package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestChinaWhitelistAuthorizationPersistenceAndAgentCompatibility(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	first, err := servers.Create(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := servers.Create(t.Context(), "second")
	if err != nil {
		t.Fatal(err)
	}
	agents := agentcontrol.NewService(db, time.Now)
	agent, err := agents.RegisterAgentWithMetadata(t.Context(), first.EnrollmentToken, agentcontrol.Metadata{Implementation: "test", Version: "1", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilityFirewallCNBlock, agentcontrol.CapabilityFirewallCNWhitelist}}, false)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	admin := initializeChinaFirewallAdmin(t, handler)
	vip, _ := registerAccount(t, db, handler, admin, "vip", "whitelist-vip")
	path := fmt.Sprintf("/api/servers/%d/china-inbound-whitelist", first.ID)
	entries := []map[string]string{{"address": "203.0.113.10", "remark": "relay"}, {"address": "2001:db8::ab/64", "remark": "v6"}}
	for _, tc := range []struct {
		cookie *http.Cookie
		status int
	}{{nil, http.StatusUnauthorized}, {vip, http.StatusForbidden}} {
		res := performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": entries}, tc.cookie)
		if res.Code != tc.status {
			t.Fatalf("unauthorized request = %d", res.Code)
		}
	}
	res := performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": entries}, admin)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "203.0.113.10/32") || !strings.Contains(res.Body.String(), "2001:db8::/64") {
		t.Fatalf("save = %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Server  serverResponse                    `json:"server"`
		Entries []serverstore.ChinaWhitelistEntry `json:"entries"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Server.DesiredStateVersion != 2 || !body.Server.ChinaInboundWhitelistConfigured {
		t.Fatalf("response = %s", res.Body.String())
	}
	res = performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": body.Entries}, admin)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"desired_state_version":2`) {
		t.Fatalf("no-op = %d %s", res.Code, res.Body.String())
	}
	for _, invalid := range [][]map[string]string{
		{{"address": "0.0.0.0/0"}}, {{"address": "::/0"}}, {{"address": "192.0.2.2/33"}},
		{{"address": "203.0.113.10"}, {"address": "203.0.113.10/32"}},
	} {
		res = performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": invalid}, admin)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("invalid accepted: %s", res.Body.String())
		}
	}
	tooMany := make([]map[string]string, 129)
	for i := range tooMany {
		tooMany[i] = map[string]string{"address": fmt.Sprintf("198.51.100.%d", i)}
	}
	if res := performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": tooMany}, admin); res.Code != http.StatusBadRequest {
		t.Fatalf("entry limit = %d", res.Code)
	}
	other, err := servers.ChinaWhitelist(t.Context(), second.ID)
	if err != nil || len(other) != 0 {
		t.Fatalf("server isolation: %v %v", other, err)
	}
	res = performRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/servers/%d", first.ID), map[string]any{"block_china_inbound": true}, admin)
	if res.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", res.Code, res.Body.String())
	}
	config := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, agent.Token)
	var desired agentDesiredStateResponse
	if config.Code != http.StatusOK || json.Unmarshal(config.Body.Bytes(), &desired) != nil || len(desired.ChinaInboundWhitelist) != 2 || desired.Version != 3 {
		t.Fatalf("desired: %d %s", config.Code, config.Body.String())
	}
	if _, err := db.Exec(`UPDATE agents SET capabilities_json = '["firewall.cn_block"]' WHERE id = ?`, agent.ID); err != nil {
		t.Fatal(err)
	}
	config = performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, agent.Token)
	if config.Code == http.StatusOK {
		t.Fatal("old Agent received active whitelist configuration")
	}
	if err := agents.RecordConfigResult(t.Context(), agent.ID, first.ID, agentcontrol.ConfigResult{Version: 3, Status: agentcontrol.ConfigSyncSuccess}); err != nil {
		t.Fatal(err)
	}
	var status string
	var applied int64
	if err := db.QueryRow(`SELECT config_sync_status, applied_config_version FROM agents WHERE id = ?`, agent.ID).Scan(&status, &applied); err != nil || status != agentcontrol.ConfigSyncFailed || applied >= 3 {
		t.Fatalf("old Agent result: %s %d %v", status, applied, err)
	}
	res = performRequest(t, handler, http.MethodPatch, fmt.Sprintf("/api/servers/%d", first.ID), map[string]any{"block_china_inbound": false}, admin)
	if res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	stored, err := servers.ChinaWhitelist(t.Context(), first.ID)
	if err != nil || len(stored) != 2 {
		t.Fatal("disabling removed whitelist")
	}
	stored[0] = serverstore.ChinaWhitelistEntry{Address: "203.0.113.11", Remark: "edited"}
	res = performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": stored}, admin)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"address":"203.0.113.11/32"`) || !strings.Contains(res.Body.String(), `"remark":"edited"`) {
		t.Fatalf("edit = %d %s", res.Code, res.Body.String())
	}
	res = performRequest(t, handler, http.MethodPut, path, map[string]any{"entries": []serverstore.ChinaWhitelistEntry{}}, admin)
	if res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	stored, _ = servers.ChinaWhitelist(t.Context(), first.ID)
	if len(stored) != 0 {
		t.Fatal("deletion failed")
	}
}
