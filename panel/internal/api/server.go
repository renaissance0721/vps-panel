package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

const sessionCookieName = "vps_panel_session"

type server struct {
	db          *sql.DB
	authService *auth.Service
	webRoot     string
}

func NewHandler(db *sql.DB, webRoot string) http.Handler {
	s := &server{db: db, authService: auth.NewService(db), webRoot: webRoot}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/initialize", s.initialize)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/admin/invitations", s.requireAuthentication(s.listInvitations))
	mux.HandleFunc("POST /api/admin/invitations", s.requireAuthentication(s.createInvitation))
	mux.HandleFunc("DELETE /api/admin/invitations/{id}", s.requireAuthentication(s.revokeInvitation))
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
	return userResponse{ID: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}
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
