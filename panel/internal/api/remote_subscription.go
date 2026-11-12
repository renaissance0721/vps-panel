package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *server) importRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	if _, err := s.remoteSubscriptions.Get(r.Context(), personalActor(user), id); err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, subscriptionstore.MaxRemoteImportBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择有效的订阅文件")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeError(w, http.StatusBadRequest, "请选择有效的订阅文件")
		return
	}
	body, err := io.ReadAll(io.LimitReader(part, subscriptionstore.MaxRemoteImportBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法读取订阅文件")
		return
	}
	if len(body) > subscriptionstore.MaxRemoteImportBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "订阅文件不能超过 4 MiB")
		return
	}
	if err = part.Close(); err != nil {
		writeError(w, http.StatusBadRequest, "无法读取订阅文件")
		return
	}
	if _, err = reader.NextPart(); err != io.EOF {
		writeError(w, http.StatusBadRequest, "每次只能导入一个订阅文件")
		return
	}
	value, err := s.remoteSubscriptions.Import(r.Context(), personalActor(user), id, body)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "remote_subscription.import", "remote_subscription", id, "导入机场订阅文件")
	writeJSON(w, http.StatusOK, map[string]any{"source": value})
}

func (s *server) listRemoteSubscriptions(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.remoteSubscriptions.List(r.Context(), personalActor(user))
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": values})
}

func (s *server) getRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	value, err := s.remoteSubscriptions.Get(r.Context(), personalActor(user), id)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": value})
}

func (s *server) createRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input subscriptionstore.RemoteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.remoteSubscriptions.Create(r.Context(), personalActor(user), input)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "remote_subscription.create", "remote_subscription", value.ID, "创建机场订阅")
	writeJSON(w, http.StatusCreated, map[string]any{"source": value})
}

func (s *server) updateRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	var input subscriptionstore.RemoteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.remoteSubscriptions.Update(r.Context(), personalActor(user), id, input)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "remote_subscription.update", "remote_subscription", id, "更新机场订阅")
	writeJSON(w, http.StatusOK, map[string]any{"source": value})
}

func (s *server) deleteRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	if err := s.remoteSubscriptions.Delete(r.Context(), personalActor(user), id); err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "remote_subscription.delete", "remote_subscription", id, "删除机场订阅")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) testRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	var input struct {
		URL       string `json:"url"`
		UserAgent string `json:"user_agent"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.remoteSubscriptions.Test(r.Context(), personalActor(user), input.URL, input.UserAgent)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": value})
}

func (s *server) refreshRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	value, err := s.remoteSubscriptions.Refresh(r.Context(), personalActor(user), id)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "remote_subscription.refresh", "remote_subscription", id, "刷新机场订阅")
	writeJSON(w, http.StatusOK, map[string]any{"source": value})
}

func (s *server) testSavedRemoteSubscription(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	var input struct {
		UserAgent string `json:"user_agent"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.remoteSubscriptions.TestSaved(r.Context(), personalActor(user), id, input.UserAgent)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": value})
}

func (s *server) remoteSubscriptionNodes(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "机场订阅 ID 无效")
	if !ok {
		return
	}
	values, err := s.remoteSubscriptions.Nodes(r.Context(), personalActor(user), id)
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": values})
}

func writeRemoteSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptionstore.ErrRemoteNotFound):
		writeError(w, http.StatusNotFound, "机场订阅不存在")
	case errors.Is(err, subscriptionstore.ErrRemoteInvalid):
		writeError(w, http.StatusBadRequest, "机场订阅设置无效")
	case errors.Is(err, subscriptionstore.ErrRemoteImport), errors.Is(err, subscriptionstore.ErrRemoteManual):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, subscriptionstore.ErrRemoteBusy):
		writeError(w, http.StatusTooManyRequests, "机场订阅正在刷新或请求过于频繁")
	case errors.Is(err, subscriptionstore.ErrRemoteChanged):
		writeError(w, http.StatusConflict, "机场订阅已修改，请重新刷新")
	case errors.Is(err, subscriptionstore.ErrRemoteKey):
		writeError(w, http.StatusServiceUnavailable, "机场订阅加密密钥不可用")
	default:
		if message, ok := subscriptionstore.RemoteFailureMessage(err); ok {
			writeError(w, http.StatusBadGateway, message)
			return
		}
		// Do not log or return upstream and database error strings: they may contain secrets.
		writeError(w, http.StatusInternalServerError, "机场订阅操作失败")
	}
}
