package api

import (
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

const maxUserRelays = relaystore.MaxUserRelays

var (
	errInvalidRelayPool   = errors.New("invalid user relay pool")
	errRelayPoolNotFound  = errors.New("user relay pool not found")
	errUserRelayLimit     = errors.New("user relay limit reached")
	errUserRelayPortsFull = errors.New("user relay port range exhausted")
	errInvalidPublicIP    = errors.New("target must be a public IP address")
)

type userRelayPoolRequest struct {
	Enabled       bool   `json:"enabled"`
	ListenAddress string `json:"listen_address"`
	EntryHost     string `json:"entry_host"`
	PortStart     int    `json:"port_start"`
	PortEnd       int    `json:"port_end"`
}

type userRelayPool struct {
	ID               int64     `json:"id"`
	ServerID         int64     `json:"server_id"`
	ServerName       string    `json:"server_name"`
	Enabled          bool      `json:"enabled"`
	ListenAddress    string    `json:"listen_address"`
	EntryHost        string    `json:"entry_host"`
	EffectiveEntry   string    `json:"effective_entry_host"`
	PortStart        int       `json:"port_start"`
	PortEnd          int       `json:"port_end"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	serverPublicIPv4 string
}

type createMyRelayRequest struct {
	RelaySourceID int64  `json:"relay_source_id"`
	Name          string `json:"name"`
	TargetIP      string `json:"target_ip"`
	TargetPort    int    `json:"target_port"`
}

type myRelayResponse struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SourceName   string    `json:"source_name"`
	EntryHost    string    `json:"entry_host"`
	ListenPort   int       `json:"listen_port"`
	TargetIP     string    `json:"target_ip"`
	TargetPort   int       `json:"target_port"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	EntryAddress string    `json:"entry_address"`
}

func (s *server) getUserRelayPool(w http.ResponseWriter, r *http.Request, user auth.User) {
	serverID, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok || !s.requireServerAccess(w, r, user, serverID) {
		return
	}
	pool, err := s.readUserRelayPool(r, serverID)
	if errors.Is(err, errRelayPoolNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"pool": nil})
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pool": pool})
}

func (s *server) updateUserRelayPool(w http.ResponseWriter, r *http.Request, user auth.User) {
	serverID, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok || !s.requireServerAccess(w, r, user, serverID) {
		return
	}
	var request userRelayPoolRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	listenAddress := strings.TrimSpace(request.ListenAddress)
	entryHost, err := normalizeRelayPoolEntryHost(request.EntryHost)
	if err != nil || (listenAddress != "0.0.0.0" && listenAddress != "::") ||
		request.PortStart < 1 || request.PortEnd > 65535 || request.PortStart > request.PortEnd ||
		(listenAddress == "::" && entryHost == "") {
		writeUserRelayError(w, errInvalidRelayPool)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	_, err = s.db.ExecContext(r.Context(),
		`INSERT INTO user_relay_pools
		 (server_id, enabled, listen_address, entry_host, port_start, port_end, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET enabled = excluded.enabled,
		 listen_address = excluded.listen_address, entry_host = excluded.entry_host,
		 port_start = excluded.port_start, port_end = excluded.port_end, updated_at = excluded.updated_at`,
		serverID, request.Enabled, listenAddress, entryHost, request.PortStart, request.PortEnd, now.Unix(), now.Unix(),
	)
	if err != nil {
		writeInternalError(w)
		return
	}
	pool, err := s.readUserRelayPool(r, serverID)
	if err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pool": pool})
}

func (s *server) readUserRelayPool(r *http.Request, serverID int64) (userRelayPool, error) {
	var pool userRelayPool
	var enabled int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(r.Context(),
		`SELECT pools.id, pools.server_id, servers.name, pools.enabled, pools.listen_address,
		 pools.entry_host, pools.port_start, pools.port_end, pools.created_at, pools.updated_at,
		 COALESCE(system_info.public_ipv4, '')
		 FROM user_relay_pools AS pools
		 JOIN servers ON servers.id = pools.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE pools.server_id = ? AND servers.archived_at IS NULL`, serverID,
	).Scan(&pool.ID, &pool.ServerID, &pool.ServerName, &enabled, &pool.ListenAddress,
		&pool.EntryHost, &pool.PortStart, &pool.PortEnd, &createdAt, &updatedAt, &pool.serverPublicIPv4)
	if errors.Is(err, sql.ErrNoRows) {
		return userRelayPool{}, errRelayPoolNotFound
	}
	if err != nil {
		return userRelayPool{}, err
	}
	pool.Enabled = enabled != 0
	pool.CreatedAt = time.Unix(createdAt, 0).UTC()
	pool.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	pool.EffectiveEntry = pool.EntryHost
	if pool.EffectiveEntry == "" {
		pool.EffectiveEntry = pool.serverPublicIPv4
	}
	return pool, nil
}

func (s *server) listMyRelaySources(w http.ResponseWriter, r *http.Request, _ auth.User) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT pools.id, servers.name, pools.entry_host, COALESCE(system_info.public_ipv4, '')
		 FROM user_relay_pools AS pools
		 JOIN servers ON servers.id = pools.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE pools.enabled = 1 AND servers.archived_at IS NULL
		 ORDER BY servers.name, pools.id`,
	)
	if err != nil {
		writeInternalError(w)
		return
	}
	defer rows.Close()
	type sourceResponse struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		EntryHost string `json:"entry_host"`
	}
	values := make([]sourceResponse, 0)
	for rows.Next() {
		var value sourceResponse
		var configured, publicIPv4 string
		if err := rows.Scan(&value.ID, &value.Name, &configured, &publicIPv4); err != nil {
			writeInternalError(w)
			return
		}
		value.EntryHost = configured
		if value.EntryHost == "" {
			if _, err := validatePublicTargetIP(publicIPv4); err != nil {
				continue
			}
			value.EntryHost = publicIPv4
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": values})
}

func (s *server) listMyRelays(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.relays.ListByOwner(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]myRelayResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toMyRelayResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": response})
}

func (s *server) createMyRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createMyRelayRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	targetIP, err := validatePublicTargetIP(request.TargetIP)
	if err != nil {
		writeUserRelayError(w, err)
		return
	}
	if request.TargetPort < 1 || request.TargetPort > 65535 {
		writeRelayError(w, relaystore.ErrInvalidPort)
		return
	}
	var pool userRelayPool
	var enabled int
	var createdAt, updatedAt int64
	err = s.db.QueryRowContext(r.Context(),
		`SELECT pools.id, pools.server_id, servers.name, pools.enabled, pools.listen_address,
		 pools.entry_host, pools.port_start, pools.port_end, pools.created_at, pools.updated_at,
		 COALESCE(system_info.public_ipv4, '')
		 FROM user_relay_pools AS pools
		 JOIN servers ON servers.id = pools.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE pools.id = ? AND pools.enabled = 1 AND servers.archived_at IS NULL`, request.RelaySourceID,
	).Scan(&pool.ID, &pool.ServerID, &pool.ServerName, &enabled, &pool.ListenAddress,
		&pool.EntryHost, &pool.PortStart, &pool.PortEnd, &createdAt, &updatedAt, &pool.serverPublicIPv4)
	if errors.Is(err, sql.ErrNoRows) {
		writeUserRelayError(w, errRelayPoolNotFound)
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	if pool.EntryHost == "" {
		if _, err := validatePublicTargetIP(pool.serverPublicIPv4); err != nil {
			writeUserRelayError(w, errRelayPoolNotFound)
			return
		}
	}
	supported, err := s.serverSupportsCapability(r, pool.ServerID, agentcontrol.CapabilityRelayRealm)
	if err != nil {
		writeServerError(w, err)
		return
	}
	if !supported {
		writeError(w, http.StatusConflict, "当前中转节点暂不可用")
		return
	}
	entryMode := relaystore.EntryHostAuto
	if pool.EntryHost != "" {
		entryMode = relaystore.EntryHostManual
	}
	for port := pool.PortStart; port <= pool.PortEnd; port++ {
		value, mutation, err := s.relays.Create(r.Context(), relaystore.CreateInput{
			ServerID: pool.ServerID, OwnerUserID: &user.ID, Name: request.Name,
			ListenAddress: pool.ListenAddress, ListenPort: port,
			EntryHostMode: entryMode, EntryHost: pool.EntryHost,
			TargetType: relaystore.TargetManual, TargetHost: targetIP.String(), TargetPort: request.TargetPort,
			Network: relaystore.NetworkTCP, Enabled: true,
		})
		if errors.Is(err, relaystore.ErrPortConflict) {
			continue
		}
		if errors.Is(err, relaystore.ErrUserRelayLimit) {
			writeUserRelayError(w, errUserRelayLimit)
			return
		}
		if err != nil {
			writeRelayError(w, err)
			return
		}
		s.notifyRelayMutations([]relaystore.Mutation{mutation})
		writeJSON(w, http.StatusCreated, map[string]any{"relay": toMyRelayResponse(value)})
		return
	}
	writeUserRelayError(w, errUserRelayPortsFull)
}

func (s *server) deleteMyRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	value, err := s.relays.Get(r.Context(), id)
	if err != nil || value.OwnerUserID == nil || *value.OwnerUserID != user.ID {
		writeRelayError(w, relaystore.ErrNotFound)
		return
	}
	s.deleteOwnedRelay(w, r, value)
}

func (s *server) listAdminUserRelays(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.relays.ListUserOwned(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	type adminUserRelayResponse struct {
		myRelayResponse
		Username string `json:"username"`
	}
	response := make([]adminUserRelayResponse, 0, len(values))
	for _, value := range values {
		response = append(response, adminUserRelayResponse{myRelayResponse: toMyRelayResponse(value), Username: value.OwnerUsername})
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": response})
}

func (s *server) deleteAdminUserRelay(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	value, err := s.relays.Get(r.Context(), id)
	if err != nil || value.OwnerUserID == nil {
		writeRelayError(w, relaystore.ErrNotFound)
		return
	}
	s.deleteOwnedRelay(w, r, value)
}

func (s *server) deleteOwnedRelay(w http.ResponseWriter, r *http.Request, value relaystore.Relay) {
	serverValue, err := s.servers.Get(r.Context(), value.ServerID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	allowManagedPurge := serverValue.AgentVersion == "" || agentcontrol.DeclaresCapability(agentcontrol.Metadata{
		Implementation: serverValue.AgentImplementation,
		Version:        serverValue.AgentVersion,
		APIVersion:     serverValue.AgentAPIVersion,
		Capabilities:   serverValue.AgentCapabilities,
	}, agentcontrol.CapabilityManagedRuntimePurge)
	mutation, err := s.relays.DeleteWithManagedPurge(r.Context(), value.ID, allowManagedPurge)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeNoContent(w)
}

func toMyRelayResponse(value relaystore.Relay) myRelayResponse {
	return myRelayResponse{
		ID: value.ID, Name: value.Name, SourceName: value.ServerName,
		EntryHost: value.EntryAddress, EntryAddress: relaystore.JoinHostPort(value.EntryAddress, value.ListenPort),
		ListenPort: value.ListenPort, TargetIP: value.TargetHost, TargetPort: value.TargetPort,
		Enabled: value.Enabled, CreatedAt: value.CreatedAt,
	}
}

func normalizeRelayPoolEntryHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return "", errInvalidRelayPool
	}
	if address, err := netip.ParseAddr(strings.Trim(value, "[]")); err == nil {
		return address.String(), nil
	}
	if strings.Contains(value, ":") || !validDNSHostname(value) {
		return "", errInvalidRelayPool
	}
	return strings.ToLower(value), nil
}

func validatePublicTargetIP(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, errInvalidPublicIP
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() || isSpecialUseTarget(address) {
		return netip.Addr{}, errInvalidPublicIP
	}
	return address, nil
}

func isSpecialUseTarget(address netip.Addr) bool {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func writeUserRelayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidRelayPool):
		writeError(w, http.StatusBadRequest, "用户中转池配置无效")
	case errors.Is(err, errRelayPoolNotFound):
		writeError(w, http.StatusNotFound, "中转来源不存在或不可用")
	case errors.Is(err, errUserRelayLimit):
		writeError(w, http.StatusConflict, "每个普通用户最多可创建 "+strconv.Itoa(maxUserRelays)+" 条中转")
	case errors.Is(err, errUserRelayPortsFull):
		writeError(w, http.StatusConflict, "中转端口范围已用尽")
	case errors.Is(err, errInvalidPublicIP):
		writeError(w, http.StatusBadRequest, "落地地址必须是可公开访问的 IP，不能使用域名、内网或保留地址")
	default:
		writeInternalError(w)
	}
}
