package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type mcpToken struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	RevokedAt  *int64 `json:"revoked_at"`
}

func (s *server) mcpSettings(w http.ResponseWriter, r *http.Request, user auth.User) {
	if r.Method == http.MethodPatch {
		var input struct {
			Enabled      *bool `json:"mcp_enabled"`
			WriteEnabled *bool `json:"mcp_write_enabled"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.Enabled == nil && input.WriteEnabled == nil {
			writeError(w, 400, "缺少服务状态")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE mcp_settings SET enabled = COALESCE(?, enabled), write_enabled = COALESCE(?, write_enabled) WHERE id = 1`, input.Enabled, input.WriteEnabled); err != nil {
			writeInternalError(w, err)
			return
		}
		if input.WriteEnabled != nil {
			s.recordAudit(r, user, "mcp.write_settings", "mcp_settings", 0, "更新 MCP 写操作开关")
		}
	}
	var enabled, writeEnabled bool
	if err := s.db.QueryRowContext(r.Context(), `SELECT enabled,write_enabled FROM mcp_settings WHERE id = 1`).Scan(&enabled, &writeEnabled); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mcp_enabled": enabled, "mcp_write_enabled": writeEnabled})
}

func (s *server) mcpTokens(w http.ResponseWriter, r *http.Request, user auth.User) {
	if r.Method == http.MethodPost {
		var in struct {
			Name         string   `json:"name"`
			Scope        string   `json:"scope"`
			Capabilities []string `json:"capabilities"`
			ExpiresAt    string   `json:"expires_at"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Scope == "" {
			in.Scope = "read"
		}
		// A legacy client may request only server.rename. Accepting that request as
		// a general read_write token would silently grant broader authority.
		if len(in.Capabilities) != 0 {
			writeError(w, 400, "旧版细粒度权限已停用，请明确创建读写令牌")
			return
		}
		if in.Name == "" || utf8.RuneCountInString(in.Name) > 100 || (in.Scope != "read" && in.Scope != "read_write") {
			writeError(w, 400, "令牌名称或权限无效")
			return
		}
		now := time.Now().UTC()
		expiry := now.Add(90 * 24 * time.Hour)
		if in.ExpiresAt != "" {
			var err error
			expiry, err = time.Parse(time.RFC3339, in.ExpiresAt)
			if err != nil || !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) {
				writeError(w, 400, "有效期无效")
				return
			}
		}
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			writeInternalError(w, err)
			return
		}
		token := "vpsmcp_" + base64.RawURLEncoding.EncodeToString(random[:])
		hash := sha256.Sum256([]byte(token))
		prefix := token[:15]
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		defer tx.Rollback()
		// Keep the legacy scope column unchanged; access_scope is authoritative.
		result, err := tx.ExecContext(r.Context(), `INSERT INTO mcp_tokens(name,token_hash,token_prefix,owner_user_id,scope,access_scope,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, in.Name, hash[:], prefix, user.ID, "read", in.Scope, now.Unix(), expiry.Unix())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		id, err := result.LastInsertId()
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if err := tx.Commit(); err != nil {
			writeInternalError(w, err)
			return
		}
		if in.Scope == "read_write" {
			s.recordAudit(r, user, "mcp.write_token.create", "mcp_token", id, "创建 MCP 读写令牌")
		}
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "item": mcpToken{ID: id, Name: in.Name, Prefix: prefix, Scope: in.Scope, CreatedAt: now.Unix(), ExpiresAt: expiry.Unix()}})
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,token_prefix,access_scope,created_at,expires_at,last_used_at,revoked_at FROM mcp_tokens ORDER BY id DESC LIMIT 500`)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer rows.Close()
	items := []mcpToken{}
	for rows.Next() {
		var item mcpToken
		if err := rows.Scan(&item.ID, &item.Name, &item.Prefix, &item.Scope, &item.CreatedAt, &item.ExpiresAt, &item.LastUsedAt, &item.RevokedAt); err != nil {
			writeInternalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": items})
}
func (s *server) revokeMCPToken(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "令牌 ID 无效")
	if !ok {
		return
	}
	result, err := s.db.ExecContext(r.Context(), `UPDATE mcp_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, time.Now().Unix(), id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if count == 0 {
		writeError(w, 404, "令牌不存在")
		return
	}
	s.recordAudit(r, user, "mcp.token.revoke", "mcp_token", id, "撤销 MCP 令牌")
	writeNoContent(w)
}

type mcpIdentity struct {
	user    auth.User
	tokenID int64
}

func (s *server) authenticateMCP(r *http.Request) (mcpIdentity, error) {
	var identity mcpIdentity
	var enabled bool
	if err := s.db.QueryRowContext(r.Context(), `SELECT enabled FROM mcp_settings WHERE id=1`).Scan(&enabled); err != nil || !enabled {
		return identity, errors.New("MCP unavailable")
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "vpsmcp_") || len(parts[1]) != 50 {
		return identity, errors.New("invalid token")
	}
	hash := sha256.Sum256([]byte(parts[1]))
	var stored []byte
	var owner sql.NullInt64
	var scope string
	var expiry int64
	var revoked sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), `SELECT id,token_hash,owner_user_id,scope,expires_at,revoked_at FROM mcp_tokens WHERE token_hash=?`, hash[:]).Scan(&identity.tokenID, &stored, &owner, &scope, &expiry, &revoked)
	if err != nil || subtle.ConstantTimeCompare(hash[:], stored) != 1 || !owner.Valid || scope != "read" || revoked.Valid || expiry <= time.Now().Unix() {
		return mcpIdentity{}, errors.New("invalid token")
	}
	identity.user, err = s.authService.GetUser(r.Context(), owner.Int64)
	if err != nil || identity.user.Role != auth.RoleAdmin {
		return mcpIdentity{}, errors.New("invalid token")
	}
	if _, err = s.db.ExecContext(r.Context(), `UPDATE mcp_tokens SET last_used_at=? WHERE id=?`, time.Now().Unix(), identity.tokenID); err != nil {
		return mcpIdentity{}, errors.New("authentication unavailable")
	}
	return identity, nil
}
