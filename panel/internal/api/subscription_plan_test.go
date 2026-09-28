package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestSubscriptionPlanTitleAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	adminCookie := initialized.Result().Cookies()[0]
	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
		"name": "50G 月付套餐", "subscription_title": " Refrain Cloud ", "enabled": true,
	}, adminCookie)
	var payload struct {
		Plan subscriptionPlanResponse `json:"plan"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &payload) != nil ||
		payload.Plan.Name != "50G 月付套餐" || payload.Plan.SubscriptionTitle != "Refrain Cloud" {
		t.Fatalf("create plan title = %d %s", created.Code, created.Body.String())
	}
	invalid := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"subscription_title": strings.Repeat("长", 101)}, adminCookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid plan title = %d %s", invalid.Code, invalid.Body.String())
	}
	cleared := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"subscription_title": ""}, adminCookie)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"subscription_title":""`) {
		t.Fatalf("clear plan title = %d %s", cleared.Code, cleared.Body.String())
	}
	for _, field := range []string{
		"traffic_reset_mode", "traffic_reset_day", "traffic_reset_time", "default_validity_days", "billing_period_months",
	} {
		legacy := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
			"name": "Legacy", field: 1,
		}, adminCookie)
		if legacy.Code != http.StatusBadRequest {
			t.Fatalf("legacy plan field %s accepted = %d %s", field, legacy.Code, legacy.Body.String())
		}
	}
}
