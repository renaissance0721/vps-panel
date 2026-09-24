package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"
)

func normalizeCreate(input CreateInput) (Relay, error) {
	listenAddress := input.ListenAddress
	if strings.TrimSpace(listenAddress) == "" {
		listenAddress = "0.0.0.0"
	}
	return normalizeRelay(Relay{
		ServerID: input.ServerID, Name: input.Name, ListenAddress: listenAddress,
		ListenPort: input.ListenPort, EntryHostMode: input.EntryHostMode, EntryHost: input.EntryHost,
		TargetType:    input.TargetType,
		TargetProxyID: input.TargetProxyID, TargetClientID: input.TargetClientID, TargetLandingID: input.TargetLandingID,
		TargetHost: input.TargetHost,
		TargetPort: input.TargetPort, Network: input.Network, Enabled: input.Enabled,
	})
}

func ValidateCreateInput(input CreateInput) error {
	value, err := normalizeCreate(input)
	if err != nil {
		return err
	}
	if value.TargetType == TargetProxy && value.TargetClientID == nil {
		return ErrInvalidTargetClient
	}
	return nil
}

func ValidateUpdateInput(value Relay, input UpdateInput) (Relay, error) {
	targetProxyChanged := input.TargetProxyID != nil && (value.TargetProxyID == nil || *value.TargetProxyID != *input.TargetProxyID)
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.ListenAddress != nil {
		value.ListenAddress = *input.ListenAddress
	}
	if input.ListenPort != nil {
		value.ListenPort = *input.ListenPort
	}
	if input.EntryHostMode != nil {
		value.EntryHostMode = *input.EntryHostMode
	}
	if input.EntryHost != nil {
		value.EntryHost = *input.EntryHost
	}
	if input.TargetType != nil {
		value.TargetType = *input.TargetType
	}
	if input.TargetProxyID != nil {
		id := *input.TargetProxyID
		value.TargetProxyID = &id
	}
	if input.TargetClientID != nil {
		id := *input.TargetClientID
		value.TargetClientID = &id
	} else if targetProxyChanged {
		value.TargetClientID = nil
	}
	if input.TargetLandingID != nil {
		id := *input.TargetLandingID
		value.TargetLandingID = &id
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
	value, err := normalizeRelay(value)
	if err != nil {
		return Relay{}, err
	}
	if value.TargetType == TargetProxy && targetProxyChanged && value.TargetClientID == nil {
		return Relay{}, ErrInvalidTargetClient
	}
	return value, nil
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
	entryHostMode, entryHost, err := normalizeRelayEntryHost(value.EntryHostMode, value.EntryHost)
	if err != nil {
		return Relay{}, err
	}
	value.EntryHostMode, value.EntryHost = entryHostMode, entryHost
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
		value.TargetLandingID = nil
	case TargetLanding:
		if value.TargetLandingID == nil || *value.TargetLandingID <= 0 {
			return Relay{}, ErrInvalidTarget
		}
		value.TargetProxyID = nil
		value.TargetClientID = nil
		value.TargetHost = ""
		value.TargetPort = 0
	case TargetManual:
		value.TargetProxyID = nil
		value.TargetClientID = nil
		value.TargetLandingID = nil
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
	if value.TargetType == TargetManual {
		value.TargetAddressReady = true
		return nil
	}
	if value.TargetType == TargetLanding {
		var landingID int64
		err := query.QueryRowContext(ctx,
			`SELECT id FROM landing_nodes WHERE id = ?`, *value.TargetLandingID,
		).Scan(&landingID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLandingNotFound
		}
		if err != nil {
			return fmt.Errorf("validate relay target landing: %w", err)
		}
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
	if value.TargetClientID != nil {
		var clientID int64
		err := query.QueryRowContext(ctx,
			`SELECT id FROM clients WHERE id = ? AND proxy_id = ?`, *value.TargetClientID, *value.TargetProxyID,
		).Scan(&clientID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidTargetClient
		}
		if err != nil {
			return fmt.Errorf("validate relay target client: %w", err)
		}
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

func normalizeRelayEntryHost(mode, host string) (string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == EntryHostAuto {
		return EntryHostAuto, "", nil
	}
	if mode != EntryHostManual {
		return "", "", ErrInvalidEntryHostMode
	}
	host, err := normalizeHost(host)
	if err != nil {
		return "", "", ErrInvalidEntryHost
	}
	return EntryHostManual, host, nil
}

func validPort(value int) bool { return value >= 1 && value <= 65535 }

func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
