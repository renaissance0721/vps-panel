package api

import (
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

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
			ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
			TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
			TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
			TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
			CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": response})
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
