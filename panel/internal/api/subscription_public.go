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
		writeInternalError(w, err)
		return
	}
	writeSubscriptionHeaders(w, value, format)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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
	extension := ".txt"
	contentType := "text/plain; charset=utf-8"
	if format == subscriptionFormatMihomo {
		extension = ".yaml"
		contentType = "text/yaml; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		value.Upload, value.Download, value.Total, value.Expire,
	))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(value.Title)))
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+encodeRFC5987(value.Title)+extension)
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
