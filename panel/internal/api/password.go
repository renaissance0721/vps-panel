package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type passwordChangeRequestBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type passwordChangeRequestResponse struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Username   string     `json:"username"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at"`
}

func toPasswordChangeRequestResponse(value auth.PasswordChangeRequest) passwordChangeRequestResponse {
	return passwordChangeRequestResponse{
		ID: value.ID, UserID: value.UserID, Username: value.Username, Status: value.Status,
		CreatedAt: value.CreatedAt, ReviewedAt: value.ReviewedAt,
	}
}

func (s *server) createMyPasswordChangeRequest(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request passwordChangeRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	created, err := s.authService.RequestPasswordChange(r.Context(), user.ID, request.CurrentPassword, request.NewPassword)
	if err != nil {
		writePasswordRequestError(w, err)
		return
	}
	created.Username = user.Username
	writeJSON(w, http.StatusCreated, map[string]any{"request": toPasswordChangeRequestResponse(created)})
}

func (s *server) getMyPasswordChangeRequest(w http.ResponseWriter, r *http.Request, user auth.User) {
	value, err := s.authService.LatestPasswordChangeRequest(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	if value == nil {
		writeJSON(w, http.StatusOK, map[string]any{"request": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": toPasswordChangeRequestResponse(*value)})
}

func (s *server) listPasswordChangeRequests(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.authService.ListPendingPasswordChangeRequests(r.Context())
	if err != nil {
		writeInternalError(w)
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
		writeError(w, http.StatusConflict, "已有待审核的密码修改申请")
	case errors.Is(err, auth.ErrPasswordRequestNotFound):
		writeError(w, http.StatusNotFound, "密码修改申请不存在或已审核")
	default:
		writeInternalError(w)
	}
}
