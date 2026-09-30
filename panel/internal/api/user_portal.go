package api

import (
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type myNodeResponse struct {
	ClientID            int64      `json:"client_id"`
	ClientName          string     `json:"client_name"`
	ServerName          string     `json:"server_name"`
	ProxyName           string     `json:"proxy_name"`
	Protocol            string     `json:"protocol"`
	Status              string     `json:"status"`
	Enabled             bool       `json:"enabled"`
	EffectiveEnabled    bool       `json:"effective_enabled"`
	Expired             bool       `json:"expired"`
	QuotaExhausted      bool       `json:"quota_exhausted"`
	TrafficUsedBytes    int64      `json:"traffic_used_bytes"`
	TrafficLimitBytes   *int64     `json:"traffic_limit_bytes"`
	ExpiresAt           *time.Time `json:"expires_at"`
	BillingPeriodMonths *int       `json:"billing_period_months"`
	NextResetAt         *time.Time `json:"next_reset_at"`
}

type updateMyNodeRequest struct {
	Name string `json:"name"`
}

func (s *server) listMyNodes(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]myNodeResponse, 0, len(values))
	now := time.Now()
	for _, value := range values {
		lifecycle := value.LifecycleAt(now)
		response = append(response, myNodeResponse{
			ClientID: value.ID, ClientName: value.Name, ServerName: value.ServerName, ProxyName: value.ProxyName, Protocol: value.Protocol,
			Status: lifecycle.Status, Enabled: value.Enabled, EffectiveEnabled: lifecycle.EffectiveEnabled,
			Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
			TrafficUsedBytes: proxystore.ClientUsedBytes(value.Metrics), TrafficLimitBytes: value.TrafficLimitBytes,
			ExpiresAt: value.ExpiresAt, BillingPeriodMonths: value.BillingPeriodMonths,
			NextResetAt: value.NextResetAt(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": response})
}

func (s *server) updateMyNode(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "节点 ID 无效")
	if !ok {
		return
	}
	if _, err := s.proxies.GetAssignedClient(r.Context(), user.ID, id); err != nil {
		writeProxyError(w, err)
		return
	}
	var request updateMyNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutation, err := s.proxies.UpdateClient(r.Context(), id, proxystore.ClientUpdateInput{Name: &request.Name})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusOK, map[string]any{"client": map[string]any{"id": value.ID, "name": value.Name}})
}

func (s *server) getMyNodeShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "节点 ID 无效")
	if !ok {
		return
	}
	client, err := s.proxies.GetAssignedClient(r.Context(), user.ID, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	share, err := s.proxies.GetClientShare(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": map[string]any{
		"uri": share.URI, "protocol": share.Protocol,
		"server_name": client.ServerName, "proxy_name": share.ProxyName,
	}})
}
