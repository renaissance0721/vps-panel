package api

import (
	"context"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

type emailPasswordResetRequest struct {
	Identifier string `json:"identifier"`
}

type emailPasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (s *server) requestEmailPasswordReset(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var request emailPasswordResetRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	settings, err := s.mail.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !settings.Enabled {
		writeError(w, http.StatusConflict, "管理员尚未启用邮件服务，请使用管理员审核方式")
		return
	}
	baseURL, ok := s.trustedEmailBaseURL()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "管理员尚未配置可信 Panel 域名，请使用管理员审核方式")
		return
	}
	if allowed, retryAfter := s.emailResetLimiter.Take(clientIP(r), request.Identifier, time.Now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int((retryAfter+time.Second-1)/time.Second))))
		writeError(w, http.StatusTooManyRequests, "密码重置邮件申请过于频繁，请稍后再试")
		return
	}
	verification, err := s.authService.PreparePasswordResetEmail(r.Context(), request.Identifier)
	if err != nil {
		// Never expose account-dependent preparation failures to the public API.
		log.Printf("password reset email preparation failed request_id=%s", requestIDFromContext(r.Context()))
	} else if verification != nil {
		resetURL := baseURL + "/reset-password?token=" + url.QueryEscape(verification.Token)
		if err := s.mail.Send(r.Context(), passwordResetEmailMessage(verification.Target, resetURL)); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cleanupErr := s.authService.CancelPasswordResetEmail(ctx, verification.ID, verification.UserID)
			cancel()
			// Do not log sender error strings, which may echo private message data.
			log.Printf("password reset email delivery failed request_id=%s token_cancelled=%t", requestIDFromContext(r.Context()), cleanupErr == nil)
		} else {
			s.recordAudit(r, auth.User{ID: verification.UserID, Username: verification.Username},
				"account.password_reset.email_requested", "account", verification.UserID, "邮箱="+maskEmail(verification.Target))
		}
	}
	// Reduce fast-path timing differences without holding a transaction or
	// pretending that SMTP network latency can be made constant-time.
	if remaining := 200*time.Millisecond - time.Since(started); remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *server) confirmEmailPasswordReset(w http.ResponseWriter, r *http.Request) {
	var request emailPasswordResetConfirm
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.authService.ResetPasswordByEmail(r.Context(), request.Token, request.NewPassword)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordResetTokenInvalid) {
			writeError(w, http.StatusBadRequest, "密码重置链接无效或已过期")
		} else {
			writePasswordRequestError(w, err)
		}
		return
	}
	s.recordAudit(r, user, "account.password_reset.email_completed", "account", user.ID, "通过已验证邮箱重置密码")
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func passwordResetEmailMessage(target, resetURL string) mailservice.Message {
	return mailservice.Message{
		To: []string{target}, Subject: "重置你的 VPS Panel 密码",
		Text: "你正在请求重置 VPS Panel 账号密码。\n\n请点击以下链接：\n" + resetURL +
			"\n\n链接将在 30 分钟后失效。\n如果不是你本人操作，可以忽略此邮件。",
		HTML: "<p>你正在请求重置 VPS Panel 账号密码。</p><p><a href=\"" + html.EscapeString(resetURL) +
			"\">重置密码</a></p><p>链接将在 30 分钟后失效。</p><p>如果不是你本人操作，可以忽略此邮件。</p>",
	}
}
