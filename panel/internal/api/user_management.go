package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type createAssignedNodeRequest struct {
	SourceType          string          `json:"source_type"`
	SourceID            int64           `json:"source_id"`
	ProxyID             int64           `json:"proxy_id"`
	Name                string          `json:"name"`
	ClientUDP443        bool            `json:"client_udp443"`
	Enabled             *bool           `json:"enabled"`
	ExpiresAt           json.RawMessage `json:"expires_at"`
	BillingPeriodMonths *int            `json:"billing_period_months"`
	UserRelayPortCount  *int            `json:"user_relay_port_count"`
	clientTrafficRequest
}

func (s *server) listAdminUsers(w http.ResponseWriter, r *http.Request, _ auth.User) {
	users, err := s.authService.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]accessUserResponse, 0)
	for _, user := range users {
		if user.Role == auth.RoleCarpool {
			response = append(response, toAccessUserResponse(user))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": response})
}

func (s *server) getAdminUserDetail(w http.ResponseWriter, r *http.Request, _ auth.User) {
	user, ok := s.readManagedUser(w, r)
	if !ok {
		return
	}
	sources, err := s.carpoolSources(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	assignments, err := s.carpoolAssignments(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	nodes := []carpoolSource{}
	assigned := map[string]bool{}
	for _, assignment := range assignments {
		assigned[fmt.Sprintf("%s:%d", assignment.SourceType, assignment.SourceID)] = true
		node, _, err := s.carpoolNode(r.Context(), assignment)
		if err != nil {
			if errors.Is(err, proxystore.ErrClientNotFound) || errors.Is(err, relaystore.ErrNotFound) {
				continue
			}
			writeInternalError(w, err)
			return
		}
		nodes = append(nodes, node)
	}
	clients, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	clientByProxy := map[int64]proxystore.Client{}
	for _, client := range clients {
		current, ok := clientByProxy[client.ProxyID]
		if !ok || client.ID < current.ID {
			clientByProxy[client.ProxyID] = client
		}
	}
	available := []carpoolSource{}
	for _, source := range sources {
		if !assigned[fmt.Sprintf("%s:%d", source.SourceType, source.SourceID)] {
			proxyID := source.SourceID
			if source.SourceType == "relay" {
				relay, err := s.relays.Get(r.Context(), source.SourceID)
				if err != nil || relay.TargetProxyID == nil {
					continue
				}
				proxyID = *relay.TargetProxyID
			}
			if client, ok := clientByProxy[proxyID]; ok {
				value := toClientResponse(client)
				source.Client = &value
			}
			available = append(available, source)
		}
	}
	relays, err := s.relays.ListByOwner(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	relayResponse := make([]adminUserRelayResponse, 0, len(relays))
	for _, relay := range relays {
		relayResponse = append(relayResponse, toAdminUserRelayResponse(relay))
	}
	passwordRequest, err := s.authService.LatestPasswordChangeRequest(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	var pendingPasswordRequest *passwordChangeRequestResponse
	if passwordRequest != nil && passwordRequest.Status == "pending" {
		value := toPasswordChangeRequestResponse(*passwordRequest)
		pendingPasswordRequest = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  toAccessUserResponse(user),
		"nodes": nodes, "available_sources": available, "relays": relayResponse, "password_request": pendingPasswordRequest,
	})
}

func (s *server) createAdminUserNode(w http.ResponseWriter, r *http.Request, actor auth.User) {
	user, ok := s.readManagedUser(w, r)
	if !ok {
		return
	}
	var request createAssignedNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.SourceType == "" && request.ProxyID > 0 {
		request.SourceType, request.SourceID = "proxy", request.ProxyID
	}
	sources, err := s.carpoolSources(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	var proxyID int64
	for _, source := range sources {
		if source.SourceType != request.SourceType || source.SourceID != request.SourceID {
			continue
		}
		if source.SourceType == "proxy" {
			proxyID = source.SourceID
		} else {
			relay, err := s.relays.Get(r.Context(), source.SourceID)
			if err != nil || relay.TargetProxyID == nil {
				break
			}
			proxyID = *relay.TargetProxyID
		}
		break
	}
	if proxyID == 0 {
		if request.SourceType == "proxy" {
			if _, err := s.proxies.Get(r.Context(), request.SourceID); err != nil {
				writeProxyError(w, err)
				return
			}
			if err := proxystore.RequireAdminCreatedProxy(r.Context(), s.db, request.SourceID); err != nil {
				writeClientAssignmentError(w, err)
				return
			}
		}
		writeError(w, http.StatusBadRequest, "节点来源不可用")
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
	userRelayPortCount := proxystore.MaxClientRelayPorts
	if request.UserRelayPortCount != nil {
		userRelayPortCount = *request.UserRelayPortCount
	}
	value, mutation, err := s.proxies.CreateCarpoolNode(r.Context(), proxystore.AssignedClientCreateInput{
		UserID: user.ID, ProxyID: proxyID, Name: request.Name,
		ClientUDP443: request.ClientUDP443, Enabled: enabled, ExpiresAt: expiresAt,
		Traffic: traffic, BillingPeriodMonths: request.BillingPeriodMonths,
		UserRelayPortCount: userRelayPortCount,
	}, request.SourceType, request.SourceID)
	if err != nil {
		if errors.Is(err, proxystore.ErrAssignedClientExists) {
			writeError(w, http.StatusConflict, "该用户已开通此节点")
			return
		}
		writeClientRelayPortError(w, err)
		return
	}
	if mutation.Version > 0 {
		s.notifyProxyMutation(mutation)
	}
	s.recordAudit(r, actor, "carpool.node.create", "user", user.ID, "为用户添加拼车节点")
	writeJSON(w, http.StatusCreated, map[string]any{"client": toClientResponse(value)})
}

func (s *server) readManagedUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	id, ok := readPositiveID(w, r.PathValue("id"), "用户 ID 无效")
	if !ok {
		return auth.User{}, false
	}
	user, err := s.authService.GetUser(r.Context(), id)
	if errors.Is(err, auth.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "拼车用户不存在")
		return auth.User{}, false
	}
	if err != nil {
		writeInternalError(w, err)
		return auth.User{}, false
	}
	if user.Role != auth.RoleCarpool {
		writeError(w, http.StatusBadRequest, "目标账号不是拼车用户")
		return auth.User{}, false
	}
	return user, true
}

func (s *server) deleteAdminUserNode(w http.ResponseWriter, r *http.Request, actor auth.User) {
	user, ok := s.readManagedUser(w, r)
	if !ok {
		return
	}
	id, ok := readPositiveID(w, r.PathValue("nodeID"), "节点 ID 无效")
	if !ok {
		return
	}
	result, err := s.db.ExecContext(r.Context(), `DELETE FROM carpool_node_assignments WHERE user_id = ? AND id = ?`, user.ID, id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	s.recordAudit(r, actor, "carpool.node.delete", "user", user.ID, "移除拼车节点")
	writeNoContent(w)
}
