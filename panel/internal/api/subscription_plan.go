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
	RoutingPresetID   *int64 `json:"routing_preset_id"`
	TemplateID        *int64 `json:"template_id"`
}

type updateSubscriptionPlanRequest struct {
	Name              *string         `json:"name"`
	SubscriptionTitle *string         `json:"subscription_title"`
	Enabled           *bool           `json:"enabled"`
	TrafficLimitBytes json.RawMessage `json:"traffic_limit_bytes"`
	RoutingPresetID   json.RawMessage `json:"routing_preset_id"`
	TemplateID        json.RawMessage `json:"template_id"`
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
	RoutingPresetID   *int64                              `json:"routing_preset_id"`
	RoutingBindings   subscriptionstore.RoutingBindings   `json:"routing_bindings"`
	TemplateID        *int64                              `json:"template_id"`
	Nodes             []subscriptionPublishedNodeResponse `json:"nodes"`
	CreatedAt         time.Time                           `json:"created_at"`
	UpdatedAt         time.Time                           `json:"updated_at"`
}

func (s *server) setSubscriptionPlanRoutingBindings(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "共享订阅 ID 无效")
	if !ok {
		return
	}
	var request setRoutingBindingsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := s.subscriptions.SetPlanRoutingBindings(r.Context(), id, request.RoutingBindings)
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) listSubscriptionPlans(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListPlans(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]subscriptionPlanResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toSubscriptionPlanResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": response})
}

func (s *server) createSubscriptionPlan(w http.ResponseWriter, r *http.Request, user auth.User) {
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
		RoutingPresetID: request.RoutingPresetID, TemplateID: request.TemplateID,
	})
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	s.recordAudit(r, user, "subscription_plan.create", "subscription_plan", value.ID, "创建共享订阅 "+value.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) getSubscriptionPlan(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "共享订阅 ID 无效")
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

func (s *server) updateSubscriptionPlan(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "共享订阅 ID 无效")
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
	routingPresetID, routingPresetIDSet, err := decodeNullableInt64(request.RoutingPresetID)
	if err != nil || routingPresetIDSet && routingPresetID == nil {
		writeError(w, http.StatusBadRequest, "分流方案格式无效")
		return
	}
	templateID, templateIDSet, err := decodeNullableInt64(request.TemplateID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "订阅模板格式无效")
		return
	}
	value, mutations, err := s.subscriptions.UpdatePlan(r.Context(), id, subscriptionstore.UpdatePlanInput{
		Name: request.Name, SubscriptionTitle: request.SubscriptionTitle, Enabled: request.Enabled,
		TrafficLimitBytesSet: trafficLimitSet, TrafficLimitBytes: trafficLimit,
		RoutingPresetIDSet: routingPresetIDSet, RoutingPresetID: routingPresetID,
		TemplateIDSet: templateIDSet, TemplateID: templateID,
	})
	if err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	s.recordAudit(r, user, "subscription_plan.update", "subscription_plan", value.ID, "更新共享订阅 "+value.Name)
	writeJSON(w, http.StatusOK, map[string]any{"plan": toSubscriptionPlanResponse(value)})
}

func (s *server) deleteSubscriptionPlan(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "共享订阅 ID 无效")
	if !ok {
		return
	}
	if err := s.subscriptions.DeletePlan(r.Context(), id); err != nil {
		writeSubscriptionPlanError(w, err)
		return
	}
	s.recordAudit(r, user, "subscription_plan.delete", "subscription_plan", id, "删除共享订阅")
	writeNoContent(w)
}

func (s *server) setSubscriptionPlanNodes(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "共享订阅 ID 无效")
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
		RoutingPresetID:   value.RoutingPresetID, RoutingBindings: value.RoutingBindings,
		TemplateID: value.TemplateID,
		Nodes:      nodes, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
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
		writeError(w, http.StatusNotFound, "共享订阅不存在")
	case errors.Is(err, subscriptionstore.ErrPublishedNodeNotFound):
		writeError(w, http.StatusNotFound, "共享订阅包含的发布节点不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanName):
		writeError(w, http.StatusBadRequest, "共享订阅名称不能为空且不能超过 100 个字符")
	case errors.Is(err, subscriptionstore.ErrInvalidSubscriptionTitle):
		writeError(w, http.StatusBadRequest, "订阅显示名称不能超过 100 个字符")
	case errors.Is(err, subscriptionstore.ErrInvalidTrafficLimit):
		writeError(w, http.StatusBadRequest, "流量额度不能小于 0")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanNodes):
		writeError(w, http.StatusBadRequest, "共享订阅节点列表无效；同一共享订阅不能包含多个指向同一 Proxy 的发布节点")
	case errors.Is(err, subscriptionstore.ErrInvalidRoutingBindings):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, subscriptionstore.ErrServerNotDistributable):
		writeError(w, http.StatusBadRequest, "共享订阅只能包含管理员创建服务器上的发布节点")
	case errors.Is(err, subscriptionstore.ErrPlanReferenced):
		writeError(w, http.StatusConflict, "请先切换或取消使用该共享订阅的订阅用户")
	case errors.Is(err, subscriptionstore.ErrInvalidPlanRouting):
		writeError(w, http.StatusBadRequest, "该分流方案已停用，不能用于新的共享订阅选择")
	case errors.Is(err, subscriptionstore.ErrRoutingPresetNotFound):
		writeError(w, http.StatusBadRequest, "分流方案不存在")
	case errors.Is(err, subscriptionstore.ErrTemplateNotFound):
		writeError(w, http.StatusBadRequest, "订阅模板不存在")
	default:
		writeInternalError(w, err)
	}
}
