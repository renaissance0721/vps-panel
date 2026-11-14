package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func (s *server) getChinaInboundWhitelist(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok || !s.requireServerAccess(w, r, user, id) {
		return
	}
	entries, err := s.servers.ChinaWhitelist(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *server) setChinaInboundWhitelist(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok || !s.requireServerAccess(w, r, user, id) || !s.requireMutableServer(w, r, id) {
		return
	}
	var request struct {
		Entries []serverstore.ChinaWhitelistEntry `json:"entries"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Entries == nil {
		writeError(w, http.StatusBadRequest, "请提供白名单列表")
		return
	}
	updated, version, changed, err := s.servers.UpdateChinaWhitelist(r.Context(), id, request.Entries)
	if errors.Is(err, serverstore.ErrInvalidChinaWhitelist) {
		writeError(w, http.StatusBadRequest, "白名单地址无效、重复或超过数量限制（最多 128 项）")
		return
	}
	if err != nil {
		writeServerError(w, err)
		return
	}
	if changed {
		if err := s.agents.NotifyConfigChanged(id, version); err != nil {
			log.Printf("notify Agent of China whitelist change: %v", err)
		}
		s.recordAudit(r, user, "server.update", "server", id, "更新中国入站白名单")
	}
	entries, err := s.servers.ChinaWhitelist(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "server": toServerResponse(updated, s.panelVersion)})
}
