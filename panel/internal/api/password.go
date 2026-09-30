package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type passwordChangeRequestBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type passwordResetRequestBody struct {
	Username    string `json:"username"`
	NewPassword string `json:"new_password"`
}

type passwordChangeRequestResponse struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Username   string     `json:"username"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at"`
}

func toPasswordChangeRequestResponse(value auth.PasswordChangeRequest) passwordChangeRequestResponse {
	return passwordChangeRequestResponse{
		ID: value.ID, UserID: value.UserID, Username: value.Username, Role: value.Role, Status: value.Status,
		CreatedAt: value.CreatedAt, ReviewedAt: value.ReviewedAt,
	}
}

func (s *server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var request passwordResetRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if allowed, retryAfter := s.passwordResetLimiter.Allow(ip, request.Username, now); !allowed {
		seconds := max(1, int((retryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "密码重置申请过于频繁，请稍后再试")
		return
	}
	s.passwordResetLimiter.RecordFailure(ip, request.Username, now)
	if err := s.authService.RequestPasswordResetByUsername(r.Context(), request.Username, request.NewPassword); err != nil {
		writePasswordRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *server) requestMyPasswordReset(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request passwordResetRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	created, err := s.authService.RequestPasswordResetForUser(r.Context(), user.ID, request.NewPassword)
	if err != nil {
		writePasswordRequestError(w, err)
		return
	}
	created.Username = user.Username
	created.Role = user.Role
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "request": toPasswordChangeRequestResponse(created)})
}

func (s *server) listPasswordChangeRequests(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.authService.ListPendingPasswordChangeRequests(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]passwordChangeRequestResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toPasswordChangeRequestResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": response})
}

func (s *server) approvePasswordChangeRequest(w http.ResponseWriter, r *http.Request, admin auth.User) {
	s.reviewPasswordChangeRequest(w, r, admin, true)
}

func (s *server) rejectPasswordChangeRequest(w http.ResponseWriter, r *http.Request, admin auth.User) {
	s.reviewPasswordChangeRequest(w, r, admin, false)
}

func (s *server) reviewPasswordChangeRequest(w http.ResponseWriter, r *http.Request, admin auth.User, approve bool) {
	id, ok := readPositiveID(w, r.PathValue("id"), "密码申请 ID 无效")
	if !ok {
		return
	}
	if err := s.authService.ReviewPasswordChangeRequest(r.Context(), id, admin.ID, approve); err != nil {
		writePasswordRequestError(w, err)
		return
	}
	action := "password_reset.reject"
	summary := "拒绝密码重置申请"
	if approve {
		action = "password_reset.approve"
		summary = "批准密码重置申请"
	}
	s.recordAudit(r, admin, action, "password_change_request", id, summary)
	writeNoContent(w)
}

func writePasswordRequestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "当前密码错误")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "新密码长度需为 10–72 字节")
	case errors.Is(err, auth.ErrPasswordUnchanged):
		writeError(w, http.StatusBadRequest, "新密码不能与当前密码相同")
	case errors.Is(err, auth.ErrPasswordRequestPending):
		writeError(w, http.StatusConflict, "已有待审核的密码重置申请")
	case errors.Is(err, auth.ErrPasswordRequestNotFound):
		writeError(w, http.StatusNotFound, "密码重置申请不存在或已审核")
	case errors.Is(err, auth.ErrPasswordRequestSelfReview):
		writeError(w, http.StatusForbidden, "管理员不能审核自己的密码重置申请")
	default:
		writeInternalError(w, err)
	}
}
