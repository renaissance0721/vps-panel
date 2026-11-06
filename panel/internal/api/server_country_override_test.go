package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestServerCountryOverrideAPI(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	created := createAccessTestServer(t, handler, accounts.memberCookie, "region", "private", nil)
	path := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	if created.Server.CountryOverrideCode != nil {
		t.Fatal("new country override must be null")
	}
	for _, tc := range []struct{ input, want string }{{"JP", "JP"}, {"TW", "TW"}, {"HK", "HK"}, {"MO", "MO"}, {" tw ", "TW"}} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": tc.input}, accounts.memberCookie)
		var body struct {
			Server serverResponse `json:"server"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Server.CountryOverrideCode == nil || *body.Server.CountryOverrideCode != tc.want || body.Server.DesiredStateVersion != created.Server.DesiredStateVersion {
			t.Fatalf("patch %s: %d %s", tc.input, response.Code, response.Body.String())
		}
	}
	for _, target := range []string{path, "/api/servers"} {
		response := performRequest(t, handler, http.MethodGet, target, nil, accounts.memberCookie)
		if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) || !containsCountry(response.Body.Bytes(), "TW") {
			t.Fatalf("read country: %d %s", response.Code, response.Body.String())
		}
	}
	for _, invalid := range []any{"JAPAN", "123", "ABC", "ZZ", "ſg", true, []any{}} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": invalid}, accounts.memberCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %v: %d", invalid, response.Code)
		}
	}
	mixed := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": "JP", "name": "other"}, accounts.memberCookie)
	if mixed.Code != http.StatusBadRequest {
		t.Fatalf("mixed settings: %d", mixed.Code)
	}
	for _, value := range []any{nil, ""} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": value}, accounts.memberCookie)
		var body struct {
			Server serverResponse `json:"server"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Server.CountryOverrideCode != nil {
			t.Fatalf("clear: %d %s", response.Code, response.Body.String())
		}
	}
	for _, cookie := range []*http.Cookie{nil, accounts.adminCookie} {
		response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": "HK"}, cookie)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusNotFound {
			t.Fatalf("permission: %d", response.Code)
		}
	}
	if _, err := db.Exec(`UPDATE servers SET decommission_status = 'pending' WHERE id = ?`, created.Server.ID); err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": "HK"}, accounts.memberCookie)
	if response.Code != http.StatusConflict {
		t.Fatalf("decommission: %d", response.Code)
	}
	if _, err := db.Exec(`UPDATE servers SET decommission_status = '', archived_at = 123 WHERE id = ?`, created.Server.ID); err != nil {
		t.Fatal(err)
	}
	response = performRequest(t, handler, http.MethodPatch, path, map[string]any{"country_override_code": "HK"}, accounts.memberCookie)
	if response.Code != http.StatusNotFound {
		t.Fatalf("archived: %d", response.Code)
	}
}

func containsCountry(body []byte, code string) bool {
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	if raw, ok := v["server"]; ok {
		var s serverResponse
		return json.Unmarshal(raw, &s) == nil && s.CountryOverrideCode != nil && *s.CountryOverrideCode == code
	}
	var servers []serverResponse
	return json.Unmarshal(v["servers"], &servers) == nil && len(servers) == 1 && servers[0].CountryOverrideCode != nil && *servers[0].CountryOverrideCode == code
}
