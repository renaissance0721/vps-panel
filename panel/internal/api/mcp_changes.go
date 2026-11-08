package api

import (
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"net/http"
)

// mcpChangeEvents keeps the previous approval history readable; plans cannot be executed.
func (s *server) mcpChangeEvents(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT e.plan_id,e.event,e.created_at,e.server_id,e.actor_user_id,e.request_id,t.name
	 FROM mcp_change_events e JOIN mcp_change_plans p ON p.id=e.plan_id JOIN mcp_tokens t ON t.id=e.token_id
	 JOIN servers sv ON sv.id=e.server_id
	 WHERE p.owner_user_id=? AND sv.archived_at IS NULL AND (sv.visibility='public' OR EXISTS(SELECT 1 FROM server_access a WHERE a.server_id=sv.id AND a.user_id=?))
	 ORDER BY e.id DESC LIMIT 100`, user.ID, user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var planID, event, requestID, tokenName string
		var at, serverID, actorID int64
		if err := rows.Scan(&planID, &event, &at, &serverID, &actorID, &requestID, &tokenName); err != nil {
			writeInternalError(w, err)
			return
		}
		items = append(items, map[string]any{"plan_id": planID, "event": event, "created_at": at, "server_id": serverID, "actor_user_id": actorID, "request_id": requestID, "token_name": tokenName})
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}
