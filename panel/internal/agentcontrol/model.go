package agentcontrol

import (
	"errors"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

const (
	PurposeInitial          = "initial"
	PurposeRebind           = "rebind"
	ConfigSyncPending       = "pending"
	ConfigSyncSuccess       = "success"
	ConfigSyncFailed        = "failed"
	AgentUpgradeUpgrading   = "upgrading"
	AgentUpgradeFailed      = "failed"
	EnrollmentLifetime      = 24 * time.Hour
	maxConfigSyncErrorBytes = 4096

	statusPending = "pending"
	statusOnline  = "online"
	statusOffline = "offline"
)

var (
	ErrInvalidEnrollment           = errors.New("invalid, used, or expired enrollment token")
	ErrInvalidAgentVersion         = errors.New("agent version must be 1-64 characters")
	ErrInvalidAgentMetadata        = errors.New("invalid Agent metadata")
	ErrUnsupportedAgentAPI         = errors.New("unsupported Agent API version")
	ErrAgentImplementationMismatch = errors.New("Agent implementation does not match registration")
	ErrInvalidAgentToken           = errors.New("invalid agent token")
	ErrArchived                    = errors.New("server is archived")
	ErrInvalidConfigResult         = errors.New("invalid Agent config result")
	ErrConfigVersionAhead          = errors.New("Agent config result version is newer than desired state")
	ErrAgentOffline                = errors.New("Agent is offline")
	ErrAgentNotRegistered          = errors.New("Agent is not registered")
	ErrDiagnosticsUnsupported      = errors.New("Agent does not support diagnostics")
	ErrDiagnosticsInProgress       = errors.New("a diagnostic request is already in progress")
	ErrInvalidUpgrade              = errors.New("invalid Agent upgrade")
	ErrAgentNewer                  = errors.New("Agent is newer than Panel; downgrade is not supported")
	ErrAgentAlreadyCurrent         = errors.New("Agent already runs the requested release")
	ErrUnknownAgentVersion         = errors.New("Agent version is not a formal release")
	ErrAgentUpgradeUnsupported     = errors.New("Agent does not support official self-upgrade")

	ErrServerNotFound = errors.New("server not found")
)

type RegisteredAgent struct {
	ID       int64
	ServerID int64
	Token    string
}

type Agent struct {
	ID             int64
	ServerID       int64
	Implementation string
}

type AgentUpgrade struct {
	AlreadyCurrent bool
}

type DesiredState struct {
	Version            int64
	Decommission       bool
	BlockChinaInbound  bool
	OutboundPreference string
	XrayPurge          bool
	RealmPurge         bool
	Proxies            []proxystore.DesiredProxy
	Relays             []relaystore.DesiredRelay
}

type ConfigResult struct {
	Version int64
	Status  string
	Message string
}
