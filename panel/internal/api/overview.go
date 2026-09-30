package api

import (
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/operation"
)

type overviewUser struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (s *server) overview(w http.ResponseWriter, r *http.Request, user auth.User) {
	accounts, err := s.authService.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	servers, err := s.servers.ListForUser(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	proxies, err := s.proxies.List(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	pendingOperations, failedOperations, err := operation.CountsForUser(r.Context(), s.db, user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	visibleServers := make(map[int64]bool, len(servers))
	for _, value := range servers {
		visibleServers[value.ID] = true
	}
	proxyCount := 0
	for _, value := range proxies {
		if visibleServers[value.ServerID] {
			proxyCount++
		}
	}
	users := make([]overviewUser, 0, len(accounts))
	for _, account := range accounts {
		users = append(users, overviewUser{Username: account.Username, Role: account.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_count":            len(servers),
		"proxy_count":             proxyCount,
		"pending_operation_count": pendingOperations,
		"failed_operation_count":  failedOperations,
		"users":                   users,
	})
}
