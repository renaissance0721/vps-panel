package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestAgentUpgradeRequiresAdminOnlineFormalVersionAndUsesTypedMessage(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	handler := NewHandlerWithVersion(db, t.TempDir(), "v0.12.0")
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialization.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialization.Code, initialization.Body.String())
	}
	adminCookie := initialization.Result().Cookies()[0]
	invitationRecorder := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, adminCookie)
	var invitation invitationResponse
	if invitationRecorder.Code != http.StatusCreated || json.Unmarshal(invitationRecorder.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create invitation = %d, %s", invitationRecorder.Code, invitationRecorder.Body.String())
	}
	vipRegistration := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "vip-upgrade", "password": "another-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	vipCookie := vipRegistration.Result().Cookies()[0]

	created, err := service.Create(t.Context(), "Upgrade Agent")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.11.0", false)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10) + "/agent-upgrade"
	if response := performRequest(t, handler, http.MethodPost, path, nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upgrade = %d", response.Code)
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, vipCookie); response.Code != http.StatusForbidden {
		t.Fatalf("VIP upgrade = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusConflict {
		t.Fatalf("offline upgrade = %d, %s", response.Code, response.Body.String())
	}
	devHandler := NewHandlerWithVersion(db, t.TempDir(), "dev")
	if response := performRequest(t, devHandler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "开发版本") {
		t.Fatalf("development Panel upgrade = %d, %s", response.Code, response.Body.String())
	}

	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	header.Set("X-VPS-Panel-Agent-Version", "v0.11.0")
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent = %v, response = %+v", err, response)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	upgrade := performRequest(t, handler, http.MethodPost, path, nil, adminCookie)
	if upgrade.Code != http.StatusAccepted {
		t.Fatalf("start upgrade = %d, %s", upgrade.Code, upgrade.Body.String())
	}
	readContext, cancelRead := context.WithTimeout(t.Context(), time.Second)
	messageType, message, err := connection.Read(readContext)
	cancelRead()
	if err != nil || messageType != websocket.MessageText || string(message) != `{"type":"agent_upgrade","version":"v0.12.0"}` {
		t.Fatalf("upgrade message = (%d, %q, %v)", messageType, message, err)
	}
	var agentID, serverID int64
	var tokenHash, target, status string
	if err := db.QueryRow(`SELECT id, server_id, token_hash, upgrade_target_version, upgrade_status
		FROM agents WHERE server_id = ?`, created.ID).Scan(&agentID, &serverID, &tokenHash, &target, &status); err != nil {
		t.Fatal(err)
	}
	if agentID != registered.ID || serverID != created.ID || tokenHash != token.Hash(registered.Token) ||
		target != "v0.12.0" || status != agentcontrol.AgentUpgradeUpgrading {
		t.Fatalf("upgrade state = id %d server %d token %q target %q status %q", agentID, serverID, tokenHash, target, status)
	}
	if err := connection.Close(websocket.StatusNormalClosure, "upgrade test reconnect"); err != nil {
		t.Fatal(err)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)

	header.Set("X-VPS-Panel-Agent-Version", "v0.12.0")
	currentConnection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("reconnect upgraded Agent = %v, response = %+v", err, response)
	}
	defer currentConnection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	current, err := service.Get(t.Context(), created.ID)
	if err != nil || current.AgentVersion != "v0.12.0" || current.AgentUpgradeStatus != "" || current.AgentUpgradeTarget != "" {
		t.Fatalf("upgraded server = (%+v, %v)", current, err)
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"status":"already_current"`) {
		t.Fatalf("same-version upgrade = %d, %s", response.Code, response.Body.String())
	}
	var finalAgentID, finalServerID int64
	var finalTokenHash string
	if err := db.QueryRow(`SELECT id, server_id, token_hash FROM agents WHERE server_id = ?`, created.ID).
		Scan(&finalAgentID, &finalServerID, &finalTokenHash); err != nil {
		t.Fatal(err)
	}
	if finalAgentID != agentID || finalServerID != serverID || finalTokenHash != tokenHash {
		t.Fatalf("Agent identity changed after upgrade: before %d/%d/%q, after %d/%d/%q",
			agentID, serverID, tokenHash, finalAgentID, finalServerID, finalTokenHash)
	}
}

func TestAgentNewerThanPanelIsVisibleButCannotBeDowngraded(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	handler := NewHandlerWithVersion(db, t.TempDir(), "v0.19.1")
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialization.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialization.Code, initialization.Body.String())
	}
	cookie := initialization.Result().Cookies()[0]
	created, err := service.Create(t.Context(), "Newer Agent")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.19.2", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentcontrol.NewService(db, time.Now).SetAgentConnectedVersion(t.Context(), registered.ID, created.ID, "v0.19.2"); err != nil {
		t.Fatal(err)
	}
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10)
	response := performRequest(t, handler, http.MethodGet, path, nil, cookie)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agent_version_status":"agent_newer"`) ||
		!strings.Contains(response.Body.String(), `"agent_version":"v0.19.2"`) {
		t.Fatalf("newer Agent response = %d, %s", response.Code, response.Body.String())
	}
	upgrade := performRequest(t, handler, http.MethodPost, path+"/agent-upgrade", nil, cookie)
	if upgrade.Code != http.StatusConflict || !strings.Contains(upgrade.Body.String(), "newer than Panel") {
		t.Fatalf("downgrade request = %d, %s", upgrade.Code, upgrade.Body.String())
	}
	var target, status string
	if err := db.QueryRow(`SELECT upgrade_target_version, upgrade_status FROM agents WHERE server_id = ?`, created.ID).Scan(&target, &status); err != nil || target != "" || status != "" {
		t.Fatalf("rejected downgrade changed Agent = (%q, %q, %v)", target, status, err)
	}
	health := performRequest(t, handler, http.MethodGet, "/api/health", nil, nil)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"version":"v0.19.1"`) {
		t.Fatalf("Panel version source = %d, %s", health.Code, health.Body.String())
	}
}
