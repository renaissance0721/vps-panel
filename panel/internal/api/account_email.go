package api

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

type accountEmailResponse struct {
	Email             string `json:"email"`
	Verified          bool   `json:"verified"`
	PendingEmail      string `json:"pending_email"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type accountEmailRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

type accountEmailResendRequest struct {
	CurrentPassword string `json:"current_password"`
}

type verifyAccountEmailRequest struct {
	Token string `json:"token"`
}

func (s *server) getMyEmail(w http.ResponseWriter, r *http.Request, user auth.User) {
	status, err := s.authService.GetEmailStatus(r.Context(), user.ID)
	if err != nil {
		writeAccountEmailError(w, err)
		return
	}
	response := accountEmailResponse{
		Email: status.Email, Verified: status.Verified, PendingEmail: status.PendingEmail,
	}
	settings, err := s.mail.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	switch {
	case !settings.Enabled:
		response.UnavailableReason = "管理员尚未启用邮件服务"
	case !s.hasTrustedEmailBaseURL():
		response.UnavailableReason = "管理员尚未配置可信 Panel 域名"
	default:
		response.Available = true
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) requestMyEmailVerification(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request accountEmailRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	s.sendAccountEmailVerification(w, r, user, request.Email, request.CurrentPassword)
}

func (s *server) resendMyEmailVerification(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request accountEmailResendRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	status, err := s.authService.GetEmailStatus(r.Context(), user.ID)
	if err != nil {
		writeAccountEmailError(w, err)
		return
	}
	if status.PendingEmail == "" {
		writeAccountEmailError(w, auth.ErrPendingEmailNotFound)
		return
	}
	s.sendAccountEmailVerification(w, r, user, status.PendingEmail, request.CurrentPassword)
}

func (s *server) sendAccountEmailVerification(w http.ResponseWriter, r *http.Request, user auth.User, email, currentPassword string) {
	settings, err := s.mail.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !settings.Enabled {
		writeError(w, http.StatusConflict, "管理员尚未启用邮件服务")
		return
	}
	baseURL, ok := s.trustedEmailBaseURL()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "管理员尚未配置可信 Panel 域名")
		return
	}
	verification, err := s.authService.PrepareEmailVerification(r.Context(), user.ID, email, currentPassword)
	if err != nil {
		writeAccountEmailError(w, err)
		return
	}
	verificationURL := baseURL + "/verify-email?token=" + url.QueryEscape(verification.Token)
	message := accountEmailVerificationMessage(verification.Target, verificationURL)
	if err := s.mail.Send(r.Context(), message); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := s.authService.CancelEmailVerification(cleanupContext, verification.ID, user.ID)
		cancel()
		if cleanupErr != nil {
			writeInternalError(w, errors.Join(err, cleanupErr))
			return
		}
		writeMailError(w, err)
		return
	}
	action := "account.email.verification_requested"
	if verification.Purpose == "change_email" {
		action = "account.email.change_requested"
	}
	s.recordAudit(r, user, action, "account", user.ID, "邮箱="+maskEmail(verification.Target))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
}

func (s *server) verifyAccountEmail(w http.ResponseWriter, r *http.Request) {
	var request verifyAccountEmailRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.authService.VerifyEmail(r.Context(), request.Token)
	if err != nil {
		writeAccountEmailError(w, err)
		return
	}
	action := "account.email.verified"
	if result.Changed {
		action = "account.email.changed"
	}
	s.recordAudit(r, auth.User{ID: result.UserID, Username: result.Username}, action, "account", result.UserID,
		"邮箱="+maskEmail(result.Email))
	writeJSON(w, http.StatusOK, map[string]string{"status": "verified"})
}

func (s *server) trustedEmailBaseURL() (string, bool) {
	domain := s.backup.Domain
	if domain == "" || domain == ":80" || !validPanelDomain(domain) {
		return "", false
	}
	return "https://" + domain, true
}

func (s *server) hasTrustedEmailBaseURL() bool {
	_, ok := s.trustedEmailBaseURL()
	return ok
}

func accountEmailVerificationMessage(target, verificationURL string) mailservice.Message {
	escapedURL := html.EscapeString(verificationURL)
	return mailservice.Message{
		To:      []string{target},
		Subject: "验证你的 VPS Panel 邮箱",
		Text: "你正在为 VPS Panel 账号绑定此邮箱。\n\n请点击下面的链接完成验证：\n" + verificationURL +
			"\n\n此链接将在 30 分钟后失效。\n\n如果不是你本人操作，可以忽略此邮件。",
		HTML: "<p>你正在为 VPS Panel 账号绑定此邮箱。</p>" +
			"<p><a href=\"" + escapedURL + "\">验证邮箱</a></p>" +
			"<p>此链接将在 30 分钟后失效。</p>" +
			"<p>如果不是你本人操作，可以忽略此邮件。</p>",
	}
}

func maskEmail(value string) string {
	local, domain, ok := strings.Cut(value, "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}
	first, _ := utf8.DecodeRuneInString(local)
	return string(first) + "***@" + domain
}

func writeAccountEmailError(w http.ResponseWriter, err error) {
	var rateLimit *auth.EmailRateLimitError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "当前密码错误")
	case errors.Is(err, auth.ErrInvalidEmail):
		writeError(w, http.StatusBadRequest, "邮箱地址无效")
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, http.StatusConflict, "该邮箱已绑定其他账号")
	case errors.Is(err, auth.ErrEmailUnchanged):
		writeError(w, http.StatusConflict, "新邮箱不能与当前邮箱相同")
	case errors.Is(err, auth.ErrPendingEmailNotFound):
		writeError(w, http.StatusNotFound, "没有待验证邮箱")
	case errors.Is(err, auth.ErrEmailVerificationInvalid):
		writeError(w, http.StatusBadRequest, "验证链接无效或已过期")
	case errors.As(err, &rateLimit):
		seconds := max(1, int(math.Ceil(rateLimit.RetryAfter.Seconds())))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "验证邮件发送过于频繁，请稍后重试")
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "用户不存在")
	default:
		writeInternalError(w, fmt.Errorf("account email operation: %w", err))
	}
}
