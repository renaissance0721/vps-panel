package api

import (
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type server struct {
	db           *sql.DB
	authService  *auth.Service
	servers      *serverstore.Service
	proxies      *proxystore.Service
	landings     *landingstore.Service
	relays       *relaystore.Service
	orders       *listorder.Store
	webRoot      string
	panelVersion string
	agents       *agentcontrol.Service
	backup       BackupConfig
	backupMu     sync.Mutex
}

type BackupConfig struct {
	DataDir          string
	Domain           string
	EnvironmentFile  string
	CaddyFile        string
	RestoreRequested chan<- struct{}
}

func NewHandler(db *sql.DB, webRoot string) http.Handler {
	return NewHandlerWithVersion(db, webRoot, "dev")
}

func NewHandlerWithVersion(db *sql.DB, webRoot, panelVersion string) http.Handler {
	return NewHandlerWithBackup(db, webRoot, panelVersion, BackupConfig{})
}

func NewHandlerWithBackup(db *sql.DB, webRoot, panelVersion string, backupConfig BackupConfig) http.Handler {
	s := &server{
		db:           db,
		authService:  auth.NewService(db),
		servers:      serverstore.NewService(db),
		proxies:      proxystore.NewService(db),
		landings:     landingstore.NewService(db),
		relays:       relaystore.NewService(db),
		orders:       listorder.NewStore(db),
		webRoot:      webRoot,
		panelVersion: panelVersion,
		agents:       agentcontrol.NewService(db, time.Now),
		backup:       backupConfig,
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
	mux.HandleFunc("POST /api/agent/upgrade/result", s.recordAgentUpgradeResult)
	mux.HandleFunc("POST /api/agent/traffic", s.recordAgentClientTraffic)
	mux.HandleFunc("GET /api/agent/ws", s.agentWebSocket)
	mux.HandleFunc("GET /api/admin/invitations", s.requireAdmin(s.listInvitations))
	mux.HandleFunc("POST /api/admin/invitations", s.requireAdmin(s.createInvitation))
	mux.HandleFunc("DELETE /api/admin/invitations/{id}", s.requireAdmin(s.revokeInvitation))
	mux.HandleFunc("GET /api/admin/backup/export", s.requireAdmin(s.exportBackup))
	mux.HandleFunc("POST /api/admin/backup/import", s.requireAdmin(s.importBackup))
	mux.HandleFunc("GET /api/users", s.requireAuthentication(s.listUsers))
	mux.HandleFunc("GET /api/overview", s.requireAuthentication(s.overview))
	mux.HandleFunc("GET /api/servers", s.requireAuthentication(s.listServers))
	mux.HandleFunc("POST /api/servers", s.requireAuthentication(s.createServer))
	mux.HandleFunc("POST /api/servers/{id}/reorder", s.requireAuthentication(s.reorderServer))
	mux.HandleFunc("GET /api/servers/{id}", s.requireAuthentication(s.getServer))
	mux.HandleFunc("POST /api/servers/{id}/diagnostics", s.requireAuthentication(s.diagnoseServer))
	mux.HandleFunc("PATCH /api/servers/{id}", s.requireAuthentication(s.updateServerExpiration))
	mux.HandleFunc("PATCH /api/servers/{id}/access", s.requireAuthentication(s.updateServerAccess))
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuthentication(s.deleteServer))
	mux.HandleFunc("DELETE /api/servers/{id}/force", s.requireAdmin(s.forceRemoveServer))
	mux.HandleFunc("PATCH /api/servers/{id}/traffic-adjustment", s.requireAuthentication(s.updateTrafficAdjustment))
	mux.HandleFunc("DELETE /api/servers/{id}/traffic-adjustment", s.requireAuthentication(s.clearTrafficAdjustment))
	mux.HandleFunc("POST /api/servers/{id}/enrollment", s.requireAdmin(s.createEnrollment))
	mux.HandleFunc("POST /api/servers/{id}/agent-upgrade", s.requireAdmin(s.upgradeAgent))
	mux.HandleFunc("DELETE /api/servers/{id}/permanent", s.requireAdmin(s.permanentlyDeleteServer))
	mux.HandleFunc("GET /api/proxies", s.requireAuthentication(s.listProxies))
	mux.HandleFunc("POST /api/proxies", s.requireAuthentication(s.createProxy))
	mux.HandleFunc("POST /api/proxies/{id}/reorder", s.requireAuthentication(s.reorderProxy))
	mux.HandleFunc("GET /api/proxies/{id}", s.requireAuthentication(s.getProxy))
	mux.HandleFunc("PATCH /api/proxies/{id}", s.requireAuthentication(s.updateProxy))
	mux.HandleFunc("DELETE /api/proxies/{id}", s.requireAuthentication(s.deleteProxy))
	mux.HandleFunc("GET /api/proxies/{id}/clients", s.requireAuthentication(s.listProxyClients))
	mux.HandleFunc("POST /api/proxies/{id}/clients", s.requireAuthentication(s.createProxyClient))
	mux.HandleFunc("GET /api/clients/{id}", s.requireAuthentication(s.getProxyClient))
	mux.HandleFunc("PATCH /api/clients/{id}", s.requireAuthentication(s.updateProxyClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireAuthentication(s.deleteProxyClient))
	mux.HandleFunc("POST /api/clients/{id}/traffic/reset", s.requireAuthentication(s.resetProxyClientTraffic))
	mux.HandleFunc("GET /api/clients/{id}/share", s.requireAuthentication(s.getProxyClientShare))
	mux.HandleFunc("GET /api/landings", s.requireAuthentication(s.listLandings))
	mux.HandleFunc("POST /api/landings", s.requireAuthentication(s.createLanding))
	mux.HandleFunc("GET /api/landings/{id}", s.requireAuthentication(s.getLanding))
	mux.HandleFunc("GET /api/landings/{id}/share", s.requireAuthentication(s.getLandingShare))
	mux.HandleFunc("PATCH /api/landings/{id}", s.requireAuthentication(s.updateLanding))
	mux.HandleFunc("DELETE /api/landings/{id}", s.requireAuthentication(s.deleteLanding))
	mux.HandleFunc("GET /api/relays", s.requireAuthentication(s.listRelays))
	mux.HandleFunc("POST /api/relays", s.requireAuthentication(s.createRelay))
	mux.HandleFunc("POST /api/relays/{id}/reorder", s.requireAuthentication(s.reorderRelay))
	mux.HandleFunc("GET /api/relays/{id}", s.requireAuthentication(s.getRelay))
	mux.HandleFunc("GET /api/relays/{id}/clients", s.requireAuthentication(s.getRelayClients))
	mux.HandleFunc("GET /api/relays/{id}/landing-share", s.requireAuthentication(s.getRelayLandingShare))
	mux.HandleFunc("PATCH /api/relays/{id}", s.requireAuthentication(s.updateRelay))
	mux.HandleFunc("DELETE /api/relays/{id}", s.requireAuthentication(s.deleteRelay))
	mux.HandleFunc("GET /install-agent.sh", s.installAgent)
	mux.HandleFunc("GET /upgrade-agent.sh", s.upgradeAgentInstaller)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.Handle("/", s.spa())
	return mux
}
