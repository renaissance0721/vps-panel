package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestClientTrafficConfigurationAndManualResetAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Traffic Server"}, cookie)
	var createdServer createdServerResponse
	if json.Unmarshal(serverCreation.Body.Bytes(), &createdServer) != nil {
		t.Fatal("decode created server")
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "Traffic", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Initial",
	}, cookie)
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &created) != nil {
		t.Fatalf("create proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := strconv.FormatInt(created.Proxy.ID, 10)

	createClient := func(name string, limit any, unit, mode string, weekday, day int, resetTime string) *httptest.ResponseRecorder {
		return performRequest(t, handler, http.MethodPost, "/api/proxies/"+proxyID+"/clients", map[string]any{
			"name": name, "enabled": true, "traffic_limit": limit, "limit_unit": unit,
			"traffic_reset_mode": mode, "traffic_reset_weekday": weekday,
			"traffic_reset_day": day, "traffic_reset_time": resetTime,
		}, cookie)
	}
	numberResponse := createClient("Number", 100, "G", "daily", 1, 1, "03:30")
	var numberClient struct {
		Client clientResponse `json:"client"`
	}
	if numberResponse.Code != http.StatusCreated || json.Unmarshal(numberResponse.Body.Bytes(), &numberClient) != nil ||
		numberClient.Client.TrafficLimitBytes == nil || *numberClient.Client.TrafficLimitBytes != int64(100)<<30 ||
		numberClient.Client.TrafficResetMode != "daily" || numberClient.Client.NextResetAt == nil {
		t.Fatalf("number traffic limit response = %d, %s", numberResponse.Code, numberResponse.Body.String())
	}
	stringResponse := createClient("String", "1.5", "T", "weekly", 7, 1, "00:00")
	var stringClient struct {
		Client clientResponse `json:"client"`
	}
	if stringResponse.Code != http.StatusCreated || json.Unmarshal(stringResponse.Body.Bytes(), &stringClient) != nil ||
		stringClient.Client.TrafficLimitBytes == nil || *stringClient.Client.TrafficLimitBytes != int64(3)<<39 ||
		stringClient.Client.TrafficResetMode != "weekly" || stringClient.Client.TrafficResetWeekday != 7 {
		t.Fatalf("string traffic limit response = %d, %s", stringResponse.Code, stringResponse.Body.String())
	}
	unlimitedResponse := createClient("Unlimited", "", "G", "never", 1, 1, "00:00")
	var unlimitedClient struct {
		Client clientResponse `json:"client"`
	}
	if unlimitedResponse.Code != http.StatusCreated || json.Unmarshal(unlimitedResponse.Body.Bytes(), &unlimitedClient) != nil ||
		unlimitedClient.Client.TrafficLimitBytes != nil || unlimitedClient.Client.NextResetAt != nil {
		t.Fatalf("unlimited response = %d, %s", unlimitedResponse.Code, unlimitedResponse.Body.String())
	}
	for _, invalid := range []map[string]any{
		{"name": "Bad unit", "traffic_limit": 1, "limit_unit": "M"},
		{"name": "Bad amount", "traffic_limit": "1e2", "limit_unit": "G"},
		{"name": "Bad day", "traffic_reset_mode": "monthly", "traffic_reset_day": 32, "traffic_reset_time": "00:00"},
		{"name": "Bad time", "traffic_reset_mode": "daily", "traffic_reset_time": "24:00"},
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/proxies/"+proxyID+"/clients", invalid, cookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid traffic config = %d, %s", response.Code, response.Body.String())
		}
	}

	clientID := numberClient.Client.ID
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes,
		 cycle_started_at, last_activity_at, updated_at) VALUES (?, 100, 200, 30, 40, 1, 2, 3)`, clientID); err != nil {
		t.Fatal(err)
	}
	reset := performRequest(t, handler, http.MethodPost, "/api/clients/"+strconv.FormatInt(clientID, 10)+"/traffic/reset", nil, cookie)
	if reset.Code != http.StatusOK {
		t.Fatalf("manual reset = %d, %s", reset.Code, reset.Body.String())
	}
	var uplink, downlink, cycleUplink, cycleDownlink int64
	if err := db.QueryRow(`SELECT xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes
		FROM client_metrics WHERE client_id = ?`, clientID).Scan(&uplink, &downlink, &cycleUplink, &cycleDownlink); err != nil {
		t.Fatal(err)
	}
	if uplink != 100 || downlink != 200 || cycleUplink != 0 || cycleDownlink != 0 {
		t.Fatalf("metrics after manual reset = %d/%d/%d/%d", uplink, downlink, cycleUplink, cycleDownlink)
	}
}

func TestClientExpirationAndDerivedLifecycleAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Lifecycle Server"}, cookie)
	var createdServer createdServerResponse
	if json.Unmarshal(serverCreation.Body.Bytes(), &createdServer) != nil {
		t.Fatal("decode created server")
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "Lifecycle", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Initial",
	}, cookie)
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &created) != nil {
		t.Fatalf("create proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := strconv.FormatInt(created.Proxy.ID, 10)
	creation := performRequest(t, handler, http.MethodPost, "/api/proxies/"+proxyID+"/clients", map[string]any{
		"name": "Expiring", "enabled": true, "expires_at": "2099-01-01T00:00",
		"traffic_limit": 1, "limit_unit": "G",
	}, cookie)
	var clientResult struct {
		Client clientResponse `json:"client"`
	}
	if creation.Code != http.StatusCreated || json.Unmarshal(creation.Body.Bytes(), &clientResult) != nil {
		t.Fatalf("create expiring client = %d, %s", creation.Code, creation.Body.String())
	}
	client := clientResult.Client
	if client.ExpiresAt == nil || client.ExpiresAt.Format(time.RFC3339) != "2098-12-31T16:00:00Z" ||
		client.Expired || client.QuotaExhausted || !client.EffectiveEnabled || client.Status != "normal" {
		t.Fatalf("created lifecycle = %+v", client)
	}
	clientPath := "/api/clients/" + strconv.FormatInt(client.ID, 10)
	past := performRequest(t, handler, http.MethodPatch, clientPath, map[string]any{"expires_at": "2000-01-01T00:00"}, cookie)
	if past.Code != http.StatusOK || json.Unmarshal(past.Body.Bytes(), &clientResult) != nil ||
		!clientResult.Client.Expired || clientResult.Client.EffectiveEnabled || clientResult.Client.Status != "expired" {
		t.Fatalf("past expiration = %d, %s", past.Code, past.Body.String())
	}
	clear := performRequest(t, handler, http.MethodPatch, clientPath, map[string]any{"expires_at": nil}, cookie)
	if clear.Code != http.StatusOK || json.Unmarshal(clear.Body.Bytes(), &clientResult) != nil ||
		clientResult.Client.ExpiresAt != nil || !clientResult.Client.EffectiveEnabled || clientResult.Client.Status != "normal" {
		t.Fatalf("clear expiration = %d, %s", clear.Code, clear.Body.String())
	}
	for _, value := range []any{"", "not-a-time", "2026-02-30T12:00", 123} {
		response := performRequest(t, handler, http.MethodPatch, clientPath, map[string]any{"expires_at": value}, cookie)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "到期时间") {
			t.Fatalf("invalid expiration %v = %d, %s", value, response.Code, response.Body.String())
		}
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes,
		 cycle_started_at, updated_at) VALUES (?, 0, 0, ?, 0, 1, 1)`, client.ID, int64(1)<<30); err != nil {
		t.Fatal(err)
	}
	exhausted := performRequest(t, handler, http.MethodGet, clientPath, nil, cookie)
	if exhausted.Code != http.StatusOK || json.Unmarshal(exhausted.Body.Bytes(), &clientResult) != nil ||
		!clientResult.Client.QuotaExhausted || clientResult.Client.EffectiveEnabled || clientResult.Client.Status != "exhausted" {
		t.Fatalf("quota-derived lifecycle = %d, %s", exhausted.Code, exhausted.Body.String())
	}
	if strings.Contains(exhausted.Body.String(), "private_key") || strings.Contains(exhausted.Body.String(), "reality_public_key") {
		t.Fatal("client lifecycle response leaked proxy key material")
	}
}
