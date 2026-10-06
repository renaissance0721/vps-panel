package proxy

import (
	"errors"
	"time"
)

const (
	ProtocolVLESS       = "vless"
	ProtocolShadowsocks = "shadowsocks"
	TransportTCP        = "tcp"
	SecurityTLS         = "tls"
	SecurityReality     = "reality"
	EntryHostAuto       = "auto"
	EntryHostManual     = "manual"
	ListenFamilyIPv4    = "ipv4"
	ListenFamilyIPv6    = "ipv6"
	ServerFlow          = "xtls-rprx-vision"
	Fingerprint         = "chrome"
	TrafficResetNever   = "never"
	TrafficResetDaily   = "daily"
	TrafficResetWeekly  = "weekly"
	TrafficResetMonthly = "monthly"
	maxNameLength       = 100
)

var (
	ErrNotFound                       = errors.New("proxy not found")
	ErrClientNotFound                 = errors.New("client not found")
	ErrServerNotFound                 = errors.New("server not found")
	ErrInvalidName                    = errors.New("name must be 1-100 characters")
	ErrInvalidPort                    = errors.New("listen port must be 1-65535")
	ErrPortConflict                   = errors.New("listen port is already used on this server")
	ErrInvalidEntryHostMode           = errors.New("entry host mode must be auto or manual")
	ErrInvalidEntryHost               = errors.New("manual entry host must be a hostname or IP address without scheme, path, or port")
	ErrInvalidListenFamily            = errors.New("listen family must be ipv4 or ipv6")
	ErrInvalidSecurity                = errors.New("security must be tls or reality")
	ErrInvalidServerName              = errors.New("server name must be a hostname or IP address")
	ErrInvalidTLS                     = errors.New("TLS certificate and private key are required and must match")
	ErrInvalidTLSMode                 = errors.New("TLS mode must be acme or manual")
	ErrInvalidACMEDomain              = errors.New("ACME TLS requires a valid DNS hostname")
	ErrInvalidReality                 = errors.New("REALITY server name or target is invalid")
	ErrInvalidProtocol                = errors.New("protocol must be vless or shadowsocks")
	ErrInvalidShadowsocksMethod       = errors.New("unsupported Shadowsocks method")
	ErrImmutableProtocol              = errors.New("proxy protocol cannot be changed")
	ErrImmutableShadowsocksMethod     = errors.New("Shadowsocks method cannot be changed")
	ErrShadowsocksClientUDP443        = errors.New("client_udp443 is not supported for Shadowsocks")
	ErrInvalidShadowsocksCredential   = errors.New("invalid stored Shadowsocks credential")
	ErrInvalidShadowsocksUpdate       = errors.New("TLS and REALITY fields are not supported for Shadowsocks")
	ErrConnectionAddressUnavailable   = errors.New("connection address unavailable")
	ErrIPv6Unavailable                = errors.New("server IPv6 listener unavailable")
	ErrInvalidClientTrafficConfig     = errors.New("invalid client traffic configuration")
	ErrInvalidClientExpiration        = errors.New("invalid client expiration")
	ErrAssignmentUserNotFound         = errors.New("assigned user not found")
	ErrInvalidAssignmentRole          = errors.New("client can only be assigned to a carpool account")
	ErrInvalidBillingPeriod           = errors.New("billing period must be 1, 3, 6, 12, or null")
	ErrAssignedClientExists           = errors.New("user already has a client on this proxy")
	ErrInvalidClientRelayPortCount    = errors.New("client relay port count must be between 0 and 5")
	ErrClientRelayPortsUnavailable    = errors.New("no contiguous client relay ports are available")
	ErrClientRelayPortsActive         = errors.New("client has active user relays")
	ErrClientNotAssigned              = errors.New("client is not assigned to a user")
	ErrReferencedByRelay              = errors.New("proxy is referenced by a relay")
	ErrReferencedBySubscription       = errors.New("proxy is referenced by a subscription published node")
	ErrManagedRuntimePurgeUnsupported = errors.New("Agent does not support managed runtime purge")
	ErrServerDecommissioning          = errors.New("server is decommissioning")
	ErrSubscriptionManagedClient      = errors.New("client is managed by the subscription system")
	ErrNotDistributable               = errors.New("proxy server was not created by an administrator")
)

type Proxy struct {
	ID               int64
	ServerID         int64
	ServerName       string
	ServerIPv4       []string
	ServerIPv6       []string
	ServerPublicIPv4 string
	ServerPublicIPv6 string
	Name             string
	Protocol         string
	ListenFamily     string
	ListenPort       int
	EntryHostMode    string
	EntryHost        string
	EntryAddress     string
	Enabled          bool
	Config           PublicConfig
	Clients          []ClientSummary
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type PublicConfig struct {
	Transport                string
	Security                 string
	ServerFlow               string
	ServerName               string
	Fingerprint              string
	TLSCertificateConfigured bool
	TLSMode                  string
	RealityTarget            string
	Method                   string
	Network                  string
}

type ClientSummary struct {
	ID                  int64
	ProxyID             int64
	Name                string
	AssignedUserID      *int64
	AssignedUsername    string
	BillingPeriodMonths *int
	UserRelayPortStart  *int
	UserRelayPortEnd    *int
	UserRelayPortCount  int
	ClientUDP443        bool
	Enabled             bool
	ExpiresAt           *time.Time
	TrafficLimitBytes   *int64
	TrafficResetMode    string
	TrafficResetWeekday int
	TrafficResetDay     int
	TrafficResetTime    string
	Metrics             *ClientMetrics
	SubscriptionManaged bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Client struct {
	ID                  int64
	ProxyID             int64
	Name                string
	ProxyName           string
	ServerID            int64
	ServerName          string
	ProxyEntryHostMode  string
	ProxyEntryHost      string
	AssignedUserID      *int64
	AssignedUsername    string
	BillingPeriodMonths *int
	UserRelayPortStart  *int
	UserRelayPortEnd    *int
	UserRelayPortCount  int
	UUID                string
	Password            string
	Protocol            string
	ClientUDP443        bool
	Enabled             bool
	ExpiresAt           *time.Time
	TrafficLimitBytes   *int64
	TrafficResetMode    string
	TrafficResetWeekday int
	TrafficResetDay     int
	TrafficResetTime    string
	Metrics             *ClientMetrics
	SubscriptionManaged bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
	effectiveEnabled    bool
}

type CreateInput struct {
	ServerID          int64
	Name              string
	ListenPort        int
	ListenFamily      string
	EntryHostMode     string
	EntryHost         string
	Enabled           bool
	Security          string
	TLSMode           string
	ServerName        string
	Certificate       string
	PrivateKey        string
	RealityTarget     string
	FirstClientName   string
	FirstClientUDP443 bool
	Protocol          string
	Method            string
}

type UpdateInput struct {
	Name          *string
	ListenPort    *int
	ListenFamily  *string
	EntryHostMode *string
	EntryHost     *string
	Enabled       *bool
	Security      *string
	TLSMode       *string
	ServerName    *string
	Certificate   *string
	PrivateKey    *string
	RealityTarget *string
	Protocol      *string
	Method        *string
}

type ClientCreateInput struct {
	Name         string
	ClientUDP443 bool
	Enabled      bool
	ExpiresAt    *time.Time
	Traffic      ClientTrafficConfig
}

type AssignedClientCreateInput struct {
	UserID              int64
	ProxyID             int64
	Name                string
	ClientUDP443        bool
	Enabled             bool
	ExpiresAt           *time.Time
	Traffic             ClientTrafficConfig
	BillingPeriodMonths *int
	UserRelayPortCount  int
}

type ClientUpdateInput struct {
	Name         *string
	ClientUDP443 *bool
	Enabled      *bool
	ExpiresAtSet bool
	ExpiresAt    *time.Time
	Traffic      *ClientTrafficConfig
}

type ClientTrafficConfig struct {
	LimitBytes *int64
	ResetMode  string
	Weekday    int
	Day        int
	ResetTime  string
}

type Mutation struct {
	ServerID int64
	Version  int64
}

type storedConfig struct {
	Transport   string             `json:"transport,omitempty"`
	Security    string             `json:"security,omitempty"`
	ServerFlow  string             `json:"server_flow,omitempty"`
	ServerName  string             `json:"server_name,omitempty"`
	Fingerprint string             `json:"fingerprint,omitempty"`
	TLS         *storedTLS         `json:"tls,omitempty"`
	Reality     *storedReality     `json:"reality,omitempty"`
	Shadowsocks *storedShadowsocks `json:"shadowsocks,omitempty"`
}

type storedCredential struct {
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
}
