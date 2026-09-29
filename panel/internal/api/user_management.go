package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type createAssignedNodeRequest struct {
	ProxyID             int64           `json:"proxy_id"`
	Name                string          `json:"name"`
	ClientUDP443        bool            `json:"client_udp443"`
	Enabled             *bool           `json:"enabled"`
	ExpiresAt           json.RawMessage `json:"expires_at"`
	BillingPeriodMonths *int            `json:"billing_period_months"`
	UserRelayPortCount  *int            `json:"user_relay_port_count"`
	clientTrafficRequest
}

type userManagementNodeResponse struct {
	ProxyID    int64           `json:"proxy_id"`
	ServerName string          `json:"server_name"`
	ProxyName  string          `json:"proxy_name"`
	Protocol   string          `json:"protocol"`
	Client     *clientResponse `json:"client"`
}

func (s *server) listAdminUsers(w http.ResponseWriter, r *http.Request, _ auth.User) {
	users, err := s.authService.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]accessUserResponse, 0)
	for _, user := range users {
		if user.Role == auth.RoleUser {
			response = append(response, accessUserResponse{ID: user.ID, Username: user.Username, Role: user.Role})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": response})
}

func (s *server) getAdminUserDetail(w http.ResponseWriter, r *http.Request, _ auth.User) {
	user, ok := s.readManagedUser(w, r)
	if !ok {
		return
	}
	proxies, err := s.proxies.ListDistributable(r.Context())
	if err != nil {
		writeProxyError(w, err)
		return
	}
	assigned, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	assignedByProxy := make(map[int64]proxystore.Client, len(assigned))
	for _, client := range assigned {
		assignedByProxy[client.ProxyID] = client
	}
	nodes := make([]userManagementNodeResponse, 0, len(proxies))
	for _, proxy := range proxies {
		var clientResponseValue *clientResponse
		if client, exists := assignedByProxy[proxy.ID]; exists {
			value := toClientResponse(client)
			clientResponseValue = &value
		}
		nodes = append(nodes, userManagementNodeResponse{
			ProxyID: proxy.ID, ServerName: proxy.ServerName, ProxyName: proxy.Name,
			Protocol: proxy.Protocol, Client: clientResponseValue,
		})
	}
	for _, client := range assigned {
		if _, eligible := assignedByProxy[client.ProxyID]; eligible {
			found := false
			for _, node := range nodes {
				if node.ProxyID == client.ProxyID {
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		value := toClientResponse(client)
		nodes = append(nodes, userManagementNodeResponse{
			ProxyID: client.ProxyID, ServerName: client.ServerName, ProxyName: client.ProxyName,
			Protocol: client.Protocol, Client: &value,
		})
	}
	relays, err := s.relays.ListByOwner(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	relayResponse := make([]adminUserRelayResponse, 0, len(relays))
	for _, relay := range relays {
		relayResponse = append(relayResponse, toAdminUserRelayResponse(relay))
	}
	passwordRequest, err := s.authService.LatestPasswordChangeRequest(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	var pendingPasswordRequest *passwordChangeRequestResponse
	if passwordRequest != nil && passwordRequest.Status == "pending" {
		value := toPasswordChangeRequestResponse(*passwordRequest)
		pendingPasswordRequest = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  accessUserResponse{ID: user.ID, Username: user.Username, Role: user.Role},
		"nodes": nodes, "relays": relayResponse, "password_request": pendingPasswordRequest,
	})
}

func (s *server) createAdminUserNode(w http.ResponseWriter, r *http.Request, _ auth.User) {
	user, ok := s.readManagedUser(w, r)
	if !ok {
		return
	}
	var request createAssignedNodeRequest
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
	userRelayPortCount := proxystore.MaxClientRelayPorts
	if request.UserRelayPortCount != nil {
		userRelayPortCount = *request.UserRelayPortCount
	}
	value, mutation, err := s.proxies.CreateAssignedClient(r.Context(), proxystore.AssignedClientCreateInput{
		UserID: user.ID, ProxyID: request.ProxyID, Name: request.Name,
		ClientUDP443: request.ClientUDP443, Enabled: enabled, ExpiresAt: expiresAt,
		Traffic: traffic, BillingPeriodMonths: request.BillingPeriodMonths,
		UserRelayPortCount: userRelayPortCount,
	})
	if err != nil {
		if errors.Is(err, proxystore.ErrAssignedClientExists) {
			writeError(w, http.StatusConflict, "该用户已开通此节点")
			return
		}
		writeClientRelayPortError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"client": toClientResponse(value)})
}

func (s *server) readManagedUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	id, ok := readPositiveID(w, r.PathValue("id"), "用户 ID 无效")
	if !ok {
		return auth.User{}, false
	}
	user, err := s.authService.GetUser(r.Context(), id)
	if errors.Is(err, auth.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "普通用户不存在")
		return auth.User{}, false
	}
	if err != nil {
		writeInternalError(w)
		return auth.User{}, false
	}
	if user.Role != auth.RoleUser {
		writeError(w, http.StatusBadRequest, "目标账号不是普通用户")
		return auth.User{}, false
	}
	return user, true
}
