package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
)

type reorderRequest struct {
	Direction string `json:"direction"`
}

func (s *server) orderRanks(ctx context.Context, userID int64, kind listorder.Kind, ids []int64) (map[int64]int, error) {
	ordered, err := s.orders.Sort(ctx, userID, kind, ids)
	if err != nil {
		return nil, err
	}
	ranks := make(map[int64]int, len(ordered))
	for index, id := range ordered {
		ranks[id] = index
	}
	return ranks, nil
}

func (s *server) reorderServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.reorder(w, r, user, listorder.Servers)
}

func (s *server) reorderProxy(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.reorder(w, r, user, listorder.Proxies)
}

func (s *server) reorderRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.reorder(w, r, user, listorder.Relays)
}

func (s *server) reorder(w http.ResponseWriter, r *http.Request, user auth.User, kind listorder.Kind) {
	id, ok := readPositiveID(w, r.PathValue("id"), "资源 ID 无效")
	if !ok {
		return
	}
	var request reorderRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Direction != "up" && request.Direction != "down" {
		writeError(w, http.StatusBadRequest, "排序方向只允许 up 或 down")
		return
	}
	archived := false
	if kind == listorder.Servers {
		var archivedAt sql.NullInt64
		err := s.db.QueryRowContext(r.Context(), "SELECT archived_at FROM servers WHERE id = ?", id).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "资源不存在")
			return
		}
		if err != nil {
			writeInternalError(w)
			return
		}
		archived = archivedAt.Valid
	}
	if err := s.orders.Move(r.Context(), user.ID, kind, archived, id, request.Direction); err != nil {
		if errors.Is(err, listorder.ErrNotFound) {
			writeError(w, http.StatusNotFound, "资源不存在")
			return
		}
		writeInternalError(w)
		return
	}
	writeNoContent(w)
}
