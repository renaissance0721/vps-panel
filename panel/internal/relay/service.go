package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TargetProxy  = "proxy"
	TargetManual = "manual"
	NetworkTCP   = "tcp"
	NetworkUDP   = "udp"
	NetworkBoth  = "tcp,udp"
	maxNameRunes = 100
)

var (
	ErrNotFound          = errors.New("relay not found")
	ErrServerNotFound    = errors.New("server not found")
	ErrProxyNotFound     = errors.New("target proxy not found")
	ErrInvalidName       = errors.New("relay name must be 1-100 characters")
	ErrInvalidListenIP   = errors.New("listen address must be an IP address")
	ErrInvalidPort       = errors.New("port must be 1-65535")
	ErrInvalidTarget     = errors.New("relay target is invalid")
	ErrInvalidNetwork    = errors.New("relay network must be tcp, udp, or tcp,udp")
	ErrPortConflict      = errors.New("relay listen port conflicts with an existing listener")
	ErrTargetUnavailable = errors.New("relay target address is unavailable")
)

type Relay struct {
	ID                 int64
	ServerID           int64
	ServerName         string
	ServerPublicIPv4   string
	Name               string
	ListenAddress      string
	ListenPort         int
	TargetType         string
	TargetProxyID      *int64
	TargetProxyName    string
	TargetHost         string
	TargetPort         int
	TargetAddressReady bool
	Network            string
	Enabled            bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type DesiredRelay struct {
	ID            int64  `json:"id"`
	ListenAddress string `json:"listen_address"`
	ListenPort    int    `json:"listen_port"`
	TargetHost    string `json:"target_host"`
	TargetPort    int    `json:"target_port"`
	Network       string `json:"network"`
}

type CreateInput struct {
	ServerID      int64
	Name          string
	ListenAddress string
	ListenPort    int
	TargetType    string
	TargetProxyID *int64
	TargetHost    string
	TargetPort    int
	Network       string
	Enabled       bool
}

type UpdateInput struct {
	Name          *string
	ListenAddress *string
	ListenPort    *int
	TargetType    *string
	TargetProxyID *int64
	TargetHost    *string
	TargetPort    *int
	Network       *string
	Enabled       *bool
}

type Mutation struct {
	ServerID int64
	Version  int64
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) List(ctx context.Context) ([]Relay, error) {
	return list(ctx, s.db, `WHERE source.archived_at IS NULL ORDER BY relays.created_at DESC, relays.id DESC`)
}

func (s *Service) Get(ctx context.Context, id int64) (Relay, error) {
	values, err := list(ctx, s.db, `WHERE relays.id = ? AND source.archived_at IS NULL`, id)
	if err != nil {
		return Relay{}, err
	}
	if len(values) == 0 {
		return Relay{}, ErrNotFound
	}
	return values[0], nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Relay, Mutation, error) {
	value, err := normalizeCreate(input)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("begin relay creation: %w", err)
	}
	defer tx.Rollback()
	if err := ensureActiveServer(ctx, tx, value.ServerID); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, 0); err != nil {
		return Relay{}, Mutation{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO relays
		 (server_id, name, listen_address, listen_port, target_type, target_proxy_id,
		  target_host, target_port, network, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ServerID, value.Name, value.ListenAddress, value.ListenPort, value.TargetType,
		nullableID(value.TargetProxyID), value.TargetHost, nullablePort(value), value.Network,
		value.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("create relay: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("read relay id: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("commit relay creation: %w", err)
	}
	created, err := s.Get(ctx, id)
	return created, Mutation{ServerID: value.ServerID, Version: version}, err
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (Relay, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("begin relay update: %w", err)
	}
	defer tx.Rollback()
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.ListenAddress != nil {
		value.ListenAddress = *input.ListenAddress
	}
	if input.ListenPort != nil {
		value.ListenPort = *input.ListenPort
	}
	if input.TargetType != nil {
		value.TargetType = *input.TargetType
	}
	if input.TargetProxyID != nil {
		id := *input.TargetProxyID
		value.TargetProxyID = &id
	}
	if input.TargetHost != nil {
		value.TargetHost = *input.TargetHost
	}
	if input.TargetPort != nil {
		value.TargetPort = *input.TargetPort
	}
	if input.Network != nil {
		value.Network = *input.Network
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	value, err = normalizeRelay(value)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, id); err != nil {
		return Relay{}, Mutation{}, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE relays SET name = ?, listen_address = ?, listen_port = ?, target_type = ?,
		 target_proxy_id = ?, target_host = ?, target_port = ?, network = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		value.Name, value.ListenAddress, value.ListenPort, value.TargetType,
		nullableID(value.TargetProxyID), value.TargetHost, nullablePort(value), value.Network,
		value.Enabled, now.Unix(), id,
	)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("update relay: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("commit relay update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, Mutation{ServerID: value.ServerID, Version: version}, err
}

func (s *Service) Delete(ctx context.Context, id int64) (Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin relay deletion: %w", err)
	}
	defer tx.Rollback()
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relays WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete relay: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit relay deletion: %w", err)
	}
	return Mutation{ServerID: value.ServerID, Version: version}, nil
}

func (s *Service) ListDesired(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID int64) ([]DesiredRelay, error) {
	values, err := list(ctx, query, `WHERE relays.server_id = ? AND relays.enabled = 1 AND source.archived_at IS NULL ORDER BY relays.id`, serverID)
	if err != nil {
		return nil, err
	}
	desired := make([]DesiredRelay, 0, len(values))
	for _, value := range values {
		if !value.TargetAddressReady {
			return nil, fmt.Errorf("%w: relay %d", ErrTargetUnavailable, value.ID)
		}
		desired = append(desired, DesiredRelay{
			ID: value.ID, ListenAddress: value.ListenAddress, ListenPort: value.ListenPort,
			TargetHost: value.TargetHost, TargetPort: value.TargetPort, Network: value.Network,
		})
	}
	return desired, nil
}

func (s *Service) BumpForProxyTarget(ctx context.Context, proxyID, excludeServerID int64) ([]Mutation, error) {
	return s.bumpDependencies(ctx,
		`SELECT DISTINCT relays.server_id FROM relays
		 JOIN servers ON servers.id = relays.server_id
		 WHERE relays.target_type = 'proxy' AND relays.target_proxy_id = ?
		   AND relays.server_id != ? AND servers.archived_at IS NULL
		 ORDER BY relays.server_id`, proxyID, excludeServerID)
}

func (s *Service) BumpForAutoTargetServer(ctx context.Context, targetServerID int64) ([]Mutation, error) {
	return s.bumpDependencies(ctx,
		`SELECT DISTINCT relays.server_id FROM relays
		 JOIN servers ON servers.id = relays.server_id
		 JOIN proxies ON proxies.id = relays.target_proxy_id
		 WHERE relays.target_type = 'proxy' AND proxies.server_id = ?
		   AND proxies.entry_host_mode = 'auto' AND servers.archived_at IS NULL
		 ORDER BY relays.server_id`, targetServerID)
}

func (s *Service) bumpDependencies(ctx context.Context, statement string, arguments ...any) ([]Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin relay dependency update: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list relay dependencies: %w", err)
	}
	serverIDs := make([]int64, 0)
	for rows.Next() {
		var serverID int64
		if err := rows.Scan(&serverID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan relay dependency: %w", err)
		}
		serverIDs = append(serverIDs, serverID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close relay dependencies: %w", err)
	}
	mutations := make([]Mutation, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		version, err := bumpVersion(ctx, tx, serverID, now)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, Mutation{ServerID: serverID, Version: version})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit relay dependency update: %w", err)
	}
	return mutations, nil
}

func normalizeCreate(input CreateInput) (Relay, error) {
	listenAddress := input.ListenAddress
	if strings.TrimSpace(listenAddress) == "" {
		listenAddress = "0.0.0.0"
	}
	return normalizeRelay(Relay{
		ServerID: input.ServerID, Name: input.Name, ListenAddress: listenAddress,
		ListenPort: input.ListenPort, TargetType: input.TargetType,
		TargetProxyID: input.TargetProxyID, TargetHost: input.TargetHost,
		TargetPort: input.TargetPort, Network: input.Network, Enabled: input.Enabled,
	})
}

func normalizeRelay(value Relay) (Relay, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes {
		return Relay{}, ErrInvalidName
	}
	ip := net.ParseIP(strings.TrimSpace(value.ListenAddress))
	if ip == nil {
		return Relay{}, ErrInvalidListenIP
	}
	value.ListenAddress = ip.String()
	if !validPort(value.ListenPort) {
		return Relay{}, ErrInvalidPort
	}
	value.TargetType = strings.ToLower(strings.TrimSpace(value.TargetType))
	value.Network = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value.Network), " ", ""))
	if value.Network != NetworkTCP && value.Network != NetworkUDP && value.Network != NetworkBoth {
		return Relay{}, ErrInvalidNetwork
	}
	switch value.TargetType {
	case TargetProxy:
		if value.TargetProxyID == nil || *value.TargetProxyID <= 0 {
			return Relay{}, ErrInvalidTarget
		}
		value.TargetHost = ""
		value.TargetPort = 0
	case TargetManual:
		value.TargetProxyID = nil
		host, err := normalizeHost(value.TargetHost)
		if err != nil || !validPort(value.TargetPort) {
			return Relay{}, ErrInvalidTarget
		}
		value.TargetHost = host
	default:
		return Relay{}, ErrInvalidTarget
	}
	return value, nil
}

func validateTarget(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, value *Relay) error {
	if value.TargetType != TargetProxy {
		value.TargetAddressReady = true
		return nil
	}
	var proxyID int64
	err := query.QueryRowContext(ctx,
		`SELECT proxies.id FROM proxies JOIN servers ON servers.id = proxies.server_id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, *value.TargetProxyID,
	).Scan(&proxyID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProxyNotFound
	}
	if err != nil {
		return fmt.Errorf("validate relay target proxy: %w", err)
	}
	return nil
}

func ensurePortAvailable(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, serverID int64, port int, network string, excludeRelayID int64) error {
	rows, err := query.QueryContext(ctx,
		`SELECT protocol FROM proxies WHERE server_id = ? AND listen_port = ?`, serverID, port)
	if err != nil {
		return fmt.Errorf("check proxy listener conflicts: %w", err)
	}
	for rows.Next() {
		var protocol string
		if err := rows.Scan(&protocol); err != nil {
			rows.Close()
			return fmt.Errorf("scan proxy listener conflict: %w", err)
		}
		proxyNetwork := NetworkTCP
		if protocol == "shadowsocks" {
			proxyNetwork = NetworkBoth
		}
		if networksOverlap(network, proxyNetwork) {
			rows.Close()
			return ErrPortConflict
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close proxy listener conflicts: %w", err)
	}
	rows, err = query.QueryContext(ctx,
		`SELECT network FROM relays WHERE server_id = ? AND listen_port = ? AND id != ?`,
		serverID, port, excludeRelayID,
	)
	if err != nil {
		return fmt.Errorf("check relay listener conflicts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			return fmt.Errorf("scan relay listener conflict: %w", err)
		}
		if networksOverlap(network, existing) {
			return ErrPortConflict
		}
	}
	return rows.Err()
}

func ProxyPortAvailable(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID int64, port int, protocol string) error {
	network := NetworkTCP
	if protocol == "shadowsocks" {
		network = NetworkBoth
	}
	rows, err := query.QueryContext(ctx,
		`SELECT network FROM relays WHERE server_id = ? AND listen_port = ?`, serverID, port)
	if err != nil {
		return fmt.Errorf("check relay listener conflicts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			return err
		}
		if networksOverlap(network, existing) {
			return ErrPortConflict
		}
	}
	return rows.Err()
}

func IsProxyReferenced(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, proxyID int64) (bool, error) {
	var count int
	if err := query.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM relays WHERE target_type = 'proxy' AND target_proxy_id = ?`, proxyID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check relay proxy references: %w", err)
	}
	return count != 0, nil
}

func list(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, condition string, arguments ...any) ([]Relay, error) {
	rows, err := query.QueryContext(ctx,
		`SELECT relays.id, relays.server_id, source.name, COALESCE(source_info.public_ipv4, ''),
		 relays.name, relays.listen_address, relays.listen_port, relays.target_type,
		 relays.target_proxy_id, relays.target_host, relays.target_port,
		 relays.network, relays.enabled, relays.created_at, relays.updated_at,
		 target_proxy.name, target_proxy.listen_port, target_proxy.entry_host_mode,
		 target_proxy.entry_host, COALESCE(target_info.public_ipv4, ''), target_server.archived_at
		 FROM relays
		 JOIN servers AS source ON source.id = relays.server_id
		 LEFT JOIN server_system_info AS source_info ON source_info.server_id = source.id
		 LEFT JOIN proxies AS target_proxy ON target_proxy.id = relays.target_proxy_id
		 LEFT JOIN servers AS target_server ON target_server.id = target_proxy.server_id
		 LEFT JOIN server_system_info AS target_info ON target_info.server_id = target_server.id `+condition,
		arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf("list relays: %w", err)
	}
	defer rows.Close()
	values := make([]Relay, 0)
	for rows.Next() {
		var value Relay
		var targetProxyID, storedTargetPort, proxyPort, targetArchived sql.NullInt64
		var targetProxyName, entryMode, entryHost, targetPublicIPv4 sql.NullString
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.ID, &value.ServerID, &value.ServerName, &value.ServerPublicIPv4,
			&value.Name, &value.ListenAddress, &value.ListenPort, &value.TargetType,
			&targetProxyID, &value.TargetHost, &storedTargetPort,
			&value.Network, &enabled, &createdAt, &updatedAt,
			&targetProxyName, &proxyPort, &entryMode, &entryHost, &targetPublicIPv4, &targetArchived,
		); err != nil {
			return nil, fmt.Errorf("scan relay: %w", err)
		}
		value.Enabled = enabled != 0
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if targetProxyID.Valid {
			id := targetProxyID.Int64
			value.TargetProxyID = &id
		}
		if value.TargetType == TargetManual {
			value.TargetPort = int(storedTargetPort.Int64)
			value.TargetAddressReady = true
		} else if targetProxyID.Valid && proxyPort.Valid && !targetArchived.Valid {
			value.TargetProxyName = targetProxyName.String
			value.TargetPort = int(proxyPort.Int64)
			if entryMode.String == "manual" {
				value.TargetHost = entryHost.String
			} else {
				value.TargetHost = targetPublicIPv4.String
			}
			value.TargetAddressReady = value.TargetHost != ""
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relays: %w", err)
	}
	return values, nil
}

func getForMutation(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (Relay, error) {
	var value Relay
	var targetProxyID, targetPort sql.NullInt64
	var enabled int
	err := query.QueryRowContext(ctx,
		`SELECT relays.id, relays.server_id, relays.name, relays.listen_address,
		 relays.listen_port, relays.target_type, relays.target_proxy_id,
		 relays.target_host, relays.target_port, relays.network, relays.enabled
		 FROM relays JOIN servers ON servers.id = relays.server_id
		 WHERE relays.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&value.ID, &value.ServerID, &value.Name, &value.ListenAddress,
		&value.ListenPort, &value.TargetType, &targetProxyID,
		&value.TargetHost, &targetPort, &value.Network, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Relay{}, ErrNotFound
	}
	if err != nil {
		return Relay{}, fmt.Errorf("read relay: %w", err)
	}
	if targetProxyID.Valid {
		id := targetProxyID.Int64
		value.TargetProxyID = &id
	}
	if targetPort.Valid {
		value.TargetPort = int(targetPort.Int64)
	}
	value.Enabled = enabled != 0
	return value, nil
}

func ensureActiveServer(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, serverID int64) error {
	var exists int
	err := query.QueryRowContext(ctx,
		`SELECT 1 FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	}
	if err != nil {
		return fmt.Errorf("validate relay server: %w", err)
	}
	return nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`, now.Unix(), serverID,
	)
	if err != nil {
		return 0, fmt.Errorf("bump relay desired state version: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read relay desired state update: %w", err)
	}
	if count != 1 {
		return 0, ErrServerNotFound
	}
	var version int64
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version FROM servers WHERE id = ?`, serverID,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("read relay desired state version: %w", err)
	}
	return version, nil
}

func networksOverlap(left, right string) bool {
	return (hasProtocol(left, NetworkTCP) && hasProtocol(right, NetworkTCP)) ||
		(hasProtocol(left, NetworkUDP) && hasProtocol(right, NetworkUDP))
}

func hasProtocol(network, protocol string) bool {
	return network == protocol || network == NetworkBoth
}

func normalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return "", ErrInvalidTarget
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String(), nil
	}
	if strings.Contains(value, ":") || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", ErrInvalidTarget
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidTarget
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return "", ErrInvalidTarget
			}
		}
	}
	return strings.ToLower(value), nil
}

func validPort(value int) bool { return value >= 1 && value <= 65535 }

func nullableID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePort(value Relay) any {
	if value.TargetType == TargetManual {
		return value.TargetPort
	}
	return nil
}

func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
