package api

import (
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type overviewUser struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (s *server) overview(w http.ResponseWriter, r *http.Request, user auth.User) {
	accounts, err := s.authService.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	servers, err := s.servers.ListForUser(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	proxies, err := s.proxies.List(r.Context())
	if err != nil {
		writeInternalError(w)
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
		"server_count": len(servers),
		"proxy_count":  proxyCount,
		"users":        users,
	})
}
