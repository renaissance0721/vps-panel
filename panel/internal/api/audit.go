package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/audit"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type auditLogResponse struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	ActorUserID   *int64    `json:"actor_user_id"`
	ActorUsername string    `json:"actor_username"`
	Action        string    `json:"action"`
	ResourceType  string    `json:"resource_type"`
	ResourceID    *int64    `json:"resource_id"`
	Summary       string    `json:"summary"`
	RequestID     string    `json:"request_id"`
}

func (s *server) listAuditLogs(w http.ResponseWriter, r *http.Request, _ auth.User) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	values, total, err := audit.List(r.Context(), s.db, audit.Filter{
		Action: r.URL.Query().Get("action"), User: r.URL.Query().Get("user"), Page: page, Size: size,
	})
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]auditLogResponse, 0, len(values))
	for _, value := range values {
		response = append(response, auditLogResponse{
			ID: value.ID, CreatedAt: value.CreatedAt, ActorUserID: value.ActorUserID,
			ActorUsername: value.ActorUsername, Action: value.Action, ResourceType: value.ResourceType,
			ResourceID: value.ResourceID, Summary: value.Summary, RequestID: value.RequestID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_logs": response, "total": total})
}

func (s *server) recordAudit(r *http.Request, actor auth.User, action, resourceType string, resourceID int64, summary string) {
	summary = strings.TrimSpace(summary)
	if len(summary) > 500 {
		summary = summary[:500]
	}
	actorID := actor.ID
	var resourceIDValue *int64
	if resourceID > 0 {
		resourceIDValue = &resourceID
	}
	if err := audit.Record(r.Context(), s.db, audit.Entry{
		CreatedAt: time.Now().UTC(), ActorUserID: &actorID, ActorUsername: actor.Username,
		Action: action, ResourceType: resourceType, ResourceID: resourceIDValue,
		Summary: summary, RequestID: requestIDFromContext(r.Context()),
	}); err != nil {
		log.Printf("record audit request_id=%s action=%s resource_type=%s resource_id=%d: %v",
			requestIDFromContext(r.Context()), action, resourceType, resourceID, err)
	}
}
