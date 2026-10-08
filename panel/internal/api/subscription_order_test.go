package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func createPersonalSubscriptionForOrder(t *testing.T, handler http.Handler, cookie *http.Cookie, name string) int64 {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/personal-subscriptions", map[string]any{
		"name": name, "client_name": name, "enabled": true,
	}, cookie)
	var payload struct {
		Personal personalSubscriptionResponse `json:"personal_subscription"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatalf("create personal subscription %s = %d, %s", name, response.Code, response.Body.String())
	}
	return payload.Personal.ID
}

func moveSubscriptionOrderAPI(t *testing.T, handler http.Handler, cookie *http.Cookie, path, direction string, status int) {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, path, map[string]string{"direction": direction}, cookie)
	if response.Code != status {
		t.Fatalf("%s %s = %d, want %d: %s", path, direction, response.Code, status, response.Body.String())
	}
}

func TestSubscriptionOrderAPIPersistsAndEnforcesScope(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	vipCookie, vipID := registerAccount(t, db, handler, adminCookie, "vip", "vip-order")
	carpoolCookie, _ := registerAccount(t, db, handler, adminCookie, "carpool", "carpool-order")

	adminFirst := createPersonalSubscriptionForOrder(t, handler, adminCookie, "admin-first")
	adminSecond := createPersonalSubscriptionForOrder(t, handler, adminCookie, "admin-second")
	vipFirst := createPersonalSubscriptionForOrder(t, handler, vipCookie, "vip-first")
	vipSecond := createPersonalSubscriptionForOrder(t, handler, vipCookie, "vip-second")
	expectOrderAPI(t, handler, adminCookie, "/api/personal-subscriptions", "personal_subscriptions", adminSecond, adminFirst)
	expectOrderAPI(t, handler, vipCookie, "/api/personal-subscriptions", "personal_subscriptions", vipSecond, vipFirst)

	moveSubscriptionOrderAPI(t, handler, adminCookie,
		"/api/personal-subscriptions/"+strconv.FormatInt(adminFirst, 10)+"/reorder", "up", http.StatusNoContent)
	expectOrderAPI(t, handler, adminCookie, "/api/personal-subscriptions", "personal_subscriptions", adminFirst, adminSecond)
	expectOrderAPI(t, handler, vipCookie, "/api/personal-subscriptions", "personal_subscriptions", vipSecond, vipFirst)
	moveSubscriptionOrderAPI(t, handler, adminCookie,
		"/api/personal-subscriptions/"+strconv.FormatInt(vipFirst, 10)+"/reorder", "up", http.StatusNotFound)
	moveSubscriptionOrderAPI(t, handler, carpoolCookie,
		"/api/personal-subscriptions/"+strconv.FormatInt(adminFirst, 10)+"/reorder", "up", http.StatusForbidden)

	if _, err := db.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, vipID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
		 VALUES (100, 'Order Server', 'admin', 'offline', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (100, 100, 'Order Proxy', 'vless', 8443, '{}', 1, 1)`,
		`INSERT INTO subscription_published_nodes
		 (id, name, mode, target_proxy_id, enabled, created_at, updated_at)
		 VALUES (100, 'published-first', 'direct', 100, 1, 1, 1),
		        (101, 'published-second', 'direct', 100, 1, 2, 2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	expectOrderAPI(t, handler, adminCookie, "/api/admin/subscription/nodes", "nodes", 101, 100)
	expectOrderAPI(t, handler, vipCookie, "/api/admin/subscription/nodes", "nodes", 101, 100)
	moveSubscriptionOrderAPI(t, handler, adminCookie,
		"/api/admin/subscription/nodes/100/reorder", "up", http.StatusNoContent)
	expectOrderAPI(t, handler, adminCookie, "/api/admin/subscription/nodes", "nodes", 100, 101)
	expectOrderAPI(t, handler, vipCookie, "/api/admin/subscription/nodes", "nodes", 101, 100)
	moveSubscriptionOrderAPI(t, handler, vipCookie,
		"/api/admin/subscription/nodes/101/reorder", "sideways", http.StatusBadRequest)
	moveSubscriptionOrderAPI(t, handler, carpoolCookie,
		"/api/admin/subscription/nodes/101/reorder", "up", http.StatusForbidden)
}
