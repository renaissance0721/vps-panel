package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
)

const sessionCookieName = "vps_panel_session"

const actorContextKey requestContextKey = "actor"

func actorFromContext(ctx context.Context) (auth.User, bool) {
	value, ok := ctx.Value(actorContextKey).(auth.User)
	return value, ok
}

func (s *server) authState(w http.ResponseWriter, r *http.Request) {
	requiresInitialization, err := s.authService.NeedsInitialization(r.Context())
	if err != nil {
		writeInternalError(w, err)
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
			writeInternalError(w, err)
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
	s.recordAudit(r, user, "user.create", "user", user.ID, "初始化管理员账号 "+user.Username)
	s.startSession(w, r, user, http.StatusCreated)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if allowed, retryAfter := s.loginLimiter.Allow(ip, request.Username, now); !allowed {
		seconds := max(1, int((retryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	user, err := s.authService.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.loginLimiter.RecordFailure(ip, request.Username, now)
		}
		writeAuthError(w, err)
		return
	}
	s.loginLimiter.Reset(ip, request.Username)
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
	s.recordAudit(r, user, "user.create", "user", user.ID, "创建用户账号 "+user.Username)
	s.startSession(w, r, user, http.StatusCreated)
}

func (s *server) getInvitation(w http.ResponseWriter, r *http.Request) {
	invitation, err := s.authService.GetInvitation(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitation": toInvitationResponse(invitation)})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if readSessionToken(r) != "" && !s.validateSessionRequestOrigin(r) {
		writeError(w, http.StatusForbidden, "请求来源无效")
		return
	}
	if err := s.authService.Logout(r.Context(), readSessionToken(r)); err != nil {
		writeInternalError(w, err)
		return
	}
	clearSessionCookie(w, secureRequest(r))
	writeNoContent(w)
}

func (s *server) listInvitations(w http.ResponseWriter, r *http.Request, _ auth.User) {
	invitations, err := s.authService.ListActiveInvitations(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	response := make([]invitationResponse, 0, len(invitations))
	for _, invitation := range invitations {
		response = append(response, toInvitationResponse(invitation))
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": response})
}

func (s *server) createInvitation(w http.ResponseWriter, r *http.Request, user auth.User) {
	request := createInvitationRequest{Role: auth.RoleVIP}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &request) {
			return
		}
	}
	invitation, err := s.authService.CreateInvitation(r.Context(), user.ID, request.Role)
	if err != nil {
		writeAuthError(w, err)
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

func (s *server) listUsers(w http.ResponseWriter, r *http.Request, viewer auth.User) {
	users, err := s.authService.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	ids := make([]int64, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	ranks, err := s.orderRanks(r.Context(), viewer.ID, listorder.Users, ids)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	sort.SliceStable(users, func(i, j int) bool { return ranks[users[i].ID] < ranks[users[j].ID] })
	response := make([]accessUserResponse, 0, len(users))
	for _, user := range users {
		response = append(response, toAccessUserResponse(user))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": response})
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, user auth.User, status int) {
	token, expiresAt, err := s.authService.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, err)
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
			writeInternalError(w, err)
			return
		}
		if isUnsafeSessionMethod(r.Method) && !s.validateSessionRequestOrigin(r) {
			writeError(w, http.StatusForbidden, "请求来源无效")
			return
		}
		*r = *r.WithContext(context.WithValue(r.Context(), actorContextKey, user))
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

func (s *server) requireManager(
	next func(http.ResponseWriter, *http.Request, auth.User),
) http.HandlerFunc {
	return s.requireAuthentication(func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if user.Role != auth.RoleAdmin && user.Role != auth.RoleVIP {
			writeError(w, http.StatusForbidden, "仅管理员和 VIP 可以访问管理 API")
			return
		}
		next(w, r, user)
	})
}

func (s *server) requireCarpool(
	next func(http.ResponseWriter, *http.Request, auth.User),
) http.HandlerFunc {
	return s.requireAuthentication(func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if user.Role != auth.RoleCarpool {
			writeError(w, http.StatusForbidden, "仅拼车用户可以访问拼车门户 API")
			return
		}
		next(w, r, user)
	})
}

func (s *server) requireSubscriber(
	next func(http.ResponseWriter, *http.Request, auth.User),
) http.HandlerFunc {
	return s.requireAuthentication(func(w http.ResponseWriter, r *http.Request, user auth.User) {
		if user.Role != auth.RoleSubscriber {
			writeError(w, http.StatusForbidden, "仅订阅用户可以访问订阅门户 API")
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
	case errors.Is(err, auth.ErrInvalidInvitationRole):
		writeError(w, http.StatusBadRequest, "邀请角色仅支持 VIP、拼车用户或订阅用户")
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "用户名需为 3–64 位字母、数字、点、下划线或连字符")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "密码长度需为 6–72 字节")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "用户名已存在")
	default:
		writeInternalError(w, err)
	}
}
