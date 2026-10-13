package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestServerNetworkMetadataAPI(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	created := createAccessTestServer(t, handler, accounts.memberCookie, "metadata", "private", nil)
	path := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	if created.Server.BandwidthMbps != nil || created.Server.NetworkStack != nil {
		t.Fatal("new metadata must be null")
	}
	for _, field := range []string{"bandwidth_mbps", "network_stack"} {
		for _, value := range []any{true, []any{1}, map[string]any{}, "unknown", ""} {
			response := performRequest(t, handler, http.MethodPatch, path, map[string]any{field: value}, accounts.memberCookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid %s type/value: %d", field, response.Code)
			}
		}
	}
	for _, value := range []any{0, -1, 10000001, 1.5, "500"} {
		for _, method := range []string{http.MethodPost, http.MethodPatch} {
			body := map[string]any{"bandwidth_mbps": value}
			target := path
			if method == http.MethodPost {
				body["name"] = "invalid"
				target = "/api/servers"
			}
			response := performRequest(t, handler, method, target, body, accounts.memberCookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid bandwidth %v: %d", value, response.Code)
			}
		}
	}
	for _, stack := range []any{nil, "ipv4", "ipv6", "dual"} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"bandwidth_mbps": 500, "network_stack": stack}, accounts.memberCookie)
		if response.Code != http.StatusOK {
			t.Fatalf("patch metadata: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			Server serverResponse `json:"server"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Server.BandwidthMbps == nil || *payload.Server.BandwidthMbps != 500 || payload.Server.DesiredStateVersion != created.Server.DesiredStateVersion {
			t.Fatalf("patch DTO: %v", err)
		}
		if stack == nil && payload.Server.NetworkStack != nil || stack != nil && (payload.Server.NetworkStack == nil || *payload.Server.NetworkStack != stack.(string)) {
			t.Fatal("network stack DTO mismatch")
		}
	}
	for _, target := range []string{path, "/api/servers"} {
		response := performRequest(t, handler, http.MethodGet, target, nil, accounts.memberCookie)
		var payload map[string]json.RawMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatalf("read metadata DTO: %d", response.Code)
		}
		var value serverResponse
		if target == path {
			if err := json.Unmarshal(payload["server"], &value); err != nil {
				t.Fatal(err)
			}
		} else {
			var values []serverResponse
			if err := json.Unmarshal(payload["servers"], &values); err != nil || len(values) != 1 {
				t.Fatalf("list DTO: %v", err)
			}
			value = values[0]
		}
		if value.BandwidthMbps == nil || *value.BandwidthMbps != 500 || value.NetworkStack == nil || *value.NetworkStack != "dual" {
			t.Fatal("Get/List lost metadata")
		}
	}
	cleared := performRequest(t, handler, http.MethodPatch, path, map[string]any{"bandwidth_mbps": nil}, accounts.memberCookie)
	var payload struct {
		Server map[string]any `json:"server"`
	}
	if cleared.Code != http.StatusOK || json.Unmarshal(cleared.Body.Bytes(), &payload) != nil || payload.Server["bandwidth_mbps"] != nil || payload.Server["network_stack"] != "dual" {
		t.Fatal("explicit null/omitted field contract broken")
	}
	for _, cookie := range []*http.Cookie{nil, accounts.adminCookie} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"network_stack": nil}, cookie)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusNotFound {
			t.Fatalf("unauthorized metadata update: %d", response.Code)
		}
	}
	var audits, operations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'server.update' AND resource_id = ?`, created.Server.ID).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("metadata audits: %d, %v", audits, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM configuration_operations WHERE server_id = ?`, created.Server.ID).Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("metadata runtime operations: %d, %v", operations, err)
	}
	for _, role := range []string{"carpool", "subscriber"} {
		if _, err := db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, accounts.memberID); err != nil {
			t.Fatal(err)
		}
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"network_stack": nil}, accounts.memberCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s metadata permission: %d", role, response.Code)
		}
	}
}

func TestCreateServerNetworkMetadataDTO(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	for _, fields := range []map[string]any{
		{"name": "unset", "bandwidth_mbps": nil, "network_stack": nil},
		{"name": "set", "bandwidth_mbps": 500, "network_stack": "dual"},
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/servers", fields, accounts.adminCookie)
		var payload struct {
			Server map[string]any `json:"server"`
		}
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatalf("create metadata DTO: %d", response.Code)
		}
		if value, exists := payload.Server["network_stack"]; !exists || value != fields["network_stack"] {
			t.Fatal("create stack DTO")
		}
		if value, exists := payload.Server["bandwidth_mbps"]; !exists || fields["bandwidth_mbps"] == nil && value != nil || fields["bandwidth_mbps"] != nil && value != float64(500) {
			t.Fatal("create bandwidth DTO")
		}
	}
	for _, value := range []any{"unknown", "", "ipv4_only", 1} {
		response := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]any{"name": "invalid", "network_stack": value}, accounts.adminCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid create stack: %d", response.Code)
		}
	}
}
