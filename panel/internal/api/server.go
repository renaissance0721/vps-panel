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

const expirationDateLayout = "2006-01-02"

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type server struct {
	db            *sql.DB
	authService   *auth.Service
	servers       *serverstore.Service
	webRoot       string
	panelVersion  string
	connectionsMu sync.Mutex
	connections   map[int64]*agentConnection
}

type agentConnection struct {
	socket  *websocket.Conn
	writeMu sync.Mutex
}

func NewHandler(db *sql.DB, webRoot string) http.Handler {
	return NewHandlerWithVersion(db, webRoot, "dev")
}

func NewHandlerWithVersion(db *sql.DB, webRoot, panelVersion string) http.Handler {
	s := &server{
		db:           db,
		authService:  auth.NewService(db),
		servers:      serverstore.NewService(db),
		webRoot:      webRoot,
		panelVersion: panelVersion,
		connections:  make(map[int64]*agentConnection),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/initialize", s.initialize)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/agent/register", s.registerAgent)
	mux.HandleFunc("GET /api/agent/config", s.getAgentConfig)
	mux.HandleFunc("POST /api/agent/config/result", s.recordAgentConfigResult)
	mux.HandleFunc("GET /api/agent/ws", s.agentWebSocket)
	mux.HandleFunc("GET /api/admin/invitations", s.requireAdmin(s.listInvitations))
	mux.HandleFunc("POST /api/admin/invitations", s.requireAdmin(s.createInvitation))
	mux.HandleFunc("DELETE /api/admin/invitations/{id}", s.requireAdmin(s.revokeInvitation))
	mux.HandleFunc("GET /api/servers", s.requireAuthentication(s.listServers))
	mux.HandleFunc("POST /api/servers", s.requireAuthentication(s.createServer))
	mux.HandleFunc("GET /api/servers/{id}", s.requireAuthentication(s.getServer))
	mux.HandleFunc("PATCH /api/servers/{id}", s.requireAuthentication(s.updateServerExpiration))
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuthentication(s.deleteServer))
	mux.HandleFunc("PATCH /api/servers/{id}/traffic-adjustment", s.requireAuthentication(s.updateTrafficAdjustment))
	mux.HandleFunc("DELETE /api/servers/{id}/traffic-adjustment", s.requireAuthentication(s.clearTrafficAdjustment))
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

type updateServerRequest struct {
	ExpiresAt                json.RawMessage `json:"expires_at"`
	MonthlyTrafficLimitBytes json.RawMessage `json:"monthly_traffic_limit_bytes"`
	TrafficCountMode         *string         `json:"traffic_count_mode"`
	TrafficResetDay          *int            `json:"traffic_reset_day"`
	TrafficResetTime         *string         `json:"traffic_reset_time"`
}

type updateTrafficAdjustmentRequest struct {
	TargetUsedBytes *int64 `json:"target_used_bytes"`
}

type serverResponse struct {
	ID                       int64               `json:"id"`
	Name                     string              `json:"name"`
	Status                   string              `json:"status"`
	ArchivedAt               *time.Time          `json:"archived_at,omitempty"`
	ExpiresAt                *time.Time          `json:"expires_at"`
	MonthlyTrafficLimitBytes *int64              `json:"monthly_traffic_limit_bytes"`
	TrafficCountMode         string              `json:"traffic_count_mode"`
	TrafficResetDay          int                 `json:"traffic_reset_day"`
	TrafficResetTime         string              `json:"traffic_reset_time"`
	TrafficUsedBytes         int64               `json:"traffic_used_bytes"`
	LastSeenAt               *time.Time          `json:"last_seen_at"`
	SystemInfo               *systemInfoResponse `json:"system_info"`
	Metrics                  *metricsResponse    `json:"metrics"`
	CreatedAt                time.Time           `json:"created_at"`
	UpdatedAt                time.Time           `json:"updated_at"`
}

type systemInfoResponse struct {
	Hostname     string   `json:"hostname"`
	OSName       string   `json:"os_name"`
	OSVersion    string   `json:"os_version"`
	Kernel       string   `json:"kernel"`
	Arch         string   `json:"arch"`
	IPv4         []string `json:"ipv4"`
	IPv6         []string `json:"ipv6"`
	AgentVersion string   `json:"agent_version"`
}

type metricsResponse struct {
	CPUPercent             float64    `json:"cpu_percent"`
	MemoryUsedBytes        int64      `json:"memory_used_bytes"`
	MemoryTotalBytes       int64      `json:"memory_total_bytes"`
	DiskUsedBytes          int64      `json:"disk_used_bytes"`
	DiskTotalBytes         int64      `json:"disk_total_bytes"`
	UptimeSeconds          int64      `json:"uptime_seconds"`
	NICRXBytes             int64      `json:"nic_rx_bytes"`
	NICTXBytes             int64      `json:"nic_tx_bytes"`
	CycleRXBytes           int64      `json:"cycle_rx_bytes"`
	CycleTXBytes           int64      `json:"cycle_tx_bytes"`
	TrafficAdjustmentBytes int64      `json:"traffic_adjustment_bytes"`
	CycleStartedAt         *time.Time `json:"cycle_started_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
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

type agentDesiredStateResponse struct {
	Version int64                  `json:"version"`
	Xray    agentDesiredXrayState  `json:"xray"`
	Realm   agentDesiredRealmState `json:"realm"`
}

type agentDesiredXrayState struct {
	Enabled bool  `json:"enabled"`
	Proxies []any `json:"proxies"`
}

type agentDesiredRealmState struct {
	Enabled bool  `json:"enabled"`
	Relays  []any `json:"relays"`
}

type agentConfigResultRequest struct {
	Version int64  `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type agentConfigChangedMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version"`
}

type agentSystemInfoMessage struct {
	Type      string   `json:"type"`
	Hostname  string   `json:"hostname"`
	OSName    string   `json:"os_name"`
	OSVersion string   `json:"os_version"`
	Kernel    string   `json:"kernel"`
	Arch      string   `json:"arch"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
}

type agentMetricsMessage struct {
	Type             string  `json:"type"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskUsedBytes    int64   `json:"disk_used_bytes"`
	DiskTotalBytes   int64   `json:"disk_total_bytes"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
	NICRXBytes       *int64  `json:"nic_rx_bytes"`
	NICTXBytes       *int64  `json:"nic_tx_bytes"`
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

func (s *server) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	state, err := s.servers.GetDesiredState(r.Context(), agent.ID, agent.ServerID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentDesiredStateResponse{
		Version: state.Version,
		Xray: agentDesiredXrayState{
			Proxies: make([]any, 0),
		},
		Realm: agentDesiredRealmState{
			Relays: make([]any, 0),
		},
	})
}

func (s *server) recordAgentConfigResult(w http.ResponseWriter, r *http.Request) {
	agent, agentToken, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}
	var request agentConfigResultRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Message != "" && strings.Contains(request.Message, agentToken) {
		writeError(w, http.StatusBadRequest, "Agent 配置同步结果无效")
		return
	}
	if err := s.servers.RecordConfigResult(r.Context(), agent.ID, agent.ServerID, serverstore.ConfigResult{
		Version: request.Version,
		Status:  request.Status,
		Message: request.Message,
	}); err != nil {
		writeServerError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *server) authenticateAgentRequest(w http.ResponseWriter, r *http.Request) (serverstore.Agent, string, bool) {
	authorization := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return serverstore.Agent{}, "", false
	}
	agent, err := s.servers.AuthenticateAgent(r.Context(), authorization[1])
	if errors.Is(err, serverstore.ErrInvalidAgentToken) {
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
		return serverstore.Agent{}, "", false
	}
	if err != nil {
		writeInternalError(w)
		return serverstore.Agent{}, "", false
	}
	return agent, authorization[1], true
}

func (s *server) agentWebSocket(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(8 << 10)
	currentConnection := &agentConnection{socket: connection}
	previous := s.trackAgentConnection(agent.ServerID, currentConnection)
	if previous != nil {
		previous.socket.CloseNow()
	}

	statusContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.servers.SetAgentConnected(statusContext, agent.ID, agent.ServerID)
	cancel()
	if err != nil {
		s.untrackAgentConnection(agent.ServerID, currentConnection)
		if errors.Is(err, serverstore.ErrArchived) || errors.Is(err, serverstore.ErrNotFound) {
			return
		}
		log.Printf("set agent %d server %d online: %v", agent.ID, agent.ServerID, err)
		_ = connection.Close(websocket.StatusInternalError, "server status update failed")
		return
	}
	log.Printf("agent %d connected to server %d", agent.ID, agent.ServerID)

	for {
		messageType, message, readErr := connection.Read(r.Context())
		if readErr != nil {
			break
		}
		if !s.isCurrentAgentConnection(agent.ServerID, currentConnection) {
			return
		}
		var payload struct {
			Type string `json:"type"`
		}
		if messageType != websocket.MessageText || json.Unmarshal(message, &payload) != nil {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid Agent message")
			break
		}
		validMessage := true
		switch payload.Type {
		case "heartbeat":
			heartbeatContext, cancelHeartbeat := context.WithTimeout(context.Background(), 5*time.Second)
			err = s.servers.TouchAgent(heartbeatContext, agent.ID, agent.ServerID)
			cancelHeartbeat()
			if err != nil {
				if !errors.Is(err, serverstore.ErrInvalidAgentToken) {
					log.Printf("update agent %d heartbeat for server %d: %v", agent.ID, agent.ServerID, err)
				}
				validMessage = false
			}
		case "system_info":
			var systemInfo agentSystemInfoMessage
			if json.Unmarshal(message, &systemInfo) != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid system information")
				validMessage = false
				break
			}
			current, reportErr := s.reportCurrentSystemInfo(agent.ServerID, agent.ID, currentConnection, serverstore.SystemInfoReport{
				Hostname:  systemInfo.Hostname,
				OSName:    systemInfo.OSName,
				OSVersion: systemInfo.OSVersion,
				Kernel:    systemInfo.Kernel,
				Arch:      systemInfo.Arch,
				IPv4:      systemInfo.IPv4,
				IPv6:      systemInfo.IPv6,
			})
			if !current {
				return
			}
			if reportErr != nil {
				if !errors.Is(reportErr, serverstore.ErrInvalidAgentToken) &&
					!errors.Is(reportErr, serverstore.ErrInvalidSystemInfo) {
					log.Printf("update system information for agent %d server %d: %v", agent.ID, agent.ServerID, reportErr)
				}
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid system information")
				validMessage = false
			}
		case "metrics":
			var metrics agentMetricsMessage
			if json.Unmarshal(message, &metrics) != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
				break
			}
			if (metrics.NICRXBytes == nil) != (metrics.NICTXBytes == nil) {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
				break
			}
			var nicRXBytes, nicTXBytes int64
			if metrics.NICRXBytes != nil {
				nicRXBytes = *metrics.NICRXBytes
				nicTXBytes = *metrics.NICTXBytes
			}
			current, reportErr := s.reportCurrentMetrics(agent.ServerID, agent.ID, currentConnection, serverstore.MetricsReport{
				CPUPercent:       metrics.CPUPercent,
				MemoryUsedBytes:  metrics.MemoryUsedBytes,
				MemoryTotalBytes: metrics.MemoryTotalBytes,
				DiskUsedBytes:    metrics.DiskUsedBytes,
				DiskTotalBytes:   metrics.DiskTotalBytes,
				UptimeSeconds:    metrics.UptimeSeconds,
				HasNetworkUsage:  metrics.NICRXBytes != nil,
				NICRXBytes:       nicRXBytes,
				NICTXBytes:       nicTXBytes,
			})
			if !current {
				return
			}
			if reportErr != nil {
				if !errors.Is(reportErr, serverstore.ErrInvalidAgentToken) &&
					!errors.Is(reportErr, serverstore.ErrInvalidMetrics) {
					log.Printf("update metrics for agent %d server %d: %v", agent.ID, agent.ServerID, reportErr)
				}
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
			}
		default:
			_ = connection.Close(websocket.StatusPolicyViolation, "unknown Agent message")
			validMessage = false
		}
		if !validMessage {
			break
		}
	}

	current, err := s.disconnectCurrentAgent(agent.ServerID, currentConnection)
	if !current {
		return
	}
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
	writeJSON(w, http.StatusCreated, s.toCreatedServerResponse(created, requestBaseURL(r)))
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

func (s *server) updateServerExpiration(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	var request updateServerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	hasExpiration := len(request.ExpiresAt) != 0
	hasAnyTraffic := len(request.MonthlyTrafficLimitBytes) != 0 || request.TrafficCountMode != nil ||
		request.TrafficResetDay != nil || request.TrafficResetTime != nil
	if hasExpiration == hasAnyTraffic {
		writeError(w, http.StatusBadRequest, "服务器设置格式无效")
		return
	}
	if hasAnyTraffic {
		if len(request.MonthlyTrafficLimitBytes) == 0 || request.TrafficCountMode == nil ||
			request.TrafficResetDay == nil || request.TrafficResetTime == nil {
			writeError(w, http.StatusBadRequest, "月流量设置不完整")
			return
		}
		var monthlyLimit *int64
		if string(request.MonthlyTrafficLimitBytes) != "null" {
			var value int64
			if json.Unmarshal(request.MonthlyTrafficLimitBytes, &value) != nil {
				writeError(w, http.StatusBadRequest, "月流量额度格式无效")
				return
			}
			monthlyLimit = &value
		}
		updated, err := s.servers.UpdateTrafficConfig(r.Context(), id, serverstore.TrafficConfig{
			MonthlyLimitBytes: monthlyLimit,
			CountMode:         *request.TrafficCountMode,
			ResetDay:          *request.TrafficResetDay,
			ResetTime:         *request.TrafficResetTime,
		})
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated)})
		return
	}
	var expiresAt *time.Time
	if string(request.ExpiresAt) != "null" {
		var value string
		if json.Unmarshal(request.ExpiresAt, &value) != nil {
			writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
			return
		}
		parsed, err := time.ParseInLocation(expirationDateLayout, value, shanghaiLocation)
		if err != nil {
			writeError(w, http.StatusBadRequest, "到期日期格式无效，请使用 YYYY-MM-DD")
			return
		}
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, shanghaiLocation).UTC()
		expiresAt = &parsed
	}
	updated, err := s.servers.UpdateExpiration(r.Context(), id, expiresAt)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated)})
}

func (s *server) updateTrafficAdjustment(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	var request updateTrafficAdjustmentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.TargetUsedBytes == nil {
		writeError(w, http.StatusBadRequest, "目标已用流量格式无效")
		return
	}
	updated, err := s.servers.UpdateTrafficAdjustment(r.Context(), id, *request.TargetUsedBytes)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated)})
}

func (s *server) clearTrafficAdjustment(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	updated, err := s.servers.ClearTrafficAdjustment(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": toServerResponse(updated)})
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
	writeJSON(w, http.StatusCreated, s.toCreatedServerResponse(created, requestBaseURL(r)))
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
	response := serverResponse{
		ID:                       value.ID,
		Name:                     value.Name,
		Status:                   value.Status,
		ArchivedAt:               value.ArchivedAt,
		ExpiresAt:                value.ExpiresAt,
		MonthlyTrafficLimitBytes: value.MonthlyTrafficLimitBytes,
		TrafficCountMode:         value.TrafficCountMode,
		TrafficResetDay:          value.TrafficResetDay,
		TrafficResetTime:         value.TrafficResetTime,
		TrafficUsedBytes:         value.TrafficUsedBytes(),
		LastSeenAt:               value.LastSeenAt,
		CreatedAt:                value.CreatedAt,
		UpdatedAt:                value.UpdatedAt,
	}
	if value.SystemInfo != nil {
		response.SystemInfo = &systemInfoResponse{
			Hostname:     value.SystemInfo.Hostname,
			OSName:       value.SystemInfo.OSName,
			OSVersion:    value.SystemInfo.OSVersion,
			Kernel:       value.SystemInfo.Kernel,
			Arch:         value.SystemInfo.Arch,
			IPv4:         value.SystemInfo.IPv4,
			IPv6:         value.SystemInfo.IPv6,
			AgentVersion: value.SystemInfo.AgentVersion,
		}
	}
	if value.Metrics != nil {
		response.Metrics = &metricsResponse{
			CPUPercent:             value.Metrics.CPUPercent,
			MemoryUsedBytes:        value.Metrics.MemoryUsedBytes,
			MemoryTotalBytes:       value.Metrics.MemoryTotalBytes,
			DiskUsedBytes:          value.Metrics.DiskUsedBytes,
			DiskTotalBytes:         value.Metrics.DiskTotalBytes,
			UptimeSeconds:          value.Metrics.UptimeSeconds,
			NICRXBytes:             value.Metrics.NICRXBytes,
			NICTXBytes:             value.Metrics.NICTXBytes,
			CycleRXBytes:           value.Metrics.CycleRXBytes,
			CycleTXBytes:           value.Metrics.CycleTXBytes,
			TrafficAdjustmentBytes: value.Metrics.TrafficAdjustmentBytes,
			CycleStartedAt:         value.Metrics.CycleStartedAt,
			UpdatedAt:              value.Metrics.UpdatedAt,
		}
	}
	return response
}

func (s *server) toCreatedServerResponse(
	created serverstore.CreatedServer,
	baseURL string,
) createdServerResponse {
	command := fmt.Sprintf(
		"curl -fsSL %s/install-agent.sh | bash -s -- \\\n  --server %s \\\n  --token %s",
		baseURL,
		baseURL,
		created.EnrollmentToken,
	)
	if version := releaseVersion(s.panelVersion); version != "" {
		command += " \\\n  --version " + version
	}
	return createdServerResponse{
		Server:                   toServerResponse(created.Server),
		EnrollmentToken:          created.EnrollmentToken,
		EnrollmentTokenExpiresAt: created.EnrollmentExpiresAt,
		AgentInstallationCommand: command,
	}
}

func releaseVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'v' || value[1] < '0' || value[1] > '9' {
		return ""
	}
	for _, character := range value[2:] {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '-' || character == '_' {
			continue
		}
		return ""
	}
	return value
}

func (s *server) trackAgentConnection(serverID int64, connection *agentConnection) *agentConnection {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	previous := s.connections[serverID]
	s.connections[serverID] = connection
	return previous
}

func (s *server) untrackAgentConnection(serverID int64, connection *agentConnection) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false
	}
	delete(s.connections, serverID)
	return true
}

func (s *server) isCurrentAgentConnection(serverID int64, connection *agentConnection) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	return s.connections[serverID] == connection
}

func (s *server) disconnectCurrentAgent(serverID int64, connection *agentConnection) (bool, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false, nil
	}
	delete(s.connections, serverID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, s.servers.SetAgentOffline(ctx, serverID)
}

func (s *server) reportCurrentSystemInfo(
	serverID, agentID int64,
	connection *agentConnection,
	report serverstore.SystemInfoReport,
) (bool, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, s.servers.ReportSystemInfo(ctx, agentID, serverID, report)
}

func (s *server) reportCurrentMetrics(
	serverID, agentID int64,
	connection *agentConnection,
	report serverstore.MetricsReport,
) (bool, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, s.servers.ReportMetrics(ctx, agentID, serverID, report)
}

func (s *server) notifyConfigChanged(serverID, version int64) error {
	if version <= 0 {
		return errors.New("invalid desired state version")
	}
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	s.connectionsMu.Unlock()
	if connection == nil {
		return nil
	}

	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	s.connectionsMu.Lock()
	current := s.connections[serverID] == connection
	s.connectionsMu.Unlock()
	if !current {
		return nil
	}
	payload, err := json.Marshal(agentConfigChangedMessage{Type: "config_changed", Version: version})
	if err != nil {
		return fmt.Errorf("encode Agent config notification: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.socket.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("notify Agent config changed: %w", err)
	}
	return nil
}

func (s *server) closeAgentConnections(serverID int64) {
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	delete(s.connections, serverID)
	s.connectionsMu.Unlock()
	if connection != nil {
		connection.socket.CloseNow()
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
	case errors.Is(err, serverstore.ErrInvalidTrafficConfig):
		writeError(w, http.StatusBadRequest, "月流量设置无效")
	case errors.Is(err, serverstore.ErrInvalidTrafficTarget):
		writeError(w, http.StatusBadRequest, "目标已用流量必须是非负整数")
	case errors.Is(err, serverstore.ErrInvalidConfigResult):
		writeError(w, http.StatusBadRequest, "Agent 配置同步结果无效")
	case errors.Is(err, serverstore.ErrConfigVersionAhead):
		writeError(w, http.StatusBadRequest, "Agent 配置版本高于当前目标版本")
	case errors.Is(err, serverstore.ErrInvalidAgentToken):
		writeError(w, http.StatusUnauthorized, "Agent Token 无效")
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
