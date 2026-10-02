package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type subscriptionFormat string

const (
	subscriptionFormatBase64 subscriptionFormat = "base64"
	subscriptionFormatMihomo subscriptionFormat = "mihomo"
)

func (s *server) getPublicSubscription(w http.ResponseWriter, r *http.Request) {
	s.servePublicSubscription(w, r, subscriptionFormatBase64)
}

func (s *server) getPublicSubscriptionMihomo(w http.ResponseWriter, r *http.Request) {
	s.servePublicSubscription(w, r, subscriptionFormatMihomo)
}

func (s *server) getPublicSubscriptionAuto(w http.ResponseWriter, r *http.Request) {
	s.servePublicSubscription(w, r, detectSubscriptionFormat(r.UserAgent()))
}

func (s *server) getPublicPersonalSubscription(w http.ResponseWriter, r *http.Request) {
	s.servePublicPersonalSubscription(w, r, subscriptionFormatBase64)
}

func (s *server) getPublicPersonalSubscriptionMihomo(w http.ResponseWriter, r *http.Request) {
	s.servePublicPersonalSubscription(w, r, subscriptionFormatMihomo)
}

func (s *server) getPublicPersonalSubscriptionAuto(w http.ResponseWriter, r *http.Request) {
	s.servePublicPersonalSubscription(w, r, detectSubscriptionFormat(r.UserAgent()))
}

func (s *server) getPublicSubscriptionPath(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.PathValue("rest"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if parts[0] == "personal" {
		if len(parts) < 2 || len(parts) > 3 || parts[1] == "" {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("token", parts[1])
		if len(parts) == 2 {
			s.getPublicPersonalSubscription(w, r)
			return
		}
		switch parts[2] {
		case "mihomo":
			s.getPublicPersonalSubscriptionMihomo(w, r)
		case "auto":
			s.getPublicPersonalSubscriptionAuto(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	r.SetPathValue("token", parts[0])
	if len(parts) == 1 {
		s.getPublicSubscription(w, r)
		return
	}
	switch parts[1] {
	case "mihomo":
		s.getPublicSubscriptionMihomo(w, r)
	case "auto":
		s.getPublicSubscriptionAuto(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) servePublicSubscription(w http.ResponseWriter, r *http.Request, format subscriptionFormat) {
	w.Header().Set("Cache-Control", "no-store")
	value, mutations, err := s.subscriptions.GenerateSubscriptionData(r.Context(), r.PathValue("token"))
	if err != nil {
		writePublicSubscriptionError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	var body []byte
	switch format {
	case subscriptionFormatMihomo:
		body, err = subscriptionstore.RenderMihomoSubscription(value)
	default:
		body = []byte(subscriptionstore.RenderBase64Subscription(value))
	}
	if err != nil {
		writePublicSubscriptionRenderError(w, err)
		return
	}
	writeSubscriptionHeaders(w, value, format)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *server) servePublicPersonalSubscription(w http.ResponseWriter, r *http.Request, format subscriptionFormat) {
	w.Header().Set("Cache-Control", "no-store")
	value, err := s.subscriptions.GeneratePersonalSubscriptionData(r.Context(), r.PathValue("token"))
	if err != nil {
		writePublicPersonalSubscriptionError(w, err)
		return
	}
	var body []byte
	switch format {
	case subscriptionFormatMihomo:
		body, err = subscriptionstore.RenderPersonalMihomoSubscription(value)
	default:
		body = []byte(subscriptionstore.RenderResolvedBase64Subscription(value.Nodes))
	}
	if err != nil {
		writePublicSubscriptionRenderError(w, err)
		return
	}
	writeSubscriptionProfileHeaders(w, value.Title, format)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func writePublicSubscriptionRenderError(w http.ResponseWriter, err error) {
	if errors.Is(err, subscriptionstore.ErrRoutingGroupEmpty) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeInternalError(w, err)
}

func writePublicPersonalSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrPersonalSubscriptionNotFound):
		http.Error(w, "个人订阅不存在或不可用", http.StatusNotFound)
	case errors.Is(err, subscriptionstore.ErrSubscriptionUnavailable):
		http.Error(w, "个人订阅不存在或不可用", http.StatusForbidden)
	case errors.Is(err, subscriptionstore.ErrPersonalSubscriptionEmpty):
		http.Error(w, "个人订阅当前没有任何可用节点", http.StatusServiceUnavailable)
	default:
		writeInternalError(w, err)
	}
}

func writePublicSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrSubscriptionNotFound):
		http.Error(w, "订阅不存在或不可用", http.StatusNotFound)
	case errors.Is(err, subscriptionstore.ErrSubscriptionUnavailable):
		http.Error(w, "订阅不存在或不可用", http.StatusForbidden)
	default:
		writeInternalError(w, err)
	}
}

func writeSubscriptionHeaders(w http.ResponseWriter, value subscriptionstore.SubscriptionData, format subscriptionFormat) {
	writeSubscriptionProfileHeaders(w, value.Title, format)
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		value.Upload, value.Download, value.Total, value.Expire,
	))
}

func writeSubscriptionProfileHeaders(w http.ResponseWriter, title string, format subscriptionFormat) {
	contentType := "text/plain; charset=utf-8"
	if format == subscriptionFormatMihomo {
		contentType = "text/yaml; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+encodeRFC5987(title))
}

func detectSubscriptionFormat(userAgent string) subscriptionFormat {
	value := strings.ToLower(userAgent)
	for _, marker := range []string{"clash", "mihomo", "clash-verge", "clashmeta", "meta", "stash", "flclash"} {
		if strings.Contains(value, marker) {
			return subscriptionFormatMihomo
		}
	}
	return subscriptionFormatBase64
}

func encodeRFC5987(value string) string {
	var result strings.Builder
	for _, current := range []byte(value) {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' || strings.ContainsRune("!#$&+-.^_`|~", rune(current)) {
			result.WriteByte(current)
			continue
		}
		fmt.Fprintf(&result, "%%%02X", current)
	}
	return result.String()
}
