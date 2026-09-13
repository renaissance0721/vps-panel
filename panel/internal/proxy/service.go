package proxy

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ProtocolVLESS              = "vless"
	ProtocolShadowsocks        = "shadowsocks"
	TransportTCP               = "tcp"
	ShadowsocksNetwork         = "tcp,udp"
	ShadowsocksMethodAES128GCM = "2022-blake3-aes-128-gcm"
	ShadowsocksMethodAES256GCM = "2022-blake3-aes-256-gcm"
	SecurityTLS                = "tls"
	SecurityReality            = "reality"
	EntryHostAuto              = "auto"
	EntryHostManual            = "manual"
	ServerFlow                 = "xtls-rprx-vision"
	Fingerprint                = "chrome"
	maxNameLength              = 100
)

var (
	ErrNotFound                     = errors.New("proxy not found")
	ErrClientNotFound               = errors.New("client not found")
	ErrServerNotFound               = errors.New("server not found")
	ErrInvalidName                  = errors.New("name must be 1-100 characters")
	ErrInvalidPort                  = errors.New("listen port must be 1-65535")
	ErrPortConflict                 = errors.New("listen port is already used on this server")
	ErrInvalidEntryHostMode         = errors.New("entry host mode must be auto or manual")
	ErrInvalidEntryHost             = errors.New("manual entry host must be a hostname or IP address without scheme, path, or port")
	ErrInvalidSecurity              = errors.New("security must be tls or reality")
	ErrInvalidServerName            = errors.New("server name must be a hostname or IP address")
	ErrInvalidTLS                   = errors.New("TLS certificate and private key are required and must match")
	ErrInvalidReality               = errors.New("REALITY server name or target is invalid")
	ErrInvalidProtocol              = errors.New("protocol must be vless or shadowsocks")
	ErrInvalidShadowsocksMethod     = errors.New("unsupported Shadowsocks method")
	ErrImmutableProtocol            = errors.New("proxy protocol cannot be changed")
	ErrImmutableShadowsocksMethod   = errors.New("Shadowsocks method cannot be changed")
	ErrShadowsocksClientUDP443      = errors.New("client_udp443 is not supported for Shadowsocks")
	ErrInvalidShadowsocksCredential = errors.New("invalid stored Shadowsocks credential")
	ErrInvalidShadowsocksUpdate     = errors.New("TLS and REALITY fields are not supported for Shadowsocks")
	ErrLastClient                   = errors.New("a proxy must keep at least one client")
	ErrConnectionAddressUnavailable = errors.New("connection address unavailable")
)

type Proxy struct {
	ID               int64
	ServerID         int64
	ServerName       string
	ServerIPv4       []string
	ServerIPv6       []string
	ServerPublicIPv4 string
	Name             string
	Protocol         string
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
	RealityTarget            string
	RealityPublicKey         string
	RealityShortID           string
	Method                   string
	Network                  string
}

type ClientSummary struct {
	ID           int64
	ProxyID      int64
	Name         string
	UUIDSummary  string
	ClientUDP443 bool
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Client struct {
	ID           int64
	ProxyID      int64
	Name         string
	UUID         string
	Password     string
	Protocol     string
	ClientUDP443 bool
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ClientShare struct {
	Client
	ProxyName        string
	Address          string
	Port             int
	Security         string
	ServerName       string
	Fingerprint      string
	Flow             string
	RealityPublicKey string
	RealityShortID   string
	Protocol         string
	Method           string
	Network          string
	URI              string
}

type CreateInput struct {
	ServerID          int64
	Name              string
	ListenPort        int
	EntryHostMode     string
	EntryHost         string
	Enabled           bool
	Security          string
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
	EntryHostMode *string
	EntryHost     *string
	Enabled       *bool
	Security      *string
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
}

type ClientUpdateInput struct {
	Name         *string
	ClientUDP443 *bool
	Enabled      *bool
}

type Mutation struct {
	ServerID int64
	Version  int64
}

type DesiredProxy struct {
	ID          int64               `json:"id"`
	Listen      string              `json:"listen"`
	Port        int                 `json:"port"`
	Protocol    string              `json:"protocol"`
	Transport   string              `json:"transport,omitempty"`
	Security    string              `json:"security,omitempty"`
	ServerFlow  string              `json:"server_flow,omitempty"`
	ServerName  string              `json:"server_name,omitempty"`
	TLS         *DesiredTLS         `json:"tls,omitempty"`
	Reality     *DesiredReality     `json:"reality,omitempty"`
	Shadowsocks *DesiredShadowsocks `json:"shadowsocks,omitempty"`
	Clients     []DesiredClient     `json:"clients"`
}

type DesiredShadowsocks struct {
	Method   string `json:"method"`
	Network  string `json:"network"`
	Password string `json:"password"`
}

type DesiredTLS struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

type DesiredReality struct {
	Target     string `json:"target"`
	PrivateKey string `json:"private_key"`
	ShortID    string `json:"short_id"`
}

type DesiredClient struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
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

type storedShadowsocks struct {
	Method   string `json:"method"`
	Network  string `json:"network"`
	Password string `json:"password"`
}

type storedTLS struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

type storedReality struct {
	Target     string `json:"target"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

type storedCredential struct {
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Proxy, Mutation, error) {
	protocol, err := normalizeProtocol(input.Protocol)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	name, err := validateName(input.Name)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	clientName, err := validateName(input.FirstClientName)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if input.ServerID <= 0 {
		return Proxy{}, Mutation{}, ErrServerNotFound
	}
	if err := validatePort(input.ListenPort); err != nil {
		return Proxy{}, Mutation{}, err
	}
	entryHostMode, entryHost, err := normalizeEntryHost(input.EntryHostMode, input.EntryHost)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	var config storedConfig
	var credential storedCredential
	switch protocol {
	case ProtocolVLESS:
		config, err = newStoredConfig(input.Security, input.ServerName, input.Certificate, input.PrivateKey, input.RealityTarget)
		if err == nil {
			credential, err = newVLESSCredential()
		}
	case ProtocolShadowsocks:
		if input.FirstClientUDP443 {
			return Proxy{}, Mutation{}, ErrShadowsocksClientUDP443
		}
		config, credential, err = newShadowsocksConfigAndCredential(input.Method)
	}
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("encode proxy config: %w", err)
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("encode client credential: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("begin proxy creation: %w", err)
	}
	defer tx.Rollback()

	if err := ensureActiveServer(ctx, tx, input.ServerID); err != nil {
		return Proxy{}, Mutation{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO proxies
		 (server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ServerID, name, protocol, input.ListenPort, entryHostMode, entryHost, input.Enabled,
		string(configJSON), now.Unix(), now.Unix(),
	)
	if isUniqueConstraint(err) {
		return Proxy{}, Mutation{}, ErrPortConflict
	}
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("create proxy: %w", err)
	}
	proxyID, err := result.LastInsertId()
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("read proxy id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO clients
		 (proxy_id, name, credential_json, client_udp443, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		proxyID, clientName, string(credentialJSON), input.FirstClientUDP443, now.Unix(), now.Unix(),
	); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("create first proxy client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, input.ServerID, now)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("commit proxy creation: %w", err)
	}
	value, err := s.Get(ctx, proxyID)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	return value, Mutation{ServerID: input.ServerID, Version: version}, nil
}

func (s *Service) List(ctx context.Context) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE servers.archived_at IS NULL
		 ORDER BY proxies.created_at DESC, proxies.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	defer rows.Close()
	values := make([]Proxy, 0)
	for rows.Next() {
		value, _, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proxy: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxies: %w", err)
	}
	return values, nil
}

func (s *Service) Get(ctx context.Context, id int64) (Proxy, error) {
	value, _, err := scanProxy(s.db.QueryRowContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Proxy{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, fmt.Errorf("get proxy: %w", err)
	}
	clients, err := s.ListClients(ctx, id)
	if err != nil {
		return Proxy{}, err
	}
	value.Clients = make([]ClientSummary, 0, len(clients))
	for _, client := range clients {
		value.Clients = append(value.Clients, summarizeClient(client))
	}
	return value, nil
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (Proxy, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("begin proxy update: %w", err)
	}
	defer tx.Rollback()
	value, config, err := getProxyForMutation(ctx, tx, id)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if input.Protocol != nil {
		protocol, protocolErr := normalizeProtocol(*input.Protocol)
		if protocolErr != nil {
			return Proxy{}, Mutation{}, protocolErr
		}
		if protocol != value.Protocol {
			return Proxy{}, Mutation{}, ErrImmutableProtocol
		}
	}
	if input.Name != nil {
		value.Name, err = validateName(*input.Name)
		if err != nil {
			return Proxy{}, Mutation{}, err
		}
	}
	if input.ListenPort != nil {
		if err := validatePort(*input.ListenPort); err != nil {
			return Proxy{}, Mutation{}, err
		}
		value.ListenPort = *input.ListenPort
	}
	entryHostMode, entryHost := value.EntryHostMode, value.EntryHost
	if input.EntryHostMode != nil {
		entryHostMode = *input.EntryHostMode
	}
	if input.EntryHost != nil {
		entryHost = *input.EntryHost
	}
	value.EntryHostMode, value.EntryHost, err = normalizeEntryHost(entryHostMode, entryHost)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	switch value.Protocol {
	case ProtocolVLESS:
		if input.Method != nil {
			return Proxy{}, Mutation{}, ErrInvalidShadowsocksMethod
		}
		if err := updateStoredConfig(&config, input); err != nil {
			return Proxy{}, Mutation{}, err
		}
	case ProtocolShadowsocks:
		if config.Shadowsocks == nil {
			return Proxy{}, Mutation{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		if input.Method != nil {
			method, methodErr := normalizeShadowsocksMethod(*input.Method)
			if methodErr != nil {
				return Proxy{}, Mutation{}, methodErr
			}
			if method != config.Shadowsocks.Method {
				return Proxy{}, Mutation{}, ErrImmutableShadowsocksMethod
			}
		}
		if input.Security != nil || input.ServerName != nil || input.Certificate != nil || input.PrivateKey != nil || input.RealityTarget != nil {
			return Proxy{}, Mutation{}, ErrInvalidShadowsocksUpdate
		}
	default:
		return Proxy{}, Mutation{}, ErrInvalidProtocol
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("encode proxy config: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE proxies SET name = ?, listen_port = ?, entry_host_mode = ?, entry_host = ?, enabled = ?, config_json = ?, updated_at = ?
		 WHERE id = ?`,
		value.Name, value.ListenPort, value.EntryHostMode, value.EntryHost, value.Enabled, string(configJSON), now.Unix(), id,
	)
	if isUniqueConstraint(err) {
		return Proxy{}, Mutation{}, ErrPortConflict
	}
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("update proxy: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("commit proxy update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, Mutation{ServerID: value.ServerID, Version: version}, err
}

func (s *Service) Delete(ctx context.Context, id int64) (Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin proxy deletion: %w", err)
	}
	defer tx.Rollback()
	value, _, err := getProxyForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete proxy: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit proxy deletion: %w", err)
	}
	return Mutation{ServerID: value.ServerID, Version: version}, nil
}

func (s *Service) ListClients(ctx context.Context, proxyID int64) ([]Client, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM proxies JOIN servers ON servers.id = proxies.server_id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, proxyID,
	).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read proxy for client list: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json
		 FROM clients JOIN proxies ON proxies.id = clients.proxy_id
		 WHERE clients.proxy_id = ? ORDER BY clients.created_at, clients.id`, proxyID,
	)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()
	values := make([]Client, 0)
	for rows.Next() {
		value, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("scan client: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) CreateClient(ctx context.Context, proxyID int64, input ClientCreateInput) (Client, Mutation, error) {
	name, err := validateName(input.Name)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("begin client creation: %w", err)
	}
	defer tx.Rollback()
	proxyValue, config, err := getProxyForMutation(ctx, tx, proxyID)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if proxyValue.Protocol == ProtocolShadowsocks && input.ClientUDP443 {
		return Client{}, Mutation{}, ErrShadowsocksClientUDP443
	}
	credential, err := newCredentialForProxy(proxyValue.Protocol, config)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("encode client credential: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO clients (proxy_id, name, credential_json, client_udp443, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		proxyID, name, string(credentialJSON), input.ClientUDP443, input.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("create client: %w", err)
	}
	clientID, err := result.LastInsertId()
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("read client id: %w", err)
	}
	version, err := bumpVersion(ctx, tx, proxyValue.ServerID, now)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("commit client creation: %w", err)
	}
	value, err := s.GetClient(ctx, clientID)
	return value, Mutation{ServerID: proxyValue.ServerID, Version: version}, err
}

func (s *Service) GetClient(ctx context.Context, id int64) (Client, error) {
	value, err := scanClient(s.db.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrClientNotFound
	}
	if err != nil {
		return Client{}, fmt.Errorf("get client: %w", err)
	}
	return value, nil
}

func (s *Service) UpdateClient(ctx context.Context, id int64, input ClientUpdateInput) (Client, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("begin client update: %w", err)
	}
	defer tx.Rollback()
	value, serverID, err := getClientForMutation(ctx, tx, id)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if input.Name != nil {
		value.Name, err = validateName(*input.Name)
		if err != nil {
			return Client{}, Mutation{}, err
		}
	}
	if input.ClientUDP443 != nil {
		if value.Protocol == ProtocolShadowsocks && *input.ClientUDP443 {
			return Client{}, Mutation{}, ErrShadowsocksClientUDP443
		}
		value.ClientUDP443 = *input.ClientUDP443
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE clients SET name = ?, client_udp443 = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.ClientUDP443, value.Enabled, now.Unix(), id,
	); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("update client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, serverID, now)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("commit client update: %w", err)
	}
	updated, err := s.GetClient(ctx, id)
	return updated, Mutation{ServerID: serverID, Version: version}, err
}

func (s *Service) DeleteClient(ctx context.Context, id int64) (Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin client deletion: %w", err)
	}
	defer tx.Rollback()
	value, serverID, err := getClientForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE proxy_id = ?`, value.ProxyID).Scan(&count); err != nil {
		return Mutation{}, fmt.Errorf("count proxy clients: %w", err)
	}
	if count <= 1 {
		return Mutation{}, ErrLastClient
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, serverID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit client deletion: %w", err)
	}
	return Mutation{ServerID: serverID, Version: version}, nil
}

func (s *Service) GetClientShare(ctx context.Context, id int64) (ClientShare, error) {
	var value Client
	var credentialJSON, configJSON string
	var proxyName, protocol, entryHostMode, entryHost, publicIPv4 string
	var listenPort int
	var clientUDP443, enabled int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.created_at, clients.updated_at,
		 proxies.name, proxies.protocol, proxies.listen_port, proxies.entry_host_mode, proxies.entry_host,
		 proxies.config_json, COALESCE(system_info.public_ipv4, '')
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(
		&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &clientUDP443, &enabled,
		&createdAt, &updatedAt, &proxyName, &protocol, &listenPort, &entryHostMode, &entryHost, &configJSON, &publicIPv4,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientShare{}, ErrClientNotFound
	}
	if err != nil {
		return ClientShare{}, fmt.Errorf("get client share: %w", err)
	}
	credential, err := decodeCredential(protocol, credentialJSON)
	if err != nil {
		return ClientShare{}, err
	}
	config, err := decodeConfig(protocol, configJSON)
	if err != nil {
		return ClientShare{}, err
	}
	if protocol == ProtocolShadowsocks && !validShadowsocksKey(credential.Password, config.Shadowsocks.Method) {
		return ClientShare{}, ErrInvalidShadowsocksCredential
	}
	value.UUID = credential.UUID
	value.Password = credential.Password
	value.Protocol = protocol
	value.ClientUDP443 = clientUDP443 != 0
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	address, err := resolveEntryAddress(entryHostMode, entryHost, publicIPv4)
	if err != nil {
		return ClientShare{}, err
	}
	flow := ServerFlow
	if value.ClientUDP443 {
		flow += "-udp443"
	}
	share := ClientShare{
		Client: value, ProxyName: proxyName, Address: address, Port: listenPort,
		Security: config.Security, ServerName: config.ServerName, Fingerprint: config.Fingerprint,
		Flow: flow, Protocol: protocol,
	}
	if config.Reality != nil {
		share.RealityPublicKey = config.Reality.PublicKey
		share.RealityShortID = config.Reality.ShortID
	}
	switch protocol {
	case ProtocolVLESS:
		share.URI = buildVLESSURI(share)
	case ProtocolShadowsocks:
		share.Method = config.Shadowsocks.Method
		share.Network = config.Shadowsocks.Network
		share.URI = buildShadowsocksURI(share, config.Shadowsocks.Password)
	default:
		return ClientShare{}, ErrInvalidProtocol
	}
	return share, nil
}

func ListDesired(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID int64) ([]DesiredProxy, error) {
	rows, err := query.QueryContext(ctx,
		`SELECT id, listen_port, protocol, config_json FROM proxies
		 WHERE server_id = ? AND enabled = 1 ORDER BY id`, serverID,
	)
	if err != nil {
		return nil, fmt.Errorf("list desired proxies: %w", err)
	}
	type desiredRecord struct {
		value  DesiredProxy
		config storedConfig
	}
	records := make([]desiredRecord, 0)
	for rows.Next() {
		var value DesiredProxy
		var configJSON string
		if err := rows.Scan(&value.ID, &value.Port, &value.Protocol, &configJSON); err != nil {
			return nil, fmt.Errorf("scan desired proxy: %w", err)
		}
		config, err := decodeConfig(value.Protocol, configJSON)
		if err != nil {
			return nil, err
		}
		value.Listen = "0.0.0.0"
		if value.Protocol == ProtocolVLESS {
			value.Transport = config.Transport
			value.Security = config.Security
			value.ServerFlow = config.ServerFlow
			value.ServerName = config.ServerName
		} else {
			value.Shadowsocks = &DesiredShadowsocks{
				Method: config.Shadowsocks.Method, Network: config.Shadowsocks.Network, Password: config.Shadowsocks.Password,
			}
		}
		records = append(records, desiredRecord{value: value, config: config})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate desired proxies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close desired proxies: %w", err)
	}
	values := make([]DesiredProxy, 0, len(records))
	for _, record := range records {
		value, config := record.value, record.config
		if config.TLS != nil {
			value.TLS = &DesiredTLS{Certificate: config.TLS.Certificate, PrivateKey: config.TLS.PrivateKey}
		}
		if config.Reality != nil {
			value.Reality = &DesiredReality{Target: config.Reality.Target, PrivateKey: config.Reality.PrivateKey, ShortID: config.Reality.ShortID}
		}
		clientRows, err := query.QueryContext(ctx,
			`SELECT id, credential_json FROM clients WHERE proxy_id = ? AND enabled = 1 ORDER BY id`, value.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("list desired clients: %w", err)
		}
		value.Clients = make([]DesiredClient, 0)
		for clientRows.Next() {
			var client DesiredClient
			var credentialJSON string
			if err := clientRows.Scan(&client.ID, &credentialJSON); err != nil {
				clientRows.Close()
				return nil, fmt.Errorf("scan desired client: %w", err)
			}
			credential, err := decodeCredential(value.Protocol, credentialJSON)
			if err != nil {
				clientRows.Close()
				return nil, err
			}
			client.UUID = credential.UUID
			client.Password = credential.Password
			if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(client.Password, config.Shadowsocks.Method) {
				clientRows.Close()
				return nil, ErrInvalidShadowsocksCredential
			}
			value.Clients = append(value.Clients, client)
		}
		if err := clientRows.Close(); err != nil {
			return nil, fmt.Errorf("close desired clients: %w", err)
		}
		if value.Protocol == ProtocolShadowsocks && len(value.Clients) == 0 {
			continue
		}
		values = append(values, value)
	}
	return values, nil
}

func newStoredConfig(security, serverName, certificate, privateKey, realityTarget string) (storedConfig, error) {
	security = strings.ToLower(strings.TrimSpace(security))
	serverName, err := normalizeHost(serverName, false)
	if err != nil {
		return storedConfig{}, ErrInvalidServerName
	}
	config := storedConfig{Transport: TransportTCP, Security: security, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint}
	switch security {
	case SecurityTLS:
		if err := validateTLS(certificate, privateKey); err != nil {
			return storedConfig{}, err
		}
		config.TLS = &storedTLS{Certificate: strings.TrimSpace(certificate), PrivateKey: strings.TrimSpace(privateKey)}
	case SecurityReality:
		target, err := normalizeTarget(realityTarget)
		if err != nil {
			return storedConfig{}, err
		}
		privateValue, publicValue, shortID, err := newRealitySecrets()
		if err != nil {
			return storedConfig{}, err
		}
		config.Reality = &storedReality{Target: target, PrivateKey: privateValue, PublicKey: publicValue, ShortID: shortID}
	default:
		return storedConfig{}, ErrInvalidSecurity
	}
	return config, nil
}

func updateStoredConfig(config *storedConfig, input UpdateInput) error {
	security := config.Security
	if input.Security != nil {
		security = strings.ToLower(strings.TrimSpace(*input.Security))
	}
	serverName := config.ServerName
	if input.ServerName != nil {
		var err error
		serverName, err = normalizeHost(*input.ServerName, false)
		if err != nil {
			return ErrInvalidServerName
		}
	}
	switch security {
	case SecurityTLS:
		certificate, privateKey := "", ""
		if config.Security == SecurityTLS && config.TLS != nil {
			certificate, privateKey = config.TLS.Certificate, config.TLS.PrivateKey
		}
		providedCert, providedKey := input.Certificate != nil && strings.TrimSpace(*input.Certificate) != "", input.PrivateKey != nil && strings.TrimSpace(*input.PrivateKey) != ""
		if providedCert != providedKey {
			return ErrInvalidTLS
		}
		if providedCert {
			certificate, privateKey = *input.Certificate, *input.PrivateKey
		}
		if err := validateTLS(certificate, privateKey); err != nil {
			return err
		}
		*config = storedConfig{Transport: TransportTCP, Security: SecurityTLS, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint, TLS: &storedTLS{Certificate: strings.TrimSpace(certificate), PrivateKey: strings.TrimSpace(privateKey)}}
	case SecurityReality:
		target := ""
		var reality *storedReality
		if config.Security == SecurityReality && config.Reality != nil {
			copyValue := *config.Reality
			reality = &copyValue
			target = reality.Target
		} else {
			privateValue, publicValue, shortID, err := newRealitySecrets()
			if err != nil {
				return err
			}
			reality = &storedReality{PrivateKey: privateValue, PublicKey: publicValue, ShortID: shortID}
		}
		if input.RealityTarget != nil {
			target = *input.RealityTarget
		}
		var err error
		reality.Target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		*config = storedConfig{Transport: TransportTCP, Security: SecurityReality, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint, Reality: reality}
	default:
		return ErrInvalidSecurity
	}
	return nil
}

func validateTLS(certificate, privateKey string) error {
	if strings.TrimSpace(certificate) == "" || strings.TrimSpace(privateKey) == "" {
		return ErrInvalidTLS
	}
	if _, err := tls.X509KeyPair([]byte(certificate), []byte(privateKey)); err != nil {
		return ErrInvalidTLS
	}
	return nil
}

func newRealitySecrets() (string, string, string, error) {
	privateBytes := make([]byte, 32)
	if _, err := rand.Read(privateBytes); err != nil {
		return "", "", "", fmt.Errorf("generate REALITY private key: %w", err)
	}
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return "", "", "", fmt.Errorf("generate REALITY key pair: %w", err)
	}
	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return "", "", "", fmt.Errorf("generate REALITY short id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(privateBytes),
		base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), hex.EncodeToString(shortID), nil
}

func newVLESSCredential() (storedCredential, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return storedCredential{}, fmt.Errorf("generate client UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return storedCredential{UUID: encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]}, nil
}

func newShadowsocksConfigAndCredential(method string) (storedConfig, storedCredential, error) {
	method, err := normalizeShadowsocksMethod(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	masterPassword, err := newShadowsocksKey(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	clientPassword, err := newShadowsocksKey(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	return storedConfig{Shadowsocks: &storedShadowsocks{
		Method: method, Network: ShadowsocksNetwork, Password: masterPassword,
	}}, storedCredential{Password: clientPassword}, nil
}

func newCredentialForProxy(protocol string, config storedConfig) (storedCredential, error) {
	switch protocol {
	case ProtocolVLESS:
		return newVLESSCredential()
	case ProtocolShadowsocks:
		if config.Shadowsocks == nil {
			return storedCredential{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		password, err := newShadowsocksKey(config.Shadowsocks.Method)
		return storedCredential{Password: password}, err
	default:
		return storedCredential{}, ErrInvalidProtocol
	}
}

func newShadowsocksKey(method string) (string, error) {
	length, err := shadowsocksKeyLength(method)
	if err != nil {
		return "", err
	}
	value := make([]byte, length)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Shadowsocks key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(value), nil
}

func normalizeProtocol(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ProtocolVLESS, nil
	}
	if value != ProtocolVLESS && value != ProtocolShadowsocks {
		return "", ErrInvalidProtocol
	}
	return value, nil
}

func normalizeShadowsocksMethod(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ShadowsocksMethodAES128GCM, nil
	}
	if _, err := shadowsocksKeyLength(value); err != nil {
		return "", err
	}
	return value, nil
}

func shadowsocksKeyLength(method string) (int, error) {
	switch method {
	case ShadowsocksMethodAES128GCM:
		return 16, nil
	case ShadowsocksMethodAES256GCM:
		return 32, nil
	default:
		return 0, ErrInvalidShadowsocksMethod
	}
}

func validShadowsocksKey(value, method string) bool {
	expected, err := shadowsocksKeyLength(method)
	if err != nil {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == expected
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxNameLength {
		return "", ErrInvalidName
	}
	return value, nil
}

func validatePort(value int) error {
	if value < 1 || value > 65535 {
		return ErrInvalidPort
	}
	return nil
}

func normalizeEntryHost(mode, host string) (string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case EntryHostAuto:
		return mode, "", nil
	case EntryHostManual:
		host, err := normalizeHost(host, false)
		if err != nil {
			return "", "", ErrInvalidEntryHost
		}
		return mode, host, nil
	default:
		return "", "", ErrInvalidEntryHostMode
	}
}

func normalizeHost(value string, optional bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return "", nil
	}
	if value == "" || strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return "", ErrInvalidEntryHost
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String(), nil
	}
	if strings.Contains(value, ":") || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", ErrInvalidEntryHost
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidEntryHost
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return "", ErrInvalidEntryHost
			}
		}
	}
	return strings.ToLower(value), nil
}

func normalizeTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/?#@") {
		return "", ErrInvalidReality
	}
	host, portValue, err := net.SplitHostPort(value)
	if err != nil {
		return "", ErrInvalidReality
	}
	host, err = normalizeHost(host, false)
	if err != nil {
		return "", ErrInvalidReality
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || validatePort(port) != nil {
		return "", ErrInvalidReality
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func decodeConfig(protocol, value string) (storedConfig, error) {
	var config storedConfig
	if err := decodeStrict(value, &config); err != nil {
		return storedConfig{}, fmt.Errorf("decode proxy config: %w", err)
	}
	if protocol == ProtocolShadowsocks {
		if config.Shadowsocks == nil || config.Transport != "" || config.Security != "" || config.ServerFlow != "" ||
			config.ServerName != "" || config.Fingerprint != "" || config.TLS != nil || config.Reality != nil ||
			config.Shadowsocks.Network != ShadowsocksNetwork ||
			!validShadowsocksKey(config.Shadowsocks.Password, config.Shadowsocks.Method) {
			return storedConfig{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		return config, nil
	}
	if protocol != ProtocolVLESS || config.Shadowsocks != nil {
		return storedConfig{}, errors.New("invalid stored proxy protocol config")
	}
	if config.Transport != TransportTCP || config.ServerFlow != ServerFlow || config.Fingerprint != Fingerprint {
		return storedConfig{}, errors.New("invalid stored proxy config")
	}
	serverName, err := normalizeHost(config.ServerName, false)
	if err != nil || serverName != config.ServerName {
		return storedConfig{}, errors.New("invalid stored proxy server name")
	}
	switch config.Security {
	case SecurityTLS:
		if config.TLS == nil || config.Reality != nil || validateTLS(config.TLS.Certificate, config.TLS.PrivateKey) != nil {
			return storedConfig{}, errors.New("invalid stored TLS proxy config")
		}
	case SecurityReality:
		if config.Reality == nil || config.TLS != nil || validateReality(config.Reality) != nil {
			return storedConfig{}, errors.New("invalid stored REALITY proxy config")
		}
	default:
		return storedConfig{}, errors.New("invalid stored proxy security")
	}
	return config, nil
}

func decodeCredential(protocol, value string) (storedCredential, error) {
	var credential storedCredential
	if err := decodeStrict(value, &credential); err != nil {
		return storedCredential{}, errors.New("invalid stored client credential")
	}
	switch protocol {
	case ProtocolVLESS:
		if credential.Password != "" || !validUUID(credential.UUID) {
			return storedCredential{}, errors.New("invalid stored client credential")
		}
	case ProtocolShadowsocks:
		if credential.UUID != "" {
			return storedCredential{}, ErrInvalidShadowsocksCredential
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(credential.Password)
		if err != nil || (len(decoded) != 16 && len(decoded) != 32) {
			return storedCredential{}, ErrInvalidShadowsocksCredential
		}
	default:
		return storedCredential{}, ErrInvalidProtocol
	}
	return credential, nil
}

func decodeStrict(value string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func publicConfig(config storedConfig) PublicConfig {
	value := PublicConfig{Transport: config.Transport, Security: config.Security, ServerFlow: config.ServerFlow, ServerName: config.ServerName, Fingerprint: config.Fingerprint}
	if config.Shadowsocks != nil {
		value.Method = config.Shadowsocks.Method
		value.Network = config.Shadowsocks.Network
	}
	if config.TLS != nil {
		value.TLSCertificateConfigured = config.TLS.Certificate != "" && config.TLS.PrivateKey != ""
	}
	if config.Reality != nil {
		value.RealityTarget = config.Reality.Target
		value.RealityPublicKey = config.Reality.PublicKey
		value.RealityShortID = config.Reality.ShortID
	}
	return value
}

type rowScanner interface{ Scan(...any) error }

func scanProxy(row rowScanner) (Proxy, storedConfig, error) {
	var value Proxy
	var ipv4JSON, ipv6JSON, publicIPv4 sql.NullString
	var enabled int
	var configJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.ServerID, &value.ServerName, &ipv4JSON, &ipv6JSON,
		&publicIPv4, &value.Name, &value.Protocol, &value.ListenPort, &value.EntryHostMode, &value.EntryHost, &enabled,
		&configJSON, &createdAt, &updatedAt); err != nil {
		return Proxy{}, storedConfig{}, err
	}
	entryHostMode, entryHost, err := normalizeEntryHost(value.EntryHostMode, value.EntryHost)
	if err != nil || entryHostMode != value.EntryHostMode || entryHost != value.EntryHost {
		return Proxy{}, storedConfig{}, errors.New("invalid stored proxy entry host")
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Proxy{}, storedConfig{}, err
	}
	value.ServerPublicIPv4 = publicIPv4.String
	value.EntryAddress, err = resolveEntryAddress(value.EntryHostMode, value.EntryHost, value.ServerPublicIPv4)
	if err != nil && !errors.Is(err, ErrConnectionAddressUnavailable) {
		return Proxy{}, storedConfig{}, err
	}
	value.Enabled = enabled != 0
	value.Config = publicConfig(config)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	_ = json.Unmarshal([]byte(ipv4JSON.String), &value.ServerIPv4)
	_ = json.Unmarshal([]byte(ipv6JSON.String), &value.ServerIPv6)
	if value.ServerIPv4 == nil {
		value.ServerIPv4 = []string{}
	}
	if value.ServerIPv6 == nil {
		value.ServerIPv6 = []string{}
	}
	return value, config, nil
}

func scanClient(row rowScanner) (Client, error) {
	var value Client
	var credentialJSON, configJSON string
	var udp443, enabled int
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &udp443, &enabled, &createdAt, &updatedAt, &value.Protocol, &configJSON); err != nil {
		return Client{}, err
	}
	credential, err := decodeCredential(value.Protocol, credentialJSON)
	if err != nil {
		return Client{}, err
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Client{}, err
	}
	if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(credential.Password, config.Shadowsocks.Method) {
		return Client{}, ErrInvalidShadowsocksCredential
	}
	value.UUID = credential.UUID
	value.Password = credential.Password
	value.ClientUDP443 = udp443 != 0
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func summarizeClient(value Client) ClientSummary {
	summary := ""
	if value.UUID != "" {
		summary = value.UUID[:4] + "…" + value.UUID[len(value.UUID)-4:]
	}
	return ClientSummary{ID: value.ID, ProxyID: value.ProxyID, Name: value.Name, UUIDSummary: summary, ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func getProxyForMutation(ctx context.Context, tx *sql.Tx, id int64) (Proxy, storedConfig, error) {
	value, config, err := scanProxy(tx.QueryRowContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Proxy{}, storedConfig{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, storedConfig{}, fmt.Errorf("read proxy: %w", err)
	}
	return value, config, nil
}

func getClientForMutation(ctx context.Context, tx *sql.Tx, id int64) (Client, int64, error) {
	var serverID int64
	var value Client
	var credentialJSON, configJSON string
	var udp443, enabled int
	var createdAt, updatedAt int64
	err := tx.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json, proxies.server_id
		 FROM clients JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &udp443, &enabled, &createdAt, &updatedAt, &value.Protocol, &configJSON, &serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, 0, ErrClientNotFound
	}
	if err != nil {
		return Client{}, 0, fmt.Errorf("read client: %w", err)
	}
	credential, err := decodeCredential(value.Protocol, credentialJSON)
	if err != nil {
		return Client{}, 0, err
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Client{}, 0, err
	}
	if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(credential.Password, config.Shadowsocks.Method) {
		return Client{}, 0, ErrInvalidShadowsocksCredential
	}
	value.UUID, value.Password, value.ClientUDP443, value.Enabled = credential.UUID, credential.Password, udp443 != 0, enabled != 0
	value.CreatedAt, value.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return value, serverID, nil
}

func ensureActiveServer(ctx context.Context, tx *sql.Tx, serverID int64) error {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM servers WHERE id = ? AND archived_at IS NULL`, serverID).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	} else if err != nil {
		return fmt.Errorf("read proxy server: %w", err)
	}
	return nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`, now.Unix(), serverID,
	)
	if err != nil {
		return 0, fmt.Errorf("bump desired state version: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read desired state update count: %w", err)
	}
	if count != 1 {
		return 0, ErrServerNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now.Unix(), serverID,
	); err != nil {
		return 0, fmt.Errorf("mark Agent config sync pending: %w", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
		return 0, fmt.Errorf("read desired state version: %w", err)
	}
	return version, nil
}

func resolveEntryAddress(mode, entryHost, publicIPv4 string) (string, error) {
	mode, entryHost, err := normalizeEntryHost(mode, entryHost)
	if err != nil {
		return "", err
	}
	if mode == EntryHostManual {
		return entryHost, nil
	}
	ip := net.ParseIP(strings.TrimSpace(publicIPv4))
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", ErrConnectionAddressUnavailable
	}
	return ip.To4().String(), nil
}

func buildVLESSURI(share ClientShare) string {
	query := url.Values{}
	query.Set("encryption", "none")
	query.Set("flow", share.Flow)
	query.Set("fp", share.Fingerprint)
	query.Set("security", share.Security)
	query.Set("sni", share.ServerName)
	query.Set("type", TransportTCP)
	if share.Security == SecurityReality {
		query.Set("alpn", "h2,http/1.1")
		query.Set("headerType", "none")
		query.Set("pbk", share.RealityPublicKey)
		query.Set("sid", share.RealityShortID)
	}
	return (&url.URL{
		Scheme: "vless", User: url.User(share.UUID), Host: net.JoinHostPort(share.Address, strconv.Itoa(share.Port)),
		RawQuery: query.Encode(), Fragment: share.ProxyName + " - " + share.Name,
	}).String()
}

func buildShadowsocksURI(share ClientShare, masterPassword string) string {
	return (&url.URL{
		Scheme:   "ss",
		User:     url.UserPassword(share.Method, masterPassword+":"+share.Password),
		Host:     net.JoinHostPort(share.Address, strconv.Itoa(share.Port)),
		Fragment: share.ProxyName + " - " + share.Name,
	}).String()
}

func validateReality(value *storedReality) error {
	if _, err := normalizeTarget(value.Target); err != nil {
		return err
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(value.PrivateKey)
	if err != nil || len(privateBytes) != 32 {
		return ErrInvalidReality
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil || base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != value.PublicKey {
		return ErrInvalidReality
	}
	shortID, err := hex.DecodeString(value.ShortID)
	if err != nil || len(shortID) != 8 {
		return ErrInvalidReality
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
