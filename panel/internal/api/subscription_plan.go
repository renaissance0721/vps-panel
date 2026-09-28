package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type createSubscriptionPlanRequest struct {
	Name              string `json:"name"`
	SubscriptionTitle string `json:"subscription_title"`
	Enabled           *bool  `json:"enabled"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
}

type updateSubscriptionPlanRequest struct {
	Name              *string         `json:"name"`
	SubscriptionTitle *string         `json:"subscription_title"`
	Enabled           *bool           `json:"enabled"`
	TrafficLimitBytes json.RawMessage `json:"traffic_limit_bytes"`
}

type setSubscriptionPlanNodesRequest struct {
	NodeIDs []int64 `json:"node_ids"`
}

type subscriptionPlanResponse struct {
	ID                int64                               `json:"id"`
	Name              string                              `json:"name"`
	SubscriptionTitle string                              `json:"subscription_title"`
	Enabled           bool                                `json:"enabled"`
	TrafficLimitBytes *int64                              `json:"traffic_limit_bytes"`
	Nodes             []subscriptionPublishedNodeResponse `json:"nodes"`
	CreatedAt         time.Time                           `json:"created_at"`
	UpdatedAt         time.Time                           `json:"updated_at"`
}

func (s *server) listSubscriptionPlans(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListPlans(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]subscriptionPlanResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toSubscriptionPlanResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": response})
}

func (s *server) createSubscriptionPlan(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var request createSubscriptionPlanRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, err := s.subscriptions.CreatePlan(r.Context(), subscriptionstore.CreatePlanInput{
		Name: request.Name, SubscriptionTitle: request.SubscriptionTitle,
		Enabled: enabled, TrafficLimitBytes: request.TrafficLimitBytes,
	})
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) getSubscriptionPlan(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "套餐 ID 无效")
	if !ok {
		return
	}
	value, err := s.subscriptions.GetPlan(r.Context(), id)
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) updateSubscriptionPlan(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "套餐 ID 无效")
	if !ok {
		return
	}
	var request updateSubscriptionPlanRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	trafficLimit, trafficLimitSet, err := decodeNullableInt64(request.TrafficLimitBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "流量额度格式无效")
		return
	}
	value, mutations, err := s.subscriptions.UpdatePlan(r.Context(), id, subscriptionstore.UpdatePlanInput{
		Name: request.Name, SubscriptionTitle: request.SubscriptionTitle, Enabled: request.Enabled,
		TrafficLimitBytesSet: trafficLimitSet, TrafficLimitBytes: trafficLimit,
	})
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	writeJSON(w, http.StatusOK, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) deleteSubscriptionPlan(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "套餐 ID 无效")
	if !ok {
		return
	}
	if err := s.subscriptions.DeletePlan(r.Context(), id); err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *server) setSubscriptionPlanNodes(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "套餐 ID 无效")
	if !ok {
		return
	}
	var request setSubscriptionPlanNodesRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutations, err := s.subscriptions.SetPlanNodes(r.Context(), id, request.NodeIDs)
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	writeJSON(w, http.StatusOK, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func toSubscriptionPlanResponse(value subscriptionstore.Plan) subscriptionPlanResponse {
	nodes := make([]subscriptionPublishedNodeResponse, 0, len(value.Nodes))
	for _, node := range value.Nodes {
		response := toSubscriptionPublishedNodeResponse(node.PublishedNode)
		response.Position = node.Position
		nodes = append(nodes, response)
	}
	return subscriptionPlanResponse{
		ID: value.ID, Name: value.Name, SubscriptionTitle: value.SubscriptionTitle, Enabled: value.Enabled,
		TrafficLimitBytes: value.TrafficLimitBytes,
		Nodes:             nodes, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func decodeNullableInt64(raw json.RawMessage) (*int64, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var value *int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func decodeNullableInt(raw json.RawMessage) (*int, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var value *int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func writeSubscriptionPlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, "套餐不存在")
	case errors.Is(err, subscriptionstore.ErrPublishedNodeNotFound):
		writeError(w, http.StatusNotFound, "套餐包含的发布节点不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanName):
		writeError(w, http.StatusBadRequest, "套餐名称不能为空且不能超过 100 个字符")
	case errors.Is(err, subscriptionstore.ErrInvalidSubscriptionTitle):
		writeError(w, http.StatusBadRequest, "订阅显示名称不能超过 100 个字符")
	case errors.Is(err, subscriptionstore.ErrInvalidTrafficLimit):
		writeError(w, http.StatusBadRequest, "流量额度不能小于 0")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanNodes):
		writeError(w, http.StatusBadRequest, "套餐节点列表无效；同一套餐不能包含多个指向同一 Proxy 的发布节点")
	case errors.Is(err, subscriptionstore.ErrPlanReferenced):
		writeError(w, http.StatusConflict, "请先切换或取消使用该套餐的订阅用户")
	default:
		writeInternalError(w)
	}
}
