package server

import (
	"errors"
	"time"
)

const (
	StatusPending           = "pending"
	StatusOnline            = "online"
	StatusOffline           = "offline"
	VisibilityPublic        = "public"
	VisibilityPrivate       = "private"
	OutboundAuto            = "auto"
	OutboundPreferIPv4      = "prefer_ipv4"
	OutboundPreferIPv6      = "prefer_ipv6"
	TrafficSingle           = "single"
	TrafficBidirectional    = "bidirectional"
	maxNameLength           = 100
	maxIPAddresses          = 16
	defaultTrafficResetDay  = 1
	defaultTrafficResetTime = "00:00"
)

var (
	ErrInvalidName               = errors.New("server name must be 1-100 characters")
	ErrNotFound                  = errors.New("server not found")
	ErrInvalidSystemInfo         = errors.New("invalid system information")
	ErrInvalidMetrics            = errors.New("invalid server metrics")
	ErrInvalidTrafficConfig      = errors.New("invalid server traffic configuration")
	ErrInvalidTrafficTarget      = errors.New("invalid server traffic target")
	ErrInvalidVisibility         = errors.New("invalid server visibility")
	ErrInvalidServerAccess       = errors.New("invalid server access list")
	ErrInvalidOutboundPreference = errors.New("invalid server outbound preference")
)

type Server struct {
	ID                       int64
	Name                     string
	Status                   string
	Visibility               string
	OutboundPreference       string
	BlockChinaInbound        bool
	AccessUserIDs            []int64
	ArchivedAt               *time.Time
	ExpiresAt                *time.Time
	MonthlyTrafficLimitBytes *int64
	TrafficCountMode         string
	TrafficResetDay          int
	TrafficResetTime         string
	LastSeenAt               *time.Time
	AgentImplementation      string
	AgentVersion             string
	AgentAPIVersion          int
	AgentCapabilities        []string
	AgentUpgradeTarget       string
	AgentUpgradeStatus       string
	AgentUpgradeError        string
	SystemInfo               *SystemInfo
	Metrics                  *Metrics
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type CreatedServer struct {
	Server
	EnrollmentToken     string
	EnrollmentExpiresAt time.Time
}
