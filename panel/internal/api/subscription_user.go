package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type updateSubscriptionUserRequest struct {
	PlanID              json.RawMessage `json:"plan_id"`
	Enabled             *bool           `json:"enabled"`
	ExpiresAt           json.RawMessage `json:"expires_at"`
	TrafficResetMode    *string         `json:"traffic_reset_mode"`
	TrafficResetDay     *int            `json:"traffic_reset_day"`
	TrafficResetTime    *string         `json:"traffic_reset_time"`
	BillingPeriodMonths json.RawMessage `json:"billing_period_months"`
}

type subscriptionUserResponse struct {
	UserID                int64                          `json:"user_id"`
	Username              string                         `json:"username"`
	PlanID                *int64                         `json:"plan_id"`
	PlanName              string                         `json:"plan_name"`
	SubscriptionTitle     string                         `json:"subscription_title"`
	PlanEnabled           bool                           `json:"plan_enabled"`
	Enabled               bool                           `json:"enabled"`
	ExpiresAt             *time.Time                     `json:"expires_at"`
	TrafficResetMode      string                         `json:"traffic_reset_mode"`
	TrafficResetDay       int                            `json:"traffic_reset_day"`
	TrafficResetTime      string                         `json:"traffic_reset_time"`
	ClientCount           int                            `json:"client_count"`
	EnabledNodeCount      int                            `json:"enabled_node_count"`
	TrafficLimitBytes     *int64                         `json:"traffic_limit_bytes"`
	UsedBytes             int64                          `json:"used_bytes"`
	CycleStartedAt        time.Time                      `json:"cycle_started_at"`
	NextResetAt           *time.Time                     `json:"next_reset_at"`
	BillingPeriodMonths   *int                           `json:"billing_period_months"`
	Active                bool                           `json:"active"`
	Status                string                         `json:"status"`
	SubscriptionToken     string                         `json:"subscription_token,omitempty"`
	SubscriptionURL       string                         `json:"subscription_url,omitempty"`
	SubscriptionBase64URL string                         `json:"subscription_base64_url,omitempty"`
	SubscriptionMihomoURL string                         `json:"subscription_mihomo_url,omitempty"`
	SubscriptionAutoURL   string                         `json:"subscription_auto_url,omitempty"`
	PasswordRequest       *passwordChangeRequestResponse `json:"password_request,omitempty"`
	CreatedAt             time.Time                      `json:"created_at"`
	UpdatedAt             time.Time                      `json:"updated_at"`
}

func (s *server) listSubscriptionUsers(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListSubscribers(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]subscriptionUserResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toSubscriptionUserResponse(value, "", false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": response})
}

func (s *server) getSubscriptionUser(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅用户 ID 无效")
	if !ok {
		return
	}
	value, err := s.subscriptions.GetSubscriber(r.Context(), id)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	response := toSubscriptionUserResponse(value, baseURL, true)
	passwordRequest, err := s.authService.LatestPasswordChangeRequest(r.Context(), id)
	if err != nil {
		writeInternalError(w)
		return
	}
	if passwordRequest != nil {
		formatted := toPasswordChangeRequestResponse(*passwordRequest)
		response.PasswordRequest = &formatted
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": response})
}

func (s *server) updateSubscriptionUser(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅用户 ID 无效")
	if !ok {
		return
	}
	var request updateSubscriptionUserRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	planID, planIDSet, err := decodeNullableInt64(request.PlanID)
	if err != nil || planID != nil && *planID <= 0 {
		writeError(w, http.StatusBadRequest, "套餐格式无效")
		return
	}
	expiresAt, expiresAtSet, err := parseClientExpiration(request.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "到期时间格式无效")
		return
	}
	billingPeriod, billingPeriodSet, err := decodeNullableInt(request.BillingPeriodMonths)
	if err != nil {
		writeError(w, http.StatusBadRequest, "付款周期格式无效")
		return
	}
	value, mutations, err := s.subscriptions.UpdateSubscriber(r.Context(), id, subscriptionstore.UpdateSubscriberInput{
		PlanIDSet: planIDSet, PlanID: planID, Enabled: request.Enabled,
		ExpiresAtSet: expiresAtSet, ExpiresAt: expiresAt,
		TrafficResetMode: request.TrafficResetMode, TrafficResetDay: request.TrafficResetDay,
		TrafficResetTime:       request.TrafficResetTime,
		BillingPeriodMonthsSet: billingPeriodSet, BillingPeriodMonths: billingPeriod,
	})
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toSubscriptionUserResponse(value, baseURL, true)})
}

func (s *server) regenerateSubscriptionUserToken(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅用户 ID 无效")
	if !ok {
		return
	}
	tokenValue, err := s.subscriptions.RegenerateSubscriptionToken(r.Context(), id)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	urls := buildSubscriptionURLs(baseURL, tokenValue)
	writeJSON(w, http.StatusOK, map[string]any{
		"subscription_token":      tokenValue,
		"subscription_url":        urls.Base64,
		"subscription_base64_url": urls.Base64,
		"subscription_mihomo_url": urls.Mihomo,
		"subscription_auto_url":   urls.Auto,
	})
}

func (s *server) resetSubscriptionUserTraffic(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "订阅用户 ID 无效")
	if !ok {
		return
	}
	value, mutations, err := s.subscriptions.ResetSubscriberTraffic(r.Context(), id)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toSubscriptionUserResponse(value, baseURL, true)})
}

func toSubscriptionUserResponse(value subscriptionstore.Subscriber, baseURL string, includeToken bool) subscriptionUserResponse {
	response := subscriptionUserResponse{
		UserID: value.UserID, Username: value.Username, PlanID: value.PlanID, PlanName: value.PlanName,
		SubscriptionTitle: value.SubscriptionTitle,
		PlanEnabled:       value.PlanEnabled, Enabled: value.ProfileEnabled, ExpiresAt: value.ExpiresAt,
		TrafficResetMode: value.TrafficResetMode, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime,
		ClientCount:      value.ClientCount, EnabledNodeCount: value.EnabledNodeCount,
		TrafficLimitBytes: value.TrafficLimitBytes, UsedBytes: value.UsedBytes,
		CycleStartedAt: value.CycleStartedAt, NextResetAt: value.NextResetAt,
		BillingPeriodMonths: value.BillingPeriodMonths, Active: value.Active, Status: value.Status,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	if includeToken {
		urls := buildSubscriptionURLs(baseURL, value.SubscriptionToken)
		response.SubscriptionToken = value.SubscriptionToken
		response.SubscriptionURL = urls.Base64
		response.SubscriptionBase64URL = urls.Base64
		response.SubscriptionMihomoURL = urls.Mihomo
		response.SubscriptionAutoURL = urls.Auto
	}
	return response
}

type subscriptionURLSet struct {
	Base64 string
	Mihomo string
	Auto   string
}

func buildSubscriptionURLs(baseURL, tokenValue string) subscriptionURLSet {
	base := baseURL + "/sub/" + url.PathEscape(tokenValue)
	return subscriptionURLSet{Base64: base, Mihomo: base + "/mihomo", Auto: base + "/auto"}
}

func writeSubscriptionUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrSubscriberNotFound):
		writeError(w, http.StatusNotFound, "订阅用户不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidSubscriberPlan):
		writeError(w, http.StatusBadRequest, "套餐不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidSubscriberExpiry):
		writeError(w, http.StatusBadRequest, "到期时间无效")
	case errors.Is(err, subscriptionstore.ErrInvalidTrafficReset):
		writeError(w, http.StatusBadRequest, "流量重置配置无效")
	case errors.Is(err, subscriptionstore.ErrInvalidBillingPeriod):
		writeError(w, http.StatusBadRequest, "付款周期仅支持 1、3、6 或 12 个月")
	case errors.Is(err, subscriptionstore.ErrServerNotDistributable):
		writeError(w, http.StatusBadRequest, "订阅发布节点只能使用管理员创建的服务器")
	default:
		writeInternalError(w)
	}
}
