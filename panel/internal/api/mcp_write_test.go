package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

type writeFixture struct {
	db           *sql.DB
	handler      http.Handler
	cookie       *http.Cookie
	memberCookie *http.Cookie
	session      *mcp.ClientSession
	token        string
	tokenID      int64
	serverID     int64
}

func newWriteFixture(t *testing.T) (*writeFixture, func()) {
	t.Helper()
	db, h, accounts := setupAccessTest(t)
	created := createAccessTestServer(t, h, accounts.adminCookie, "Before", "private", nil)
	issue := performRequest(t, h, "POST", "/api/mcp/tokens", map[string]any{"name": "writer", "scope": "read_write", "capabilities": []string{"server.rename"}}, accounts.adminCookie)
	var b struct {
		Token string `json:"token"`
		Item  struct {
			ID int64 `json:"id"`
		} `json:"item"`
	}
	if issue.Code != 201 || json.Unmarshal(issue.Body.Bytes(), &b) != nil {
		t.Fatalf("issue writer: %d %s", issue.Code, issue.Body.String())
	}
	if w := performRequest(t, h, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true, "mcp_write_enabled": true}, accounts.adminCookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	srv := httptest.NewServer(h)
	transport := mcpBearerTransport{base: http.DefaultTransport, token: b.Token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &writeFixture{db: db, handler: h, cookie: accounts.adminCookie, memberCookie: accounts.memberCookie, session: session, token: b.Token, tokenID: b.Item.ID, serverID: created.Server.ID}
	return f, func() { session.Close(); srv.Close(); db.Close() }
}
func (f *writeFixture) call(t *testing.T, name string, args map[string]any, success bool) map[string]any {
	t.Helper()
	r, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if r.IsError == success {
		t.Fatalf("%s result error=%v", name, r.IsError)
	}
	if !success {
		return nil
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *writeFixture) prepare(t *testing.T, id, name string) string {
	t.Helper()
	p := f.call(t, "prepare_change", map[string]any{"request_id": id, "operation": "server.rename", "resource_id": f.serverID, "changes": map[string]any{"name": name}}, true)
	return p["plan_id"].(string)
}
func (f *writeFixture) decide(t *testing.T, id, decision string, code int) {
	t.Helper()
	r := performRequest(t, f.handler, "POST", "/api/mcp/change-plans/"+id+"/"+decision, map[string]string{"password": "strong-password"}, f.cookie)
	if r.Code != code {
		t.Fatalf("%s: %d %s", decision, r.Code, r.Body.String())
	}
	if decision == "approve" && code == 200 {
		var body map[string]any
		if json.Unmarshal(r.Body.Bytes(), &body) != nil || body["status"] != "approved" {
			t.Fatal("approval response has incorrect status")
		}
	}
}
func TestMCPWriteApprovalAtomicAndConcurrent(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	plan := f.prepare(t, "first", "After")
	if again := f.prepare(t, "first", "After"); again != plan {
		t.Fatal("prepare idempotency failed")
	}
	f.call(t, "prepare_change", map[string]any{"request_id": "first", "operation": "server.rename", "resource_id": f.serverID, "changes": map[string]any{"name": "different"}}, false)
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	f.call(t, "apply_change", map[string]any{"plan_id": plan, "approved": true}, false)
	bearer := httptest.NewRequest("POST", "/api/mcp/change-plans/"+plan+"/approve", nil)
	bearer.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, bearer)
	if w.Code != 401 {
		t.Fatalf("bearer approved: %d", w.Code)
	}
	bad := performRequest(t, f.handler, "POST", "/api/mcp/change-plans/"+plan+"/approve", map[string]string{"password": "wrong"}, f.cookie)
	if bad.Code != 403 {
		t.Fatalf("bad password: %d", bad.Code)
	}
	foreign := jsonRequest(t, "POST", "/api/mcp/change-plans/"+plan+"/approve", map[string]string{"password": "strong-password"})
	foreign.AddCookie(f.cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, foreign)
	if w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing frame protection")
	}
	denied := performRequest(t, f.handler, "POST", "/api/mcp/change-plans/"+plan+"/approve", map[string]string{"password": "another-password"}, f.memberCookie)
	if denied.Code != 403 {
		t.Fatal("non-admin approved")
	}
	f.decide(t, plan, "approve", 200)
	results := make(chan *mcp.CallToolResult, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			r, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "apply_change", Arguments: map[string]any{"plan_id": plan}})
			results <- r
			errs <- err
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for r := range results {
		if r.IsError {
			t.Fatal("concurrent apply failed")
		}
	}
	var events int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM mcp_change_events WHERE plan_id=? AND event='committed'`, plan).Scan(&events); err != nil || events != 1 {
		t.Fatal("duplicate commit audit")
	}
	result := f.call(t, "get_change_status", map[string]any{"plan_id": plan}, true)
	if result["status"] != "committed" {
		t.Fatal("not committed")
	}
	read := performRequest(t, f.handler, "GET", "/api/servers/"+strconv.FormatInt(f.serverID, 10), nil, f.cookie)
	if read.Code != 200 {
		t.Fatal("server read failed")
	}
	var payload struct {
		Server struct {
			Name string `json:"name"`
		} `json:"server"`
	}
	if json.Unmarshal(read.Body.Bytes(), &payload) != nil || payload.Server.Name != "After" {
		t.Fatal("server not renamed")
	}
}

func TestMCPWriteFailuresAndIsolation(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	bad := performRequest(t, f.handler, "POST", "/api/mcp/tokens", map[string]any{"name": "bad", "scope": "read_write"}, f.cookie)
	if bad.Code != 400 {
		t.Fatal("write without capability accepted")
	}
	read := phase1Session(t, f.handler, f.cookie)
	r, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "prepare_change", Arguments: map[string]any{"request_id": "read", "operation": "server.rename", "resource_id": f.serverID, "changes": map[string]any{"name": "x"}}})
	if err != nil || !r.IsError {
		t.Fatal("read token prepared a write")
	}
	plan := f.prepare(t, "conflict", "Conflicted")
	f.decide(t, plan, "approve", 200)
	update := performRequest(t, f.handler, "PATCH", "/api/servers/"+strconv.FormatInt(f.serverID, 10), map[string]any{"name": "Changed"}, f.cookie)
	if update.Code != 200 {
		t.Fatalf("web update: %d", update.Code)
	}
	update = performRequest(t, f.handler, "PATCH", "/api/servers/"+strconv.FormatInt(f.serverID, 10), map[string]any{"name": "Before"}, f.cookie)
	if update.Code != 200 {
		t.Fatal("web restore failed")
	}
	result := f.call(t, "apply_change", map[string]any{"plan_id": plan}, true)
	if result["status"] != "conflicted" {
		t.Fatal("ABA revision conflict missed")
	}
	plan = f.prepare(t, "audit-fail", "ShouldNotSave")
	f.decide(t, plan, "approve", 200)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_change_audit BEFORE INSERT ON mcp_change_events WHEN NEW.event='committed' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	var name, status string
	if err := f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Before" {
		t.Fatal("audit failure did not roll back server")
	}
	if err := f.db.QueryRow(`SELECT status FROM mcp_change_plans WHERE id=?`, plan).Scan(&status); err != nil || status != "approved" {
		t.Fatal("audit failure consumed plan")
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_change_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE mcp_change_plans SET approval_expires_at=1 WHERE id=?`, plan); err != nil {
		t.Fatal(err)
	}
	result = f.call(t, "apply_change", map[string]any{"plan_id": plan}, true)
	if result["status"] != "expired" {
		t.Fatal("expired approval accepted")
	}
	result = f.call(t, "get_change_status", map[string]any{"plan_id": plan}, true)
	if result["status"] != "expired" {
		t.Fatal("approval expiry not shown")
	}
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": false}, f.cookie); r.Code != 200 {
		t.Fatal("disable failed")
	}
	f.call(t, "prepare_change", map[string]any{"request_id": "disabled", "operation": "server.rename", "resource_id": f.serverID, "changes": map[string]any{"name": "x"}}, false)
}

func TestMCPWriteBackupRestoreInvalidatesApprovals(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	plan := f.prepare(t, "restore", "RestoredRename")
	f.decide(t, plan, "approve", 200)
	var seq int
	var dbname, path string
	if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &dbname, &path); err != nil {
		t.Fatal(err)
	}
	archive, remove, err := backup.CreateArchive(context.Background(), f.db, filepath.Dir(path), "v0.92.17", "panel.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	dir := t.TempDir()
	if err = backup.StageImport(t.Context(), archive, dir, "v0.92.17", "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(dir)
	if err != nil || attempt == nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = attempt.Commit(); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(restored, t.TempDir())
	srv := httptest.NewServer(h)
	defer srv.Close()
	transport := mcpBearerTransport{base: http.DefaultTransport, token: f.token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "restored", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "apply_change", Arguments: map[string]any{"plan_id": plan}})
	if err != nil || !result.IsError {
		t.Fatal("restored approval was accepted")
	}
	var name string
	if err = restored.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Before" {
		t.Fatal("restored plan changed server")
	}
}

func TestMCPWriteRechecksTokenSwitchAndPlanIntegrity(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	plan := f.prepare(t, "tamper", "Unapproved")
	f.decide(t, plan, "approve", 200)
	if _, err := f.db.Exec(`UPDATE mcp_change_plans SET new_name='Tampered' WHERE id=?`, plan); err != nil {
		t.Fatal(err)
	}
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	plan = f.prepare(t, "switch", "SwitchName")
	f.decide(t, plan, "approve", 200)
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": false}, f.cookie); r.Code != 200 {
		t.Fatal("disable failed")
	}
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": true}, f.cookie); r.Code != 200 {
		t.Fatal("enable failed")
	}
	if _, err := f.db.Exec(`UPDATE mcp_tokens SET revoked_at=1 WHERE id=?`, f.tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "apply_change", Arguments: map[string]any{"plan_id": plan}}); err == nil {
		t.Fatal("revoked token used")
	}
	var name string
	if err := f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Before" {
		t.Fatal("unauthorized modification")
	}
}
func TestMCPWriteExpiresAndRechecksServerAccess(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	expired := f.prepare(t, "expire-plan", "Never")
	if _, err := f.db.Exec(`UPDATE mcp_change_plans SET expires_at=1 WHERE id=?`, expired); err != nil {
		t.Fatal(err)
	}
	p, err := scanChange(f.db.QueryRow(`SELECT `+planColumns+` FROM mcp_change_plans p WHERE p.id=?`, expired))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE mcp_change_plans SET plan_hash=? WHERE id=?`, changeHash(p), expired); err != nil {
		t.Fatal(err)
	}
	result := f.call(t, "apply_change", map[string]any{"plan_id": expired}, true)
	if result["status"] != "expired" {
		t.Fatal("expired plan accepted")
	}
	plan := f.prepare(t, "access", "Denied")
	f.decide(t, plan, "approve", 200)
	if _, err = f.db.Exec(`DELETE FROM server_access WHERE server_id=?`, f.serverID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	f.call(t, "get_change_status", map[string]any{"plan_id": plan}, false)
	var name string
	if err = f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Before" {
		t.Fatal("lost ACL was ignored")
	}
}
func TestMCPWriteConflictsOnServerMetadataChange(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	plan := f.prepare(t, "metadata", "New Name")
	f.decide(t, plan, "approve", 200)
	response := performRequest(t, f.handler, "PATCH", "/api/servers/"+strconv.FormatInt(f.serverID, 10), map[string]any{"bandwidth_mbps": 100}, f.cookie)
	if response.Code != 200 {
		t.Fatalf("metadata update: %d", response.Code)
	}
	result := f.call(t, "apply_change", map[string]any{"plan_id": plan}, true)
	if result["status"] != "conflicted" {
		t.Fatal("metadata change did not conflict")
	}
}

func TestMCPWriteRevokeApproval(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	plan := f.prepare(t, "revoke-plan", "Never")
	f.decide(t, plan, "approve", 200)
	f.decide(t, plan, "reject", 200)
	f.call(t, "apply_change", map[string]any{"plan_id": plan}, false)
	result := f.call(t, "get_change_status", map[string]any{"plan_id": plan}, true)
	if result["status"] != "revoked" {
		t.Fatal("approval not revoked")
	}
}
