package api

import (
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type myNodeResponse struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
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

func (s *server) listMyNodes(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]myNodeResponse, 0, len(values))
	now := time.Now()
	for _, value := range values {
		lifecycle := value.LifecycleAt(now)
		response = append(response, myNodeResponse{
			ID: value.ID, Name: value.Name, ProxyName: value.ProxyName, Protocol: value.Protocol,
			Status: lifecycle.Status, Enabled: value.Enabled, EffectiveEnabled: lifecycle.EffectiveEnabled,
			Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
			TrafficUsedBytes: proxystore.ClientUsedBytes(value.Metrics), TrafficLimitBytes: value.TrafficLimitBytes,
			ExpiresAt: value.ExpiresAt, BillingPeriodMonths: value.BillingPeriodMonths,
			NextResetAt: value.NextResetAt(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": response})
}

func (s *server) getMyNodeShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "节点 ID 无效")
	if !ok {
		return
	}
	if _, err := s.proxies.GetAssignedClient(r.Context(), user.ID, id); err != nil {
		writeProxyError(w, err)
		return
	}
	share, err := s.proxies.GetClientShare(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": map[string]any{
		"uri": share.URI, "protocol": share.Protocol, "name": share.Name, "proxy_name": share.ProxyName,
	}})
}
