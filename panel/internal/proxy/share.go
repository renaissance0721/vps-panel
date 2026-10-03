package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/netutil"
)

type ClientShare struct {
	Client
	ProxyName           string
	Address             string
	Port                int
	Security            string
	ServerName          string
	Fingerprint         string
	Flow                string
	RealityPublicKey    string
	RealityShortID      string
	Protocol            string
	Method              string
	Network             string
	ShadowsocksPassword string
	URI                 string
	DisplayName         string
}

type ShareEndpoint struct {
	Address string
	Port    int
}

type ShareOptions struct {
	DisplayName string
}

func (s *Service) GetClientShare(ctx context.Context, id int64) (ClientShare, error) {
	return s.getClientShare(ctx, id, nil, ShareOptions{})
}

func (s *Service) GetClientShareAtEndpoint(ctx context.Context, id int64, endpoint ShareEndpoint) (ClientShare, error) {
	return s.getClientShare(ctx, id, &endpoint, ShareOptions{})
}

func (s *Service) GetClientShareWithOptions(ctx context.Context, id int64, options ShareOptions) (ClientShare, error) {
	return s.getClientShare(ctx, id, nil, options)
}

func (s *Service) GetClientShareAtEndpointWithOptions(ctx context.Context, id int64, endpoint ShareEndpoint, options ShareOptions) (ClientShare, error) {
	return s.getClientShare(ctx, id, &endpoint, options)
}

func (s *Service) getClientShare(ctx context.Context, id int64, endpoint *ShareEndpoint, options ShareOptions) (ClientShare, error) {
	var value Client
	var credentialJSON, configJSON string
	var proxyName, protocol, listenFamily, entryHostMode, entryHost, publicIPv4, publicIPv6, ipv6JSON string
	var listenPort int
	var clientUDP443, enabled, effectiveEnabled, subscriptionManaged int
	var expiresAt, trafficLimit sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.expires_at, clients.traffic_limit_bytes,
		 clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time,
		 clients.effective_enabled_snapshot,
		 EXISTS(SELECT 1 FROM subscriber_clients WHERE client_id = clients.id),
		 clients.created_at, clients.updated_at,
		 proxies.name, proxies.protocol, proxies.listen_family, proxies.listen_port, proxies.entry_host_mode, proxies.entry_host,
		 proxies.config_json, COALESCE(system_info.public_ipv4, ''), COALESCE(system_info.public_ipv6, ''), COALESCE(system_info.ipv6, '[]')
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(
		&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &clientUDP443, &enabled,
		&expiresAt, &trafficLimit, &value.TrafficResetMode, &value.TrafficResetWeekday,
		&value.TrafficResetDay, &value.TrafficResetTime,
		&effectiveEnabled, &subscriptionManaged, &createdAt, &updatedAt, &proxyName, &protocol, &listenFamily, &listenPort, &entryHostMode, &entryHost, &configJSON, &publicIPv4, &publicIPv6, &ipv6JSON,
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
	value.ExpiresAt = nullableTimeValue(expiresAt)
	value.effectiveEnabled = effectiveEnabled != 0
	value.SubscriptionManaged = subscriptionManaged != 0
	if trafficLimit.Valid && trafficLimit.Int64 > 0 {
		limit := trafficLimit.Int64
		value.TrafficLimitBytes = &limit
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	value.Metrics, err = s.getClientMetrics(ctx, id)
	if err != nil {
		return ClientShare{}, err
	}
	address, port := "", listenPort
	if endpoint == nil {
		var ipv6 []string
		_ = json.Unmarshal([]byte(ipv6JSON), &ipv6)
		publicIPv6 = netutil.EffectivePublicIPv6(publicIPv6, ipv6)
		address, err = resolveEntryAddress(listenFamily, entryHostMode, entryHost, publicIPv4, publicIPv6)
		if err != nil {
			return ClientShare{}, err
		}
	} else {
		address, err = normalizeHost(endpoint.Address, false)
		if err != nil || validatePort(endpoint.Port) != nil {
			return ClientShare{}, ErrConnectionAddressUnavailable
		}
		port = endpoint.Port
	}
	flow := ServerFlow
	if value.ClientUDP443 {
		flow += "-udp443"
	}
	share := ClientShare{
		Client: value, ProxyName: proxyName, Address: address, Port: port,
		Security: config.Security, ServerName: config.ServerName, Fingerprint: config.Fingerprint,
		Flow: flow, Protocol: protocol,
	}
	share.DisplayName = strings.TrimSpace(options.DisplayName)
	if share.DisplayName == "" {
		share.DisplayName = share.ProxyName + " - " + share.Name
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
		share.ShadowsocksPassword = config.Shadowsocks.Password + ":" + share.Password
		share.URI = buildShadowsocksURI(share)
	default:
		return ClientShare{}, ErrInvalidProtocol
	}
	return share, nil
}

func resolveEntryAddress(family, mode, entryHost, publicIPv4, publicIPv6 string) (string, error) {
	mode, entryHost, err := normalizeEntryHost(mode, entryHost)
	if err != nil {
		return "", err
	}
	if mode == EntryHostManual {
		if err := validateEntryHostFamily(family, entryHost); err != nil {
			return "", err
		}
		return entryHost, nil
	}
	if family == ListenFamilyIPv6 {
		ip := net.ParseIP(strings.TrimSpace(publicIPv6))
		if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return "", ErrConnectionAddressUnavailable
		}
		return ip.String(), nil
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
		RawQuery: query.Encode(), Fragment: share.DisplayName,
	}).String()
}
