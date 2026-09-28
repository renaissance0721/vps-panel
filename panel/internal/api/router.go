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
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type server struct {
	db            *sql.DB
	authService   *auth.Service
	servers       *serverstore.Service
	proxies       *proxystore.Service
	landings      *landingstore.Service
	relays        *relaystore.Service
	subscriptions *subscriptionstore.Service
	orders        *listorder.Store
	webRoot       string
	panelVersion  string
	agents        *agentcontrol.Service
	backup        BackupConfig
	backupMu      sync.Mutex
	loginLimiter  *loginLimiter
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
	relays := relaystore.NewService(db)
	s := &server{
		db:            db,
		authService:   auth.NewService(db),
		servers:       serverstore.NewService(db),
		proxies:       proxystore.NewService(db),
		landings:      landingstore.NewService(db),
		relays:        relays,
		subscriptions: subscriptionstore.NewService(db, relays),
		orders:        listorder.NewStore(db),
		webRoot:       webRoot,
		panelVersion:  panelVersion,
		agents:        agentcontrol.NewService(db, time.Now),
		backup:        backupConfig,
		loginLimiter:  newLoginLimiter(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sub/{token}", s.getPublicSubscription)
	mux.HandleFunc("GET /sub/{token}/mihomo", s.getPublicSubscriptionMihomo)
	mux.HandleFunc("GET /sub/{token}/auto", s.getPublicSubscriptionAuto)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/initialize", s.initialize)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("GET /api/auth/invitation", s.getInvitation)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("PATCH /api/account/username", s.requireAuthentication(s.renameMyAccount))
	mux.HandleFunc("POST /api/account/password", s.requireAuthentication(s.changeMyPassword))
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
	mux.HandleFunc("PATCH /api/admin/clients/{id}/assignment", s.requireAdmin(s.assignProxyClient))
	mux.HandleFunc("PATCH /api/admin/clients/{id}/relay-ports", s.requireAdmin(s.updateClientRelayPortCount))
	mux.HandleFunc("GET /api/admin/password-change-requests", s.requireAdmin(s.listPasswordChangeRequests))
	mux.HandleFunc("POST /api/admin/password-change-requests/{id}/approve", s.requireAdmin(s.approvePasswordChangeRequest))
	mux.HandleFunc("POST /api/admin/password-change-requests/{id}/reject", s.requireAdmin(s.rejectPasswordChangeRequest))
	mux.HandleFunc("GET /api/admin/user-relays", s.requireAdmin(s.listAdminUserRelays))
	mux.HandleFunc("DELETE /api/admin/user-relays/{id}", s.requireAdmin(s.deleteAdminUserRelay))
	mux.HandleFunc("GET /api/admin/subscription/nodes", s.requireAdmin(s.listSubscriptionPublishedNodes))
	mux.HandleFunc("POST /api/admin/subscription/nodes", s.requireAdmin(s.createSubscriptionPublishedNode))
	mux.HandleFunc("PATCH /api/admin/subscription/nodes/{id}", s.requireAdmin(s.updateSubscriptionPublishedNode))
	mux.HandleFunc("DELETE /api/admin/subscription/nodes/{id}", s.requireAdmin(s.deleteSubscriptionPublishedNode))
	mux.HandleFunc("GET /api/admin/subscription/plans", s.requireAdmin(s.listSubscriptionPlans))
	mux.HandleFunc("POST /api/admin/subscription/plans", s.requireAdmin(s.createSubscriptionPlan))
	mux.HandleFunc("GET /api/admin/subscription/plans/{id}", s.requireAdmin(s.getSubscriptionPlan))
	mux.HandleFunc("PATCH /api/admin/subscription/plans/{id}", s.requireAdmin(s.updateSubscriptionPlan))
	mux.HandleFunc("DELETE /api/admin/subscription/plans/{id}", s.requireAdmin(s.deleteSubscriptionPlan))
	mux.HandleFunc("PUT /api/admin/subscription/plans/{id}/nodes", s.requireAdmin(s.setSubscriptionPlanNodes))
	mux.HandleFunc("GET /api/admin/subscription/users", s.requireAdmin(s.listSubscriptionUsers))
	mux.HandleFunc("GET /api/admin/subscription/users/{id}", s.requireAdmin(s.getSubscriptionUser))
	mux.HandleFunc("PATCH /api/admin/subscription/users/{id}", s.requireAdmin(s.updateSubscriptionUser))
	mux.HandleFunc("POST /api/admin/subscription/users/{id}/token/regenerate", s.requireAdmin(s.regenerateSubscriptionUserToken))
	mux.HandleFunc("POST /api/admin/subscription/users/{id}/traffic/reset", s.requireAdmin(s.resetSubscriptionUserTraffic))
	mux.HandleFunc("GET /api/subscriber/me", s.requireSubscriber(s.getSubscriberMe))
	mux.HandleFunc("GET /api/subscriber/nodes", s.requireSubscriber(s.listSubscriberNodes))
	mux.HandleFunc("POST /api/subscriber/subscription/regenerate", s.requireSubscriber(s.regenerateSubscriberToken))
	mux.HandleFunc("GET /api/subscriber/password-change-request", s.requireSubscriber(s.getMyPasswordChangeRequest))
	mux.HandleFunc("POST /api/subscriber/password-change-request", s.requireSubscriber(s.createMyPasswordChangeRequest))
	mux.HandleFunc("GET /api/admin/users", s.requireAdmin(s.listAdminUsers))
	mux.HandleFunc("GET /api/admin/users/{id}", s.requireAdmin(s.getAdminUserDetail))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.requireAdmin(s.deleteAdminUser))
	mux.HandleFunc("POST /api/admin/users/{id}/nodes", s.requireAdmin(s.createAdminUserNode))
	mux.HandleFunc("GET /api/me/nodes", s.requireUser(s.listMyNodes))
	mux.HandleFunc("PATCH /api/me/nodes/{id}", s.requireUser(s.updateMyNode))
	mux.HandleFunc("GET /api/me/nodes/{id}/share", s.requireUser(s.getMyNodeShare))
	mux.HandleFunc("GET /api/me/password-change-request", s.requireUser(s.getMyPasswordChangeRequest))
	mux.HandleFunc("POST /api/me/password-change-request", s.requireUser(s.createMyPasswordChangeRequest))
	mux.HandleFunc("GET /api/me/relay-sources", s.requireUser(s.listMyRelaySources))
	mux.HandleFunc("GET /api/me/relays", s.requireUser(s.listMyRelays))
	mux.HandleFunc("POST /api/me/relays", s.requireUser(s.createMyRelay))
	mux.HandleFunc("GET /api/me/relays/{id}/share", s.requireUser(s.getMyRelayShare))
	mux.HandleFunc("PATCH /api/me/relays/{id}", s.requireUser(s.updateMyRelay))
	mux.HandleFunc("DELETE /api/me/relays/{id}", s.requireUser(s.deleteMyRelay))
	mux.HandleFunc("GET /api/users", s.requireManager(s.listUsers))
	mux.HandleFunc("GET /api/overview", s.requireManager(s.overview))
	mux.HandleFunc("GET /api/servers", s.requireManager(s.listServers))
	mux.HandleFunc("POST /api/servers", s.requireManager(s.createServer))
	mux.HandleFunc("POST /api/servers/{id}/reorder", s.requireManager(s.reorderServer))
	mux.HandleFunc("GET /api/servers/{id}", s.requireManager(s.getServer))
	mux.HandleFunc("POST /api/servers/{id}/diagnostics", s.requireManager(s.diagnoseServer))
	mux.HandleFunc("PATCH /api/servers/{id}", s.requireManager(s.updateServerExpiration))
	mux.HandleFunc("PATCH /api/servers/{id}/access", s.requireManager(s.updateServerAccess))
	mux.HandleFunc("DELETE /api/servers/{id}", s.requireManager(s.deleteServer))
	mux.HandleFunc("DELETE /api/servers/{id}/force", s.requireAdmin(s.forceRemoveServer))
	mux.HandleFunc("PATCH /api/servers/{id}/traffic-adjustment", s.requireManager(s.updateTrafficAdjustment))
	mux.HandleFunc("DELETE /api/servers/{id}/traffic-adjustment", s.requireManager(s.clearTrafficAdjustment))
	mux.HandleFunc("POST /api/servers/{id}/enrollment", s.requireAdmin(s.createEnrollment))
	mux.HandleFunc("POST /api/servers/{id}/agent-upgrade", s.requireAdmin(s.upgradeAgent))
	mux.HandleFunc("DELETE /api/servers/{id}/permanent", s.requireAdmin(s.permanentlyDeleteServer))
	mux.HandleFunc("GET /api/proxies", s.requireManager(s.listProxies))
	mux.HandleFunc("POST /api/proxies", s.requireManager(s.createProxy))
	mux.HandleFunc("POST /api/proxies/{id}/reorder", s.requireManager(s.reorderProxy))
	mux.HandleFunc("GET /api/proxies/{id}", s.requireManager(s.getProxy))
	mux.HandleFunc("PATCH /api/proxies/{id}", s.requireManager(s.updateProxy))
	mux.HandleFunc("DELETE /api/proxies/{id}", s.requireManager(s.deleteProxy))
	mux.HandleFunc("GET /api/proxies/{id}/clients", s.requireManager(s.listProxyClients))
	mux.HandleFunc("POST /api/proxies/{id}/clients", s.requireManager(s.createProxyClient))
	mux.HandleFunc("GET /api/clients/{id}", s.requireManager(s.getProxyClient))
	mux.HandleFunc("PATCH /api/clients/{id}", s.requireManager(s.updateProxyClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireManager(s.deleteProxyClient))
	mux.HandleFunc("POST /api/clients/{id}/traffic/reset", s.requireManager(s.resetProxyClientTraffic))
	mux.HandleFunc("GET /api/clients/{id}/share", s.requireManager(s.getProxyClientShare))
	mux.HandleFunc("GET /api/landings", s.requireManager(s.listLandings))
	mux.HandleFunc("POST /api/landings", s.requireManager(s.createLanding))
	mux.HandleFunc("GET /api/landings/{id}", s.requireManager(s.getLanding))
	mux.HandleFunc("GET /api/landings/{id}/share", s.requireManager(s.getLandingShare))
	mux.HandleFunc("PATCH /api/landings/{id}", s.requireManager(s.updateLanding))
	mux.HandleFunc("DELETE /api/landings/{id}", s.requireManager(s.deleteLanding))
	mux.HandleFunc("GET /api/relays", s.requireManager(s.listRelays))
	mux.HandleFunc("POST /api/relays", s.requireManager(s.createRelay))
	mux.HandleFunc("POST /api/relays/{id}/reorder", s.requireManager(s.reorderRelay))
	mux.HandleFunc("GET /api/relays/{id}", s.requireManager(s.getRelay))
	mux.HandleFunc("GET /api/relays/{id}/clients", s.requireManager(s.getRelayClients))
	mux.HandleFunc("GET /api/relays/{id}/landing-share", s.requireManager(s.getRelayLandingShare))
	mux.HandleFunc("PATCH /api/relays/{id}", s.requireManager(s.updateRelay))
	mux.HandleFunc("DELETE /api/relays/{id}", s.requireManager(s.deleteRelay))
	mux.HandleFunc("GET /install-agent.sh", s.installAgent)
	mux.HandleFunc("GET /upgrade-agent.sh", s.upgradeAgentInstaller)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.Handle("/", s.spa())
	return mux
}
