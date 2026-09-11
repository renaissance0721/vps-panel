package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

const sessionCookieName = "vps_panel_session"

type server struct {
	db            *sql.DB
	authService   *auth.Service
	servers       *serverstore.Service
	webRoot       string
	connectionsMu sync.Mutex
	connections   map[int64]map[*websocket.Conn]struct{}
}

func NewHandler(db *sql.DB, webRoot string) http.Handler {
	s := &server{
		db:          db,
		authService: auth.NewService(db),
		servers:     serverstore.NewService(db),
		webRoot:     webRoot,
		connections: make(map[int64]map[*websocket.Conn]struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/initialize", s.initialize)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/agent/register", s.registerAgent)
	mux.HandleFunc("GET /api/agent/ws", s.agentWebSocket)
	mux.HandleFunc("GET /api/admin/invitations", s.requireAdmin(s.listInvitations))
	mux.HandleFunc("POST /api/admin/invitations", s.requireAdmin(s.createInvitation))
	mux.HandleFunc("DELETE /api/admin/invitations/{id}", s.requireAdmin(s.revokeInvitation))
	mux.HandleFunc("GET /api/servers", s.requireAuthentication(s.listServers))
	mux.HandleFunc("POST /api/servers", s.requireAuthentication(s.createServer))
	mux.HandleFunc("GET /api/servers/{id}", s.requireAuthentication(s.getServer))
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuthentication(s.deleteServer))
	mux.HandleFunc("POST /api/servers/{id}/enrollment", s.requireAdmin(s.createEnrollment))
	mux.HandleFunc("DELETE /api/servers/{id}/permanent", s.requireAdmin(s.permanentlyDeleteServer))
	mux.HandleFunc("GET /install-agent.sh", s.installAgent)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.Handle("/", s.spa())
	return mux
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registrationRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type invitationResponse struct {
	ID                int64     `json:"id"`
	CreatedBy         int64     `json:"created_by"`
	CreatedByUsername string    `json:"created_by_username"`
	ExpiresAt         time.Time `json:"expires_at"`
	CreatedAt         time.Time `json:"created_at"`
	Token             string    `json:"token,omitempty"`
}

type createServerRequest struct {
	Name string `json:"name"`
}

type serverResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type createdServerResponse struct {
	Server                   serverResponse `json:"server"`
	EnrollmentToken          string         `json:"enrollment_token"`
	EnrollmentTokenExpiresAt time.Time      `json:"enrollment_token_expires_at"`
	AgentInstallationCommand string         `json:"agent_installation_command"`
}

type agentRegistrationRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	AgentVersion    string `json:"agent_version"`
	ExistingConfig  bool   `json:"existing_config"`
}

type agentRegistrationResponse struct {
	AgentID    int64  `json:"agent_id"`
	ServerID   int64  `json:"server_id"`
	AgentToken string `json:"agent_token"`
}

func (s *server) authState(w http.ResponseWriter, r *http.Request) {
	requiresInitialization, err := s.authService.NeedsInitialization(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}

	response := struct {
		RequiresInitialization bool          `json:"requires_initialization"`
		Authenticated          bool          `json:"authenticated"`
		User                   *userResponse `json:"user,omitempty"`
	}{RequiresInitialization: requiresInitialization}

	if token := readSessionToken(r); token != "" {
		user, err := s.authService.Authenticate(r.Context(), token)
		if err == nil {
			value := toUserResponse(user)
			response.Authenticated = true
			response.User = &value
		} else if !errors.Is(err, auth.ErrUnauthenticated) {
			writeInternalError(w)
			return
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *server) initialize(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.authService.Initialize(r.Context(), request.Username, request.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.startSession(w, r, user, http.StatusCreated)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.authService.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.startSession(w, r, user, http.StatusOK)
}

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	var request registrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.authService.RegisterWithInvitation(
		r.Context(),
		request.Token,
		request.Username,
		request.Password,
	)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.startSession(w, r, user, http.StatusCreated)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.authService.Logout(r.Context(), readSessionToken(r)); err != nil {
		writeInternalError(w)
		return
	}
	clearSessionCookie(w, secureRequest(r))
	writeNoContent(w)
}

func (s *server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var request agentRegistrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	registered, err := s.servers.RegisterAgent(
		r.Context(), request.EnrollmentToken, request.AgentVersion, request.ExistingConfig,
	)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentRegistrationResponse{
		AgentID:    registered.ID,
		ServerID:   registered.ServerID,
		AgentToken: registered.Token,
	})
}

func (s *server) agentWebSocket(w http.ResponseWriter, r *http.Request) {
	authorization := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return
	}

	agent, err := s.servers.AuthenticateAgent(r.Context(), authorization[1])
	if errors.Is(err, serverstore.ErrInvalidAgentToken) {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	s.trackAgentConnection(agent.ServerID, connection)

	statusContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.servers.SetAgentOnline(statusContext, agent.ServerID)
	cancel()
	if err != nil {
		s.untrackAgentConnection(agent.ServerID, connection)
		if errors.Is(err, serverstore.ErrArchived) || errors.Is(err, serverstore.ErrNotFound) {
			return
		}
		log.Printf("set agent %d server %d online: %v", agent.ID, agent.ServerID, err)
		_ = connection.Close(websocket.StatusInternalError, "server status update failed")
		return
	}
	log.Printf("agent %d connected to server %d", agent.ID, agent.ServerID)

	disconnected := connection.CloseRead(context.Background())
	<-disconnected.Done()
	if !s.untrackAgentConnection(agent.ServerID, connection) {
		return
	}

	statusContext, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = s.servers.SetAgentOffline(statusContext, agent.ServerID)
	cancel()
	if err != nil {
		if errors.Is(err, serverstore.ErrNotFound) {
			return
		}
		log.Printf("set agent %d server %d offline: %v", agent.ID, agent.ServerID, err)
		return
	}
	log.Printf("agent %d disconnected from server %d", agent.ID, agent.ServerID)
}

func (s *server) listInvitations(w http.ResponseWriter, r *http.Request, _ auth.User) {
	invitations, err := s.authService.ListActiveInvitations(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]invitationResponse, 0, len(invitations))
	for _, invitation := range invitations {
		response = append(response, toInvitationResponse(invitation))
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": response})
}

func (s *server) createInvitation(w http.ResponseWriter, r *http.Request, user auth.User) {
	invitation, err := s.authService.CreateInvitation(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	response := toInvitationResponse(invitation.Invitation)
	response.CreatedByUsername = user.Username
	response.Token = invitation.Token
	writeJSON(w, http.StatusCreated, response)
}

func (s *server) revokeInvitation(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "邀请 ID 无效")
		return
	}
	if err := s.authService.RevokeInvitation(r.Context(), id); err != nil {
		writeAuthError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *server) listServers(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var values []serverstore.Server
	var err error
	if r.URL.Query().Get("archived") == "true" {
		values, err = s.servers.ListArchived(r.Context())
	} else {
		values, err = s.servers.List(r.Context())
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]serverResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toServerResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": response})
}

func (s *server) createServer(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var request createServerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	created, err := s.servers.Create(r.Context(), request.Name)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCreatedServerResponse(created, requestBaseURL(r)))
}

func (s *server) getServer(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	value, err := s.servers.Get(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(value)})
}

func (s *server) deleteServer(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if err := s.servers.Archive(r.Context(), id); err != nil {
		writeServerError(w, err)
		return
	}
	s.closeAgentConnections(id)
	writeNoContent(w)
}

func (s *server) createEnrollment(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	created, err := s.servers.CreateEnrollment(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	s.closeAgentConnections(id)
	writeJSON(w, http.StatusCreated, toCreatedServerResponse(created, requestBaseURL(r)))
}

func (s *server) permanentlyDeleteServer(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if err := s.servers.PermanentlyDelete(r.Context(), id); err != nil {
		writeServerError(w, err)
		return
	}
	s.closeAgentConnections(id)
	writeNoContent(w)
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, user auth.User, status int) {
	token, expiresAt, err := s.authService.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(auth.SessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, status, map[string]any{"user": toUserResponse(user)})
}

func (s *server) requireAuthentication(
	next func(http.ResponseWriter, *http.Request, auth.User),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authService.Authenticate(r.Context(), readSessionToken(r))
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		if err != nil {
			writeInternalError(w)
			return
		}
		next(w, r, user)
	}
}

func (s *server) requireAdmin(
	next func(http.ResponseWriter, *http.Request, auth.User),
) http.HandlerFunc {
	return s.requireAuthentication(func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if user.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "仅管理员可以执行此操作")
			return
		}
		next(w, r, user)
	})
}

func readSessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProtocol := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProtocol, "https")
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func toUserResponse(user auth.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt}
}

func toInvitationResponse(invitation auth.Invitation) invitationResponse {
	return invitationResponse{
		ID:                invitation.ID,
		CreatedBy:         invitation.CreatedBy,
		CreatedByUsername: invitation.CreatedByUsername,
		ExpiresAt:         invitation.ExpiresAt,
		CreatedAt:         invitation.CreatedAt,
	}
}

func toServerResponse(value serverstore.Server) serverResponse {
	return serverResponse{
		ID:         value.ID,
		Name:       value.Name,
		Status:     value.Status,
		ArchivedAt: value.ArchivedAt,
		CreatedAt:  value.CreatedAt,
		UpdatedAt:  value.UpdatedAt,
	}
}

func toCreatedServerResponse(
	created serverstore.CreatedServer,
	baseURL string,
) createdServerResponse {
	command := fmt.Sprintf(
		"curl -fsSL %s/install-agent.sh | bash -s -- \\\n  --server %s \\\n  --token %s",
		baseURL,
		baseURL,
		created.EnrollmentToken,
	)
	return createdServerResponse{
		Server:                   toServerResponse(created.Server),
		EnrollmentToken:          created.EnrollmentToken,
		EnrollmentTokenExpiresAt: created.EnrollmentExpiresAt,
		AgentInstallationCommand: command,
	}
}

func (s *server) trackAgentConnection(serverID int64, connection *websocket.Conn) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] == nil {
		s.connections[serverID] = make(map[*websocket.Conn]struct{})
	}
	s.connections[serverID][connection] = struct{}{}
}

func (s *server) untrackAgentConnection(serverID int64, connection *websocket.Conn) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	connections, tracked := s.connections[serverID]
	if !tracked {
		return false
	}
	if _, tracked = connections[connection]; !tracked {
		return false
	}
	delete(connections, connection)
	if len(connections) != 0 {
		return false
	}
	delete(s.connections, serverID)
	return true
}

func (s *server) closeAgentConnections(serverID int64) {
	s.connectionsMu.Lock()
	connections := s.connections[serverID]
	delete(s.connections, serverID)
	s.connectionsMu.Unlock()
	for connection := range connections {
		connection.CloseNow()
	}
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "error",
			"database": "unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"database": "ok",
	})
}

func (s *server) spa() http.Handler {
	files := http.FileServer(http.Dir(s.webRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cleanPath := filepath.FromSlash(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"))
		if cleanPath != "." {
			requestedFile := filepath.Join(s.webRoot, cleanPath)
			if info, err := os.Stat(requestedFile); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		indexPath := filepath.Join(s.webRoot, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			http.Error(w, "Panel web files are unavailable.", http.StatusServiceUnavailable)
			return
		}

		http.ServeFile(w, r, indexPath)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求内容无效")
		return false
	}
	return true
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrAlreadyInitialized):
		writeError(w, http.StatusConflict, "Panel 已完成初始化")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
	case errors.Is(err, auth.ErrInvalidInvitation):
		writeError(w, http.StatusBadRequest, "邀请链接无效、已使用或已过期")
	case errors.Is(err, auth.ErrInvitationNotFound):
		writeError(w, http.StatusNotFound, "邀请不存在、已使用或已过期")
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "用户名需为 3–64 位字母、数字、点、下划线或连字符")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "密码长度需为 10–72 字节")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "用户名已存在")
	default:
		writeInternalError(w)
	}
}

func writeServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, serverstore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "服务器名称不能为空且不能超过 100 个字符")
	case errors.Is(err, serverstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在")
	case errors.Is(err, serverstore.ErrInvalidEnrollment):
		writeError(w, http.StatusUnauthorized, "Enrollment Token 无效、已使用或已过期")
	case errors.Is(err, serverstore.ErrInitialConfigExists):
		writeError(w, http.StatusConflict, "此注册令牌仅用于首次安装，当前 VPS 已存在 Agent 配置")
	case errors.Is(err, serverstore.ErrInvalidAgentVersion):
		writeError(w, http.StatusBadRequest, "Agent 版本不能为空且不能超过 64 个字符")
	default:
		writeInternalError(w)
	}
}

func readPositiveID(w http.ResponseWriter, value, errorMessage string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, errorMessage)
		return 0, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "服务器内部错误")
}

func writeNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
