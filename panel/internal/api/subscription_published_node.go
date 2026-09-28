package api

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type createSubscriptionPublishedNodeRequest struct {
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`
	TargetProxyID     int64           `json:"target_proxy_id"`
	SourceProxyID     *int64          `json:"source_proxy_id"`
	TrafficMultiplier json.RawMessage `json:"traffic_multiplier"`
	Enabled           *bool           `json:"enabled"`
}

type updateSubscriptionPublishedNodeRequest struct {
	Name              *string         `json:"name"`
	TrafficMultiplier json.RawMessage `json:"traffic_multiplier"`
	Enabled           *bool           `json:"enabled"`
}

type subscriptionPublishedNodeResponse struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	Mode              string    `json:"mode"`
	TargetProxyID     int64     `json:"target_proxy_id"`
	TargetProxyName   string    `json:"target_proxy_name"`
	TargetServerID    int64     `json:"target_server_id"`
	TargetServerName  string    `json:"target_server_name"`
	SourceProxyID     *int64    `json:"source_proxy_id,omitempty"`
	SourceProxyName   string    `json:"source_proxy_name,omitempty"`
	SourceServerID    *int64    `json:"source_server_id,omitempty"`
	SourceServerName  string    `json:"source_server_name,omitempty"`
	RelayID           *int64    `json:"relay_id,omitempty"`
	EntryAddress      string    `json:"entry_address,omitempty"`
	EntryPort         int       `json:"entry_port,omitempty"`
	TrafficMultiplier float64   `json:"traffic_multiplier"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Position          int       `json:"position,omitempty"`
}

func (s *server) listSubscriptionPublishedNodes(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.subscriptions.ListPublishedNodes(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]subscriptionPublishedNodeResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toSubscriptionPublishedNodeResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": response})
}

func (s *server) createSubscriptionPublishedNode(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var request createSubscriptionPublishedNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	multiplierBP, multiplierSet, err := decodeTrafficMultiplier(request.TrafficMultiplier)
	if err != nil {
		writeSubscriptionPublishedNodeError(w, subscriptionstore.ErrInvalidTrafficMultiplier)
		return
	}
	if !multiplierSet {
		multiplierBP = 100
	}
	if mode == subscriptionstore.NodeModeRelay && request.SourceProxyID != nil {
		source, err := s.proxies.Get(r.Context(), *request.SourceProxyID)
		if err != nil {
			writeSubscriptionPublishedNodeError(w, subscriptionstore.ErrSourceProxyNotFound)
			return
		}
		supported, err := s.serverSupportsCapability(r, source.ServerID, agentcontrol.CapabilityRelayRealm)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if !supported {
			writeError(w, http.StatusConflict, "中转节点所在 Agent 不支持 Realm 中转")
			return
		}
	}
	value, mutation, err := s.subscriptions.CreatePublishedNode(r.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: request.Name, Mode: mode, TargetProxyID: request.TargetProxyID,
		SourceProxyID: request.SourceProxyID, TrafficMultiplierBP: multiplierBP, Enabled: enabled,
	})
	if err != nil {
		writeSubscriptionPublishedNodeError(w, err)
		return
	}
	s.notifySubscriptionRelayMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"node": toSubscriptionPublishedNodeResponse(value)})
}

func (s *server) updateSubscriptionPublishedNode(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "发布节点 ID 无效")
	if !ok {
		return
	}
	var request updateSubscriptionPublishedNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	multiplierBP, multiplierSet, err := decodeTrafficMultiplier(request.TrafficMultiplier)
	if err != nil {
		writeSubscriptionPublishedNodeError(w, subscriptionstore.ErrInvalidTrafficMultiplier)
		return
	}
	var multiplier *int
	if multiplierSet {
		multiplier = &multiplierBP
	}
	value, mutations, err := s.subscriptions.UpdatePublishedNode(r.Context(), id, subscriptionstore.UpdatePublishedNodeInput{
		Name: request.Name, TrafficMultiplierBP: multiplier, Enabled: request.Enabled,
	})
	if err != nil {
		writeSubscriptionPublishedNodeError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	writeJSON(w, http.StatusOK, map[string]any{"node": toSubscriptionPublishedNodeResponse(value)})
}

func (s *server) deleteSubscriptionPublishedNode(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "发布节点 ID 无效")
	if !ok {
		return
	}
	mutation, err := s.subscriptions.DeletePublishedNode(r.Context(), id)
	if err != nil {
		writeSubscriptionPublishedNodeError(w, err)
		return
	}
	s.notifySubscriptionRelayMutation(mutation)
	writeNoContent(w)
}

func (s *server) notifySubscriptionRelayMutation(mutation *relaystore.Mutation) {
	if mutation != nil {
		s.notifyRelayMutations([]relaystore.Mutation{*mutation})
	}
}

func toSubscriptionPublishedNodeResponse(value subscriptionstore.PublishedNode) subscriptionPublishedNodeResponse {
	return subscriptionPublishedNodeResponse{
		ID: value.ID, Name: value.Name, Mode: value.Mode,
		TargetProxyID: value.TargetProxyID, TargetProxyName: value.TargetProxyName,
		TargetServerID: value.TargetServerID, TargetServerName: value.TargetServerName,
		SourceProxyID: value.SourceProxyID, SourceProxyName: value.SourceProxyName,
		SourceServerID: value.SourceServerID, SourceServerName: value.SourceServerName,
		RelayID: value.RelayID, EntryAddress: value.EntryAddress, EntryPort: value.EntryPort,
		TrafficMultiplier: float64(value.TrafficMultiplierBP) / 100,
		Enabled:           value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func writeSubscriptionPublishedNodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrPublishedNodeNotFound):
		writeError(w, http.StatusNotFound, "发布节点不存在")
	case errors.Is(err, subscriptionstore.ErrInvalidNodeName):
		writeError(w, http.StatusBadRequest, "发布名称不能为空且不能超过 100 个字符")
	case errors.Is(err, subscriptionstore.ErrInvalidTrafficMultiplier):
		writeError(w, http.StatusBadRequest, "流量倍率必须为 0.10–5.00，且最多两位小数")
	case errors.Is(err, subscriptionstore.ErrInvalidNodeMode):
		writeError(w, http.StatusBadRequest, "发布模式仅支持单一节点或中转 + 落地")
	case errors.Is(err, subscriptionstore.ErrTargetProxyNotFound):
		writeError(w, http.StatusNotFound, "落地节点不存在或已移除")
	case errors.Is(err, subscriptionstore.ErrSourceProxyNotFound):
		writeError(w, http.StatusNotFound, "中转节点不存在或已移除")
	case errors.Is(err, subscriptionstore.ErrPublishedNodeReferenced):
		writeError(w, http.StatusConflict, "请先从套餐中移除此发布节点")
	case errors.Is(err, subscriptionstore.ErrSourceProxyRequired),
		errors.Is(err, subscriptionstore.ErrInvalidNodeTopology),
		errors.Is(err, subscriptionstore.ErrInvalidNodeUpdate):
		writeError(w, http.StatusBadRequest, "发布节点配置无效")
	case errors.Is(err, relaystore.ErrPortConflict):
		writeError(w, http.StatusConflict, "中转节点没有可用的共享 Realm 端口")
	case errors.Is(err, relaystore.ErrServerDecommissioning):
		writeError(w, http.StatusConflict, "中转节点所在服务器正在退役")
	case errors.Is(err, relaystore.ErrServerNotFound), errors.Is(err, relaystore.ErrProxyNotFound):
		writeError(w, http.StatusNotFound, "发布节点引用的服务器或 Proxy 不存在")
	default:
		writeInternalError(w)
	}
}

func decodeTrafficMultiplier(raw json.RawMessage) (int, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	number := strings.TrimSpace(string(raw))
	if !json.Valid(raw) || number == "" || number[0] == '"' || number == "null" {
		return 0, true, subscriptionstore.ErrInvalidTrafficMultiplier
	}
	value, ok := new(big.Rat).SetString(number)
	if !ok {
		return 0, true, subscriptionstore.ErrInvalidTrafficMultiplier
	}
	value.Mul(value, big.NewRat(100, 1))
	if !value.IsInt() || !value.Num().IsInt64() {
		return 0, true, subscriptionstore.ErrInvalidTrafficMultiplier
	}
	bp := int(value.Num().Int64())
	if bp < 10 || bp > 500 {
		return 0, true, subscriptionstore.ErrInvalidTrafficMultiplier
	}
	return bp, true, nil
}
