package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type renameAccountRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
}

func (s *server) renameMyAccount(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request renameAccountRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	updated, err := s.authService.RenameUser(r.Context(), user.ID, request.CurrentPassword, request.Username)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(updated)})
}

func (s *server) changeMyPassword(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request passwordChangeRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	if user.Role == auth.RoleAdmin {
		if err := s.authService.ChangePassword(r.Context(), user.ID, request.CurrentPassword, request.NewPassword); err != nil {
			writeAccountError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "changed"})
		return
	}
	created, err := s.authService.RequestPasswordChange(r.Context(), user.ID, request.CurrentPassword, request.NewPassword)
	if err != nil {
		writePasswordRequestError(w, err)
		return
	}
	created.Username = user.Username
	created.Role = user.Role
	writeJSON(w, http.StatusCreated, map[string]any{"request": toPasswordChangeRequestResponse(created)})
}

func (s *server) deleteAdminUser(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "用户 ID 无效")
	if !ok {
		return
	}
	mutations, err := s.authService.DeleteUser(r.Context(), id)
	if err != nil {
		if !errors.Is(err, auth.ErrUserNotFound) && !errors.Is(err, auth.ErrCannotDeleteAdmin) {
			log.Printf("delete user %d failed: %v", id, err)
		}
		writeAccountError(w, err)
		return
	}
	s.notifyProxyMutations(mutations)
	writeNoContent(w)
}

func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "当前密码错误")
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "用户名需为 3–64 位字母、数字、点、下划线或连字符")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "用户名已存在")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "新密码长度需为 10–72 字节")
	case errors.Is(err, auth.ErrPasswordUnchanged):
		writeError(w, http.StatusBadRequest, "新密码不能与当前密码相同")
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "用户不存在")
	case errors.Is(err, auth.ErrCannotDeleteAdmin):
		writeError(w, http.StatusBadRequest, "不能删除管理员账号")
	default:
		writeInternalError(w)
	}
}
