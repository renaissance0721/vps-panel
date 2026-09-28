package api

import (
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

func (s *server) getSubscriberMe(w http.ResponseWriter, r *http.Request, user auth.User) {
	mutations, err := s.subscriptions.ReconcileSubscriber(r.Context(), user.ID)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	value, err := s.subscriptions.GetSubscriber(r.Context(), user.ID)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriber": toSubscriptionUserResponse(value, baseURL, true)})
}

func (s *server) listSubscriberNodes(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.subscriptions.ListSubscriberNodes(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	nodes := make([]map[string]any, 0, len(values))
	for _, value := range values {
		nodes = append(nodes, map[string]any{
			"id": value.ID, "name": value.Name, "mode": value.Mode, "enabled": value.Enabled,
			"traffic_multiplier": float64(value.TrafficMultiplierBP) / 100,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (s *server) regenerateSubscriberToken(w http.ResponseWriter, r *http.Request, user auth.User) {
	tokenValue, err := s.subscriptions.RegenerateSubscriptionToken(r.Context(), user.ID)
	if err != nil {
		writeSubscriptionUserError(w, err)
		return
	}
	baseURL, ok := s.panelBaseURL(r)
	if !ok {
		writeInternalError(w)
		return
	}
	urls := buildSubscriptionURLs(baseURL, tokenValue)
	writeJSON(w, http.StatusOK, map[string]any{
		"subscription_token":      tokenValue,
		"subscription_url":        urls.Base64,
		"subscription_base64_url": urls.Base64,
		"subscription_mihomo_url": urls.Mihomo,
		"subscription_auto_url":   urls.Auto,
	})
}
