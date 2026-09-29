package api

import (
	"errors"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type clientAssignmentRequest struct {
	UserID              *int64 `json:"user_id"`
	BillingPeriodMonths *int   `json:"billing_period_months"`
}

type clientRelayPortCountRequest struct {
	Count int `json:"user_relay_port_count"`
}

func (s *server) listProxyClients(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	if _, err := s.proxyForUser(r.Context(), user, id); err != nil {
		writeProxyError(w, err)
		return
	}
	values, err := s.proxies.ListClients(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	response := make([]clientSummaryResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toClientSummaryResponse(proxystore.ClientSummary{
			ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
			AssignedUserID: value.AssignedUserID, AssignedUsername: value.AssignedUsername,
			BillingPeriodMonths: value.BillingPeriodMonths,
			UserRelayPortStart:  value.UserRelayPortStart, UserRelayPortEnd: value.UserRelayPortEnd,
			UserRelayPortCount: value.UserRelayPortCount,
			ClientUDP443:       value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
			TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
			TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
			TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
			SubscriptionManaged: value.SubscriptionManaged,
			CreatedAt:           value.CreatedAt, UpdatedAt: value.UpdatedAt,
		}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": response})
}

func (s *server) updateClientRelayPortCount(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	var request clientRelayPortCountRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := s.proxies.UpdateClientRelayPortCount(r.Context(), id, request.Count)
	if err != nil {
		writeClientRelayPortError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func writeClientRelayPortError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxystore.ErrInvalidClientRelayPortCount):
		writeError(w, http.StatusBadRequest, "用户中转端口数量必须在 0–5 之间")
	case errors.Is(err, proxystore.ErrClientRelayPortsUnavailable):
		writeError(w, http.StatusConflict, "该服务器没有可分配的连续用户中转端口")
	case errors.Is(err, proxystore.ErrClientRelayPortsActive):
		writeError(w, http.StatusConflict, "该客户端存在正在使用的用户中转，请先删除中转后调整端口")
	case errors.Is(err, proxystore.ErrClientNotAssigned):
		writeError(w, http.StatusConflict, "仅已分配给普通用户的客户端可配置用户中转端口")
	default:
		writeClientAssignmentError(w, err)
	}
}

func (s *server) assignProxyClient(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	var request clientAssignmentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.UserID != nil && *request.UserID <= 0 {
		writeError(w, http.StatusBadRequest, "普通用户账号无效")
		return
	}
	current, err := s.proxies.GetClient(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if err := s.proxies.ValidateClientAssignment(r.Context(), request.UserID, request.BillingPeriodMonths); err != nil {
		writeClientAssignmentError(w, err)
		return
	}
	assignmentRemoved := current.AssignedUserID != nil &&
		(request.UserID == nil || *request.UserID != *current.AssignedUserID)
	if assignmentRemoved {
		if err := s.deleteUserRelaysForClient(r.Context(), id); err != nil {
			writeRelayError(w, err)
			return
		}
	}
	value, err := s.proxies.AssignClient(r.Context(), id, request.UserID, request.BillingPeriodMonths)
	if err != nil {
		writeClientAssignmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func writeClientAssignmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxystore.ErrAssignmentUserNotFound):
		writeError(w, http.StatusBadRequest, "普通用户账号不存在")
	case errors.Is(err, proxystore.ErrInvalidAssignmentRole):
		writeError(w, http.StatusBadRequest, "客户端只能分配给普通用户账号")
	case errors.Is(err, proxystore.ErrInvalidBillingPeriod):
		writeError(w, http.StatusBadRequest, "付款周期仅支持 1、3、6、12 个月或未设置")
	case errors.Is(err, proxystore.ErrSubscriptionManagedClient):
		writeError(w, http.StatusConflict, "该客户端由订阅系统管理，请在订阅管理中操作")
	case errors.Is(err, proxystore.ErrNotDistributable):
		writeError(w, http.StatusBadRequest, "仅管理员创建的服务器节点可分配给普通用户")
	default:
		writeProxyError(w, err)
	}
}

func (s *server) createProxyClient(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	if _, err := s.proxyForUser(r.Context(), user, id); err != nil {
		writeProxyError(w, err)
		return
	}
	var request createClientRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	traffic, _, err := parseClientTrafficRequest(request.clientTrafficRequest, proxystore.ClientTrafficConfig{})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	expiresAt, _, err := parseClientExpiration(request.ExpiresAt)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	value, mutation, err := s.proxies.CreateClient(r.Context(), id, proxystore.ClientCreateInput{
		Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: enabled,
		ExpiresAt: expiresAt, Traffic: traffic,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"client": toClientResponse(value)})
}

func (s *server) getProxyClient(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	value, err := s.clientForUser(r.Context(), user, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) updateProxyClient(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	current, err := s.clientForUser(r.Context(), user, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	var request updateClientRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	expiresAt, expiresAtSet, err := parseClientExpiration(request.ExpiresAt)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	var traffic *proxystore.ClientTrafficConfig
	if hasClientTrafficRequest(request.clientTrafficRequest) {
		parsed, _, err := parseClientTrafficRequest(request.clientTrafficRequest, proxystore.ClientTrafficConfig{
			LimitBytes: current.TrafficLimitBytes, ResetMode: current.TrafficResetMode,
			Weekday: current.TrafficResetWeekday, Day: current.TrafficResetDay,
			ResetTime: current.TrafficResetTime,
		})
		if err != nil {
			writeProxyError(w, err)
			return
		}
		traffic = &parsed
	}
	value, mutation, err := s.proxies.UpdateClient(r.Context(), id, proxystore.ClientUpdateInput{
		Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: request.Enabled,
		ExpiresAtSet: expiresAtSet, ExpiresAt: expiresAt, Traffic: traffic,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) resetProxyClientTraffic(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	if _, err := s.clientForUser(r.Context(), user, id); err != nil {
		writeProxyError(w, err)
		return
	}
	value, mutation, err := s.proxies.ResetClientTrafficWithMutation(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if mutation.Version > 0 {
		s.notifyProxyMutation(mutation)
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) deleteProxyClient(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	if _, err := s.clientForUser(r.Context(), user, id); err != nil {
		writeProxyError(w, err)
		return
	}
	if err := s.deleteUserRelaysForClient(r.Context(), id); err != nil {
		writeRelayError(w, err)
		return
	}
	mutation, err := s.proxies.DeleteClient(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeNoContent(w)
}

func (s *server) getProxyClientShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	if _, err := s.clientForUser(r.Context(), user, id); err != nil {
		writeProxyError(w, err)
		return
	}
	value, err := s.proxies.GetClientShare(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": clientShareResponse{
		Client: toClientResponse(value.Client), ProxyName: value.ProxyName, Address: value.Address,
		Port: value.Port, Protocol: value.Protocol, Method: value.Method, Network: value.Network,
		Security: value.Security, ServerName: value.ServerName,
		Fingerprint: value.Fingerprint, Flow: value.Flow, URI: value.URI,
	}})
}
