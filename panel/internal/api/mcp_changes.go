package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

type changeInput struct {
	RequestID  string `json:"request_id"`
	Operation  string `json:"operation"`
	ResourceID int64  `json:"resource_id"`
	Changes    struct {
		Name string `json:"name"`
	} `json:"changes"`
}
type changeIDInput struct {
	PlanID string `json:"plan_id"`
}
type changePlan struct {
	ID              string        `json:"plan_id"`
	InstanceID      string        `json:"-"`
	TokenID         int64         `json:"-"`
	OwnerID         int64         `json:"-"`
	RequestID       string        `json:"-"`
	RequestHash     string        `json:"-"`
	ServerID        int64         `json:"server_id"`
	OldName         string        `json:"old_name"`
	NewName         string        `json:"new_name"`
	Revision        int64         `json:"-"`
	Hash            string        `json:"-"`
	Status          string        `json:"status"`
	CreatedAt       int64         `json:"created_at"`
	ExpiresAt       int64         `json:"expires_at"`
	ApprovedAt      sql.NullInt64 `json:"-"`
	ApprovalExpires sql.NullInt64 `json:"-"`
	ApprovedBy      sql.NullInt64 `json:"-"`
	ApprovedHash    string        `json:"-"`
	TokenName       string        `json:"token_name,omitempty"`
}

const planColumns = `p.id,p.instance_id,p.token_id,p.owner_user_id,p.request_id,p.request_hash,p.server_id,p.old_name,p.new_name,p.resource_revision,p.plan_hash,p.status,p.created_at,p.expires_at,p.approved_at,p.approval_expires_at,p.approved_by,p.approved_hash`

type changeScanner interface{ Scan(...any) error }

func scanChange(row changeScanner) (changePlan, error) {
	var p changePlan
	err := row.Scan(&p.ID, &p.InstanceID, &p.TokenID, &p.OwnerID, &p.RequestID, &p.RequestHash, &p.ServerID, &p.OldName, &p.NewName, &p.Revision, &p.Hash, &p.Status, &p.CreatedAt, &p.ExpiresAt, &p.ApprovedAt, &p.ApprovalExpires, &p.ApprovedBy, &p.ApprovedHash)
	return p, err
}
func changeHash(p changePlan) string {
	data, _ := json.Marshal([]any{p.ID, p.InstanceID, p.TokenID, p.OwnerID, p.RequestID, p.RequestHash, p.ServerID, p.OldName, p.NewName, p.Revision, p.CreatedAt, p.ExpiresAt, "server.rename"})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func changeRequestHash(in changeInput) string {
	data, _ := json.Marshal([]any{in.Operation, in.ResourceID, in.Changes.Name})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func changeEvent(ctx context.Context, tx *sql.Tx, p changePlan, actor int64, event string, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO mcp_change_events(plan_id,token_id,actor_user_id,event,created_at,server_id,plan_hash,request_id) VALUES(?,?,?,?,?,?,?,?)`, p.ID, p.TokenID, actor, event, now, p.ServerID, p.Hash, requestIDFromContext(ctx))
	return err
}
func (s *server) changeAuthorizedTx(ctx context.Context, tx *sql.Tx, tokenID, userID, serverID int64, now int64) (bool, error) {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_tokens t JOIN users u ON u.id=t.owner_user_id
        JOIN mcp_token_capabilities c ON c.token_id=t.id AND c.capability='server.rename'
        JOIN mcp_settings settings ON settings.id=1
        WHERE t.id=? AND u.id=? AND u.role='admin' AND t.scope='read' AND t.access_scope='read_write' AND t.revoked_at IS NULL AND t.expires_at>?
        AND settings.enabled=1 AND settings.write_enabled=1)`, tokenID, userID, now).Scan(&allowed)
	if err != nil || !allowed {
		return false, err
	}
	return s.servers.CanAccessTx(ctx, tx, userID, serverID)
}
func (s *server) changeToolSchema(name, description string, properties map[string]any, required []string, readOnly bool) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description, InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}}
}
func (s *server) registerMCPChangeTools(ms *mcp.Server, identity mcpIdentity) {
	textSchema := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	mcp.AddTool(ms, s.changeToolSchema("prepare_change", "Prepare one server rename for independent web approval.", map[string]any{
		"request_id": textSchema, "operation": map[string]any{"type": "string", "enum": []string{"server.rename"}},
		"resource_id": map[string]any{"type": "integer", "minimum": 1},
		"changes":     map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}, "additionalProperties": false},
	}, []string{"request_id", "operation", "resource_id", "changes"}, false), func(ctx context.Context, _ *mcp.CallToolRequest, in changeInput) (*mcp.CallToolResult, any, error) {
		p, err := s.prepareChange(ctx, identity, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, p.public(), nil
	})
	schema := map[string]any{"plan_id": textSchema}
	mcp.AddTool(ms, s.changeToolSchema("get_change_status", "Read the status of your own rename plan.", schema, []string{"plan_id"}, true), func(ctx context.Context, _ *mcp.CallToolRequest, in changeIDInput) (*mcp.CallToolResult, any, error) {
		p, err := s.getChange(ctx, identity, in.PlanID)
		if err != nil {
			return nil, nil, err
		}
		return nil, p.public(), nil
	})
	mcp.AddTool(ms, s.changeToolSchema("apply_change", "Apply a web-approved rename plan once.", schema, []string{"plan_id"}, false), func(ctx context.Context, _ *mcp.CallToolRequest, in changeIDInput) (*mcp.CallToolResult, any, error) {
		p, err := s.applyChange(ctx, identity, in.PlanID)
		if err != nil {
			return nil, nil, err
		}
		return nil, p.public(), nil
	})
}
func (p changePlan) public() map[string]any {
	status := p.Status
	if (status == "pending" || status == "approved") && (p.ExpiresAt <= time.Now().Unix() || (status == "approved" && (!p.ApprovalExpires.Valid || p.ApprovalExpires.Int64 <= time.Now().Unix()))) {
		status = "expired"
	}
	return map[string]any{"plan_id": p.ID, "operation": "server.rename", "server_id": p.ServerID, "old_name": p.OldName, "new_name": p.NewName, "status": status, "created_at": p.CreatedAt, "expires_at": p.ExpiresAt, "token_name": p.TokenName, "agent_sync_required": false}
}
func (s *server) prepareChange(ctx context.Context, identity mcpIdentity, in changeInput) (changePlan, error) {
	var p changePlan
	if s.mcpInstanceID == "" || in.Operation != "server.rename" || in.ResourceID <= 0 || strings.TrimSpace(in.RequestID) != in.RequestID || in.RequestID == "" || len(in.RequestID) > 128 {
		return p, errors.New("invalid change request")
	}
	name, err := serverstore.NormalizeName(in.Changes.Name)
	if err != nil {
		return p, errors.New("invalid server name")
	}
	in.Changes.Name = name
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, errors.New("change unavailable")
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	allowed, err := s.changeAuthorizedTx(ctx, tx, identity.tokenID, identity.user.ID, in.ResourceID, now)
	if err != nil || !allowed {
		return p, errors.New("change unavailable")
	}
	requestHash := changeRequestHash(in)
	p, err = scanChange(tx.QueryRowContext(ctx, `SELECT `+planColumns+` FROM mcp_change_plans p WHERE token_id=? AND request_id=?`, identity.tokenID, in.RequestID))
	if err == nil {
		if p.InstanceID != s.mcpInstanceID || p.RequestHash != requestHash || p.Hash != changeHash(p) {
			return changePlan{}, errors.New("request conflict")
		}
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return changePlan{}, errors.New("change unavailable")
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_change_plans WHERE token_id=? AND instance_id=? AND status IN ('pending','approved') AND expires_at>?`, identity.tokenID, s.mcpInstanceID, now).Scan(&active); err != nil || active >= 32 {
		return changePlan{}, errors.New("too many active plans")
	}
	var current string
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT s.name,r.revision FROM servers s JOIN server_change_revisions r ON r.server_id=s.id WHERE s.id=? AND s.archived_at IS NULL AND s.decommission_status=''`, in.ResourceID).Scan(&current, &revision); err != nil {
		return changePlan{}, errors.New("server not found")
	}
	if current == name {
		return changePlan{}, errors.New("no change")
	}
	var random [24]byte
	if _, err = rand.Read(random[:]); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	p = changePlan{ID: base64.RawURLEncoding.EncodeToString(random[:]), InstanceID: s.mcpInstanceID, TokenID: identity.tokenID, OwnerID: identity.user.ID, RequestID: in.RequestID, RequestHash: requestHash, ServerID: in.ResourceID, OldName: current, NewName: name, Revision: revision, Status: "pending", CreatedAt: now, ExpiresAt: now + 600}
	p.Hash = changeHash(p)
	_, err = tx.ExecContext(ctx, `INSERT INTO mcp_change_plans(id,instance_id,token_id,owner_user_id,request_id,request_hash,server_id,old_name,new_name,resource_revision,plan_hash,status,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.InstanceID, p.TokenID, p.OwnerID, p.RequestID, p.RequestHash, p.ServerID, p.OldName, p.NewName, p.Revision, p.Hash, p.Status, p.CreatedAt, p.ExpiresAt)
	if err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	if err = changeEvent(ctx, tx, p, identity.user.ID, "prepared", now); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	if err = tx.Commit(); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	return p, nil
}
func (s *server) getChange(ctx context.Context, identity mcpIdentity, id string) (changePlan, error) {
	p, err := scanChange(s.db.QueryRowContext(ctx, `SELECT `+planColumns+` FROM mcp_change_plans p WHERE id=? AND token_id=? AND owner_user_id=?`, id, identity.tokenID, identity.user.ID))
	if err != nil || p.InstanceID != s.mcpInstanceID || p.Hash != changeHash(p) {
		return changePlan{}, errors.New("plan not found")
	}
	if _, err := s.mcpVisibleServer(ctx, identity.user, p.ServerID); err != nil {
		return changePlan{}, errors.New("plan not found")
	}
	if (p.Status == "pending" || p.Status == "approved") && (p.ExpiresAt <= time.Now().Unix() || (p.Status == "approved" && (!p.ApprovalExpires.Valid || p.ApprovalExpires.Int64 <= time.Now().Unix()))) {
		p.Status = "expired"
	}
	return p, nil
}
func (s *server) applyChange(ctx context.Context, identity mcpIdentity, id string) (changePlan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	defer tx.Rollback()
	// Take SQLite's write lock before reading approval, preventing competing applies.
	if _, err = tx.ExecContext(ctx, `UPDATE mcp_change_plans SET status=status WHERE id=? AND token_id=?`, id, identity.tokenID); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	p, err := scanChange(tx.QueryRowContext(ctx, `SELECT `+planColumns+` FROM mcp_change_plans p WHERE id=? AND token_id=? AND owner_user_id=?`, id, identity.tokenID, identity.user.ID))
	if err != nil || p.InstanceID != s.mcpInstanceID || p.Hash != changeHash(p) {
		return changePlan{}, errors.New("plan not found")
	}
	now := time.Now().Unix()
	allowed, err := s.changeAuthorizedTx(ctx, tx, p.TokenID, p.OwnerID, p.ServerID, now)
	if err != nil || !allowed {
		return changePlan{}, errors.New("change unavailable")
	}
	if p.Status == "committed" {
		return p, nil
	}
	if (p.Status == "pending" || p.Status == "approved") && (p.ExpiresAt <= now || (p.Status == "approved" && (!p.ApprovalExpires.Valid || p.ApprovalExpires.Int64 <= now))) {
		if _, err = tx.ExecContext(ctx, `UPDATE mcp_change_plans SET status='expired' WHERE id=?`, p.ID); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		if err = changeEvent(ctx, tx, p, p.OwnerID, "expired", now); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		if err = tx.Commit(); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		p.Status = "expired"
		return p, nil
	}
	if p.Status != "approved" || !p.ApprovedBy.Valid || p.ApprovedBy.Int64 != p.OwnerID || p.ApprovedHash != p.Hash || !p.ApprovalExpires.Valid {
		return changePlan{}, errors.New("approval unavailable")
	}
	var name string
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT s.name,r.revision FROM servers s JOIN server_change_revisions r ON r.server_id=s.id WHERE s.id=? AND s.archived_at IS NULL AND s.decommission_status=''`, p.ServerID).Scan(&name, &revision); err != nil {
		return changePlan{}, errors.New("server unavailable")
	}
	if revision != p.Revision || name != p.OldName {
		if _, err = tx.ExecContext(ctx, `UPDATE mcp_change_plans SET status='conflicted' WHERE id=?`, p.ID); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		if err = changeEvent(ctx, tx, p, p.OwnerID, "conflicted", now); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		if err = tx.Commit(); err != nil {
			return changePlan{}, errors.New("change unavailable")
		}
		p.Status = "conflicted"
		return p, nil
	}
	if err = s.servers.UpdateNameTx(ctx, tx, p.ServerID, p.NewName); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mcp_change_plans SET status='committed',consumed_at=? WHERE id=? AND status='approved'`, now, p.ID); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	if err = changeEvent(ctx, tx, p, p.OwnerID, "committed", now); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	if err = tx.Commit(); err != nil {
		return changePlan{}, errors.New("change unavailable")
	}
	p.Status = "committed"
	return p, nil
}

func (s *server) mcpListPlans(w http.ResponseWriter, r *http.Request, user auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+planColumns+`,t.name FROM mcp_change_plans p JOIN mcp_tokens t ON t.id=p.token_id JOIN servers sv ON sv.id=p.server_id WHERE p.owner_user_id=? AND p.instance_id=? AND sv.archived_at IS NULL AND (sv.visibility='public' OR EXISTS(SELECT 1 FROM server_access a WHERE a.server_id=sv.id AND a.user_id=?)) ORDER BY p.created_at DESC,p.id DESC LIMIT 100`, user.ID, s.mcpInstanceID, user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var p changePlan
		err = rows.Scan(&p.ID, &p.InstanceID, &p.TokenID, &p.OwnerID, &p.RequestID, &p.RequestHash, &p.ServerID, &p.OldName, &p.NewName, &p.Revision, &p.Hash, &p.Status, &p.CreatedAt, &p.ExpiresAt, &p.ApprovedAt, &p.ApprovalExpires, &p.ApprovedBy, &p.ApprovedHash, &p.TokenName)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if p.Hash != changeHash(p) {
			continue
		}
		if (p.Status == "pending" || p.Status == "approved") && (p.ExpiresAt <= time.Now().Unix() || (p.Status == "approved" && (!p.ApprovalExpires.Valid || p.ApprovalExpires.Int64 <= time.Now().Unix()))) {
			p.Status = "expired"
		}
		items = append(items, p.public())
	}
	if err = rows.Err(); err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"plans": items})
}
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

func (s *server) mcpApprovePlan(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.decideChange(w, r, user, true)
}
func (s *server) mcpRejectPlan(w http.ResponseWriter, r *http.Request, user auth.User) {
	s.decideChange(w, r, user, false)
}
func (s *server) decideChange(w http.ResponseWriter, r *http.Request, user auth.User, approve bool) {
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if ok, _ := s.mcpApprovalLimiter.Take(ip, user.Username, now); !ok {
		writeError(w, 429, "请求过于频繁")
		return
	}
	if len(input.Password) > 1024 || s.authService.VerifyPassword(r.Context(), user.ID, input.Password) != nil {
		s.recordAudit(r, user, "mcp.change.approval_failed", "mcp_change", 0, "MCP 变更审批身份验证失败")
		writeError(w, 403, "身份验证失败")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer tx.Rollback()
	var validSession bool
	if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions JOIN users ON users.id=sessions.user_id WHERE sessions.token_hash=? AND sessions.user_id=? AND sessions.expires_at>? AND users.role='admin')`, token.Hash(readSessionToken(r)), user.ID, time.Now().Unix()).Scan(&validSession); err != nil || !validSession {
		writeError(w, 401, "请先登录")
		return
	}
	id := r.PathValue("id")
	if _, err = tx.ExecContext(r.Context(), `UPDATE mcp_change_plans SET status=status WHERE id=? AND owner_user_id=?`, id, user.ID); err != nil {
		writeInternalError(w, err)
		return
	}
	now = time.Now()
	p, err := scanChange(tx.QueryRowContext(r.Context(), `SELECT `+planColumns+` FROM mcp_change_plans p WHERE id=? AND owner_user_id=?`, id, user.ID))
	if err != nil || p.InstanceID != s.mcpInstanceID || p.Hash != changeHash(p) || (p.Status != "pending" && (approve || p.Status != "approved")) || p.ExpiresAt <= now.Unix() {
		writeError(w, 404, "计划不可用")
		return
	}
	allowed, err := s.servers.CanAccessTx(r.Context(), tx, user.ID, p.ServerID)
	if approve {
		allowed, err = s.changeAuthorizedTx(r.Context(), tx, p.TokenID, user.ID, p.ServerID, now.Unix())
	}
	if err != nil || !allowed {
		writeError(w, 404, "计划不可用")
		return
	}
	if approve {
		var name string
		var revision int64
		if err = tx.QueryRowContext(r.Context(), `SELECT s.name,r.revision FROM servers s JOIN server_change_revisions r ON r.server_id=s.id WHERE s.id=? AND s.archived_at IS NULL AND s.decommission_status=''`, p.ServerID).Scan(&name, &revision); err != nil || name != p.OldName || revision != p.Revision {
			writeError(w, 409, "资源已变化，请重新准备计划")
			return
		}
	}
	event := "rejected"
	if p.Status == "approved" {
		event = "revoked"
	}
	p.Status = event
	if approve {
		event = "approved"
		p.Status = "approved"
		p.ApprovalExpires = sql.NullInt64{Int64: min(p.ExpiresAt, now.Unix()+300), Valid: true}
		_, err = tx.ExecContext(r.Context(), `UPDATE mcp_change_plans SET status='approved',approved_at=?,approval_expires_at=?,approved_by=?,approved_hash=? WHERE id=? AND status='pending'`, now.Unix(), min(p.ExpiresAt, now.Unix()+300), user.ID, p.Hash, p.ID)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE mcp_change_plans SET status=? WHERE id=? AND status IN ('pending','approved')`, p.Status, p.ID)
	}
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if err = changeEvent(r.Context(), tx, p, user.ID, event, now.Unix()); err != nil {
		writeInternalError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, 200, p.public())
}
