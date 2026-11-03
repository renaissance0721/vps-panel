package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type myNodeResponse struct {
	ID                  int64      `json:"id"`
	SourceType          string     `json:"source_type"`
	SourceID            int64      `json:"source_id"`
	DisplayName         string     `json:"display_name"`
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
	values, err := s.carpoolAssignments(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]myNodeResponse, 0, len(values))
	now := time.Now()
	for _, assignment := range values {
		node, value, err := s.carpoolNode(r.Context(), assignment)
		if err != nil {
			if errors.Is(err, proxystore.ErrClientNotFound) || errors.Is(err, relaystore.ErrNotFound) {
				continue
			}
			writeInternalError(w, err)
			return
		}
		lifecycle := value.LifecycleAt(now)
		status, effective := lifecycle.Status, lifecycle.EffectiveEnabled
		if assignment.SourceType == "relay" {
			relay, err := s.relays.Get(r.Context(), assignment.SourceID)
			if err != nil {
				continue
			}
			if effective && !relay.Enabled {
				status, effective = "relay_disabled", false
			} else if effective && !relayCarpoolAvailable(relay, value.ProxyID, value.Protocol) {
				status, effective = "relay_unavailable", false
			}
		}
		response = append(response, myNodeResponse{
			ID: assignment.ID, SourceType: assignment.SourceType, SourceID: assignment.SourceID, DisplayName: node.DisplayName,
			ClientID: value.ID, ClientName: value.Name, ServerName: node.ServerName, ProxyName: node.ProxyName, Protocol: value.Protocol,
			Status: status, Enabled: value.Enabled, EffectiveEnabled: effective,
			Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
			TrafficUsedBytes: proxystore.ClientUsedBytes(value.Metrics), TrafficLimitBytes: value.TrafficLimitBytes,
			ExpiresAt: value.ExpiresAt, BillingPeriodMonths: value.BillingPeriodMonths, NextResetAt: value.NextResetAt(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": response})
}

func (s *server) updateMyNode(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "节点 ID 无效")
	if !ok {
		return
	}
	assignment, err := s.carpoolAssignment(r.Context(), user.ID, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if _, err := s.proxies.GetAssignedClient(r.Context(), user.ID, assignment.ClientID); err != nil {
		writeProxyError(w, err)
		return
	}
	var request updateMyNodeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutation, err := s.proxies.UpdateClient(r.Context(), assignment.ClientID, proxystore.ClientUpdateInput{Name: &request.Name})
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
	assignment, err := s.carpoolAssignment(r.Context(), user.ID, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	client, err := s.proxies.GetAssignedClient(r.Context(), user.ID, assignment.ClientID)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	var share proxystore.ClientShare
	serverName, proxyName := client.ServerName, client.ProxyName
	if assignment.SourceType == "proxy" {
		if assignment.SourceID != client.ProxyID {
			writeError(w, http.StatusConflict, "节点不可用")
			return
		}
		share, err = s.proxies.GetClientShare(r.Context(), client.ID)
	} else if assignment.SourceType == "relay" {
		relay, relayErr := s.relays.Get(r.Context(), assignment.SourceID)
		if relayErr != nil || !relayCarpoolAvailable(relay, client.ProxyID, client.Protocol) {
			writeError(w, http.StatusConflict, "中转节点不可用")
			return
		}
		serverName, proxyName = relay.Name, client.ServerName+" · "+client.ProxyName
		share, err = s.proxies.GetClientShareAtEndpoint(r.Context(), client.ID, proxystore.ShareEndpoint{Address: relay.EntryAddress, Port: relay.ListenPort})
	} else {
		writeError(w, http.StatusConflict, "节点不可用")
		return
	}
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": map[string]any{
		"uri": share.URI, "protocol": share.Protocol,
		"server_name": serverName, "proxy_name": proxyName,
	}})
}
