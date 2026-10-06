package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

const (
	mailTestInterval = 10 * time.Second
	mailTestTimeout  = 35 * time.Second
)

type mailTestLimiter struct {
	mu       sync.Mutex
	lastTest map[int64]time.Time
	now      func() time.Time
}

func newMailTestLimiter() *mailTestLimiter {
	return &mailTestLimiter{lastTest: make(map[int64]time.Time), now: time.Now}
}

func (l *mailTestLimiter) Allow(userID int64) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if previous := l.lastTest[userID]; !previous.IsZero() {
		remaining := mailTestInterval - now.Sub(previous)
		if remaining > 0 {
			return false, remaining
		}
	}
	l.lastTest[userID] = now
	return true, 0
}

func (s *server) getMailSettings(w http.ResponseWriter, r *http.Request, _ auth.User) {
	value, err := s.mail.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *server) saveMailSettings(w http.ResponseWriter, r *http.Request, actor auth.User) {
	var input mailservice.Update
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.mail.Save(r.Context(), input)
	if err != nil {
		writeMailError(w, err)
		return
	}
	s.recordAudit(r, actor, "mail.settings.update", "mail_settings", 0, mailAuditSummary(value))
	writeJSON(w, http.StatusOK, value)
}

func (s *server) testMailSettings(w http.ResponseWriter, r *http.Request, actor auth.User) {
	allowed, retryAfter := s.mailTestLimiter.Allow(actor.ID)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
		writeError(w, http.StatusTooManyRequests, "测试邮件发送过于频繁，请稍后重试")
		return
	}
	var input mailservice.TestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mailTestTimeout)
	defer cancel()
	if err := s.mail.SendTest(ctx, input); err != nil {
		writeMailError(w, err)
		return
	}
	settings := mailservice.Settings{
		Enabled: input.Enabled, Host: input.Host, Port: input.Port, Security: input.Security,
		Username: input.Username, FromAddress: input.FromAddress, FromName: input.FromName, ReplyTo: input.ReplyTo,
	}
	s.recordAudit(r, actor, "mail.test", "mail_settings", 0, mailAuditSummary(settings))
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func writeMailError(w http.ResponseWriter, err error) {
	var delivery *mailservice.DeliveryError
	switch {
	case errors.Is(err, mailservice.ErrInvalidSettings):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, mailservice.ErrCredentialUnavailable):
		writeError(w, http.StatusUnprocessableEntity, mailservice.ErrCredentialUnavailable.Error())
	case errors.Is(err, mailservice.ErrDisabled):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &delivery):
		writeError(w, http.StatusBadGateway, delivery.Error())
	default:
		writeInternalError(w, err)
	}
}

func mailAuditSummary(value mailservice.Settings) string {
	return fmt.Sprintf("enabled=%t host=%s port=%d security=%s from=%s",
		value.Enabled, value.Host, value.Port, value.Security, value.FromAddress)
}
