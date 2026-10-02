package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationSettingsValidationMaskingAndAsyncTest(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	response := performRequest(t, handler, "GET", "/api/notifications/settings", nil, cookie)
	var settings map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &settings) != nil {
		t.Fatal(response.Body.String())
	}
	if settings["telegram_token_set"] != false || settings["offline_grace_minutes"] != float64(3) || settings["traffic_threshold_percent"] != float64(80) || settings["traffic_step_percent"] != float64(10) {
		t.Fatal(settings)
	}
	input := map[string]any{"telegram_bot_token": "123:SECRET_TOKEN", "telegram_chat_id": "-100123456"}
	for i := 0; i < 3; i++ {
		if i == 1 {
			input["telegram_bot_token"] = ""
		}
		if i == 2 {
			delete(input, "telegram_bot_token")
		}
		response = performRequest(t, handler, "PUT", "/api/notifications/settings", input, cookie)
		if response.Code != 200 || strings.Contains(response.Body.String(), "SECRET") || strings.Contains(response.Body.String(), "telegram_bot_token") {
			t.Fatalf("unsafe settings response=%d %s", response.Code, response.Body.String())
		}
		var stored string
		if err := db.QueryRow(`SELECT telegram_bot_token FROM notification_settings`).Scan(&stored); err != nil || stored != "123:SECRET_TOKEN" {
			t.Fatalf("empty token not preserved: %v", err)
		}
	}
	for _, invalid := range []map[string]any{
		{"telegram_bot_token": "invalid"}, {"telegram_bot_token": "123:bad\ntoken"}, {"telegram_bot_token": strings.Repeat("1", 257) + ":x"},
		{"telegram_chat_id": "@username"}, {"telegram_chat_id": "123\n"}, {"offline_grace_minutes": 0}, {"offline_grace_minutes": 61},
		{"traffic_threshold_percent": 49}, {"traffic_threshold_percent": 101}, {"traffic_step_percent": 0}, {"traffic_step_percent": 15},
		{"clear_telegram_token": true, "telegram_bot_token": "123:NEW"},
	} {
		response = performRequest(t, handler, "PUT", "/api/notifications/settings", invalid, cookie)
		if response.Code != 400 || strings.Contains(response.Body.String(), "SECRET") {
			t.Fatalf("validation=%d %s", response.Code, response.Body.String())
		}
	}
	// No worker is running in this handler. The API must return immediately
	// after enqueue, not claim delivery success or perform network I/O.
	response = performRequest(t, handler, "POST", "/api/notifications/test", map[string]string{"telegram_chat_id": "-100999"}, cookie)
	var result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Status != "pending" || result.ID == "" {
		t.Fatalf("test=%d %s", response.Code, response.Body.String())
	}
	response = performRequest(t, handler, "GET", "/api/notifications/test/"+result.ID, nil, cookie)
	if response.Code != 200 || strings.Contains(response.Body.String(), "SECRET") {
		t.Fatal(response.Body.String())
	}
	response = performRequest(t, handler, "PUT", "/api/notifications/settings", map[string]bool{"clear_telegram_token": true}, cookie)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"telegram_token_set":false`) {
		t.Fatal(response.Body.String())
	}
}

func TestNotificationAllEndpointsRequireAdmin(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	for _, role := range []string{"vip", "user", "subscriber", "anonymous"} {
		storedRole := role
		if storedRole == "anonymous" {
			storedRole = "user"
		}
		if _, err := db.Exec(`UPDATE users SET role=? WHERE username='admin'`, storedRole); err != nil {
			t.Fatal(err)
		}
		requestCookie := cookie
		want := 403
		if role == "anonymous" {
			requestCookie = nil
			want = 401
		}
		for _, route := range []struct{ method, path string }{{"GET", "/api/notifications/settings"}, {"PUT", "/api/notifications/settings"}, {"POST", "/api/notifications/test"}, {"GET", "/api/notifications/test/example"}} {
			response := performRequest(t, handler, route.method, route.path, map[string]any{}, requestCookie)
			if response.Code != want {
				t.Fatalf("role=%s route=%s status=%d", role, route.path, response.Code)
			}
		}
	}
}
