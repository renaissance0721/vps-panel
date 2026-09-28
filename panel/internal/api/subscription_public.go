package api

import (
	"errors"
	"fmt"
	"net/http"

	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *server) getPublicSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	value, mutations, err := s.subscriptions.GenerateSubscription(r.Context(), r.PathValue("token"))
	if err != nil {
		switch {
		case errors.Is(err, subscriptionstore.ErrSubscriptionNotFound):
			http.Error(w, "订阅不存在或不可用", http.StatusNotFound)
		case errors.Is(err, subscriptionstore.ErrSubscriptionUnavailable):
			http.Error(w, "订阅不存在或不可用", http.StatusForbidden)
		default:
			http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		}
		return
	}
	s.notifyProxyMutations(mutations)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		value.Upload, value.Download, value.Total, value.Expire,
	))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value.Body))
}
