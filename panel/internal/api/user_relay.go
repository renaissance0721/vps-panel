package api

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

const maxUserRelays = relaystore.MaxUserRelays

var (
	errUserRelayLimit     = errors.New("user relay limit reached")
	errUserRelayPortsFull = errors.New("user relay port range exhausted")
	errInvalidPublicIP    = errors.New("target must be a public IP address")
)

type createMyRelayRequest struct {
	SourceClientID int64  `json:"source_client_id"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	TargetClientID int64  `json:"target_client_id"`
	TargetIP       string `json:"target_ip"`
	TargetPort     int    `json:"target_port"`
}

type updateMyRelayRequest struct {
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
}

type myRelayNodeResponse struct {
	ServerName string `json:"server_name"`
	ProxyName  string `json:"proxy_name"`
}

type myRelayResponse struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	Mode         string               `json:"mode"`
	Source       myRelayNodeResponse  `json:"source"`
	Target       *myRelayNodeResponse `json:"target,omitempty"`
	TargetIP     *string              `json:"target_ip,omitempty"`
	TargetPort   *int                 `json:"target_port,omitempty"`
	EntryAddress string               `json:"entry_address"`
	Enabled      bool                 `json:"enabled"`
	CreatedAt    time.Time            `json:"created_at"`
}

type adminUserRelayResponse struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	Username     string               `json:"username,omitempty"`
	Mode         string               `json:"mode"`
	Source       myRelayNodeResponse  `json:"source"`
	Target       *myRelayNodeResponse `json:"target,omitempty"`
	EntryAddress string               `json:"entry_address"`
	Enabled      bool                 `json:"enabled"`
	CreatedAt    time.Time            `json:"created_at"`
}

func (s *server) listMyRelaySources(w http.ResponseWriter, r *http.Request, user auth.User) {
	clients, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	type sourceResponse struct {
		ClientID         int64  `json:"client_id"`
		ServerName       string `json:"server_name"`
		ProxyName        string `json:"proxy_name"`
		Protocol         string `json:"protocol"`
		EffectiveEnabled bool   `json:"effective_enabled"`
	}
	values := make([]sourceResponse, 0, len(clients))
	now := time.Now()
	for _, client := range clients {
		if client.UserRelayPortCount > 0 && client.LifecycleAt(now).EffectiveEnabled {
			values = append(values, sourceResponse{
				ClientID: client.ID, ServerName: client.ServerName,
				ProxyName: client.ProxyName, Protocol: client.Protocol, EffectiveEnabled: true,
			})
		}
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
	source, err := s.proxies.GetAssignedClient(r.Context(), user.ID, request.SourceClientID)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if !source.LifecycleAt(time.Now()).EffectiveEnabled {
		writeError(w, http.StatusConflict, "所选节点当前不可用于创建中转")
		return
	}
	supported, err := s.serverSupportsCapability(r, source.ServerID, agentcontrol.CapabilityRelayRealm)
	if err != nil {
		writeServerError(w, err)
		return
	}
	if !supported {
		writeError(w, http.StatusConflict, "当前中转节点暂不可用")
		return
	}
	input := relaystore.CreateInput{
		ServerID: source.ServerID, OwnerUserID: &user.ID, SourceClientID: &source.ID, Name: request.Name,
		ListenAddress: "0.0.0.0", EntryHostMode: source.ProxyEntryHostMode, EntryHost: source.ProxyEntryHost,
		Network: relaystore.NetworkTCP, Enabled: true,
	}
	switch request.Mode {
	case "assigned_node":
		if request.TargetClientID == request.SourceClientID {
			writeError(w, http.StatusBadRequest, "入口节点和落地节点不能相同")
			return
		}
		target, err := s.proxies.GetAssignedClient(r.Context(), user.ID, request.TargetClientID)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		if !target.LifecycleAt(time.Now()).EffectiveEnabled {
			writeError(w, http.StatusConflict, "所选落地节点当前不可用于创建中转")
			return
		}
		input.TargetType = relaystore.TargetProxy
		input.TargetProxyID = &target.ProxyID
		input.TargetClientID = &target.ID
	case "custom":
		targetIP, err := validatePublicTargetIP(request.TargetIP)
		if err != nil {
			writeUserRelayError(w, err)
			return
		}
		if request.TargetPort < 1 || request.TargetPort > 65535 {
			writeRelayError(w, relaystore.ErrInvalidPort)
			return
		}
		input.TargetType = relaystore.TargetManual
		input.TargetHost = targetIP.String()
		input.TargetPort = request.TargetPort
	default:
		writeError(w, http.StatusBadRequest, "落地方式无效")
		return
	}
	for attempt := 0; attempt < proxystore.MaxClientRelayPorts; attempt++ {
		ports, err := s.proxies.ListAvailableClientRelayPorts(r.Context(), source.ID)
		if err != nil {
			writeInternalError(w)
			return
		}
		if len(ports) == 0 {
			writeUserRelayError(w, errUserRelayPortsFull)
			return
		}
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(ports))))
		if err != nil {
			writeInternalError(w)
			return
		}
		port := ports[int(choice.Int64())]
		input.ListenPort = port
		value, mutation, err := s.relays.Create(r.Context(), input)
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

func (s *server) updateMyRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	value, ok := s.readOwnedRelay(w, r, user)
	if !ok {
		return
	}
	if value.TargetType != relaystore.TargetManual {
		writeError(w, http.StatusBadRequest, "仅自定义落地中转可以修改目标地址")
		return
	}
	var request updateMyRelayRequest
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
	target := targetIP.String()
	updated, mutation, err := s.relays.Update(r.Context(), value.ID, relaystore.UpdateInput{
		TargetHost: &target, TargetPort: &request.TargetPort,
	})
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeJSON(w, http.StatusOK, map[string]any{"relay": toMyRelayResponse(updated)})
}

func (s *server) getMyRelayShare(w http.ResponseWriter, r *http.Request, user auth.User) {
	value, ok := s.readOwnedRelay(w, r, user)
	if !ok {
		return
	}
	if value.EntryAddress == "" {
		writeRelayError(w, relaystore.ErrEntryUnavailable)
		return
	}
	var clientID int64
	if value.TargetType == relaystore.TargetProxy && value.TargetClientID != nil {
		clientID = *value.TargetClientID
	} else if value.TargetType == relaystore.TargetManual && value.SourceClientID != nil {
		clientID = *value.SourceClientID
	} else {
		writeRelayError(w, relaystore.ErrInvalidTargetClient)
		return
	}
	if _, err := s.proxies.GetAssignedClient(r.Context(), user.ID, clientID); err != nil {
		writeProxyError(w, err)
		return
	}
	share, err := s.proxies.GetClientShareAtEndpoint(r.Context(), clientID, proxystore.ShareEndpoint{
		Address: value.EntryAddress, Port: value.ListenPort,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": map[string]any{
		"uri": share.URI, "protocol": share.Protocol, "name": value.Name,
	}})
}

func (s *server) readOwnedRelay(w http.ResponseWriter, r *http.Request, user auth.User) (relaystore.Relay, bool) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return relaystore.Relay{}, false
	}
	value, err := s.relays.Get(r.Context(), id)
	if err != nil || value.OwnerUserID == nil || *value.OwnerUserID != user.ID {
		writeRelayError(w, relaystore.ErrNotFound)
		return relaystore.Relay{}, false
	}
	return value, true
}

func (s *server) deleteMyRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	value, ok := s.readOwnedRelay(w, r, user)
	if !ok {
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
	response := make([]adminUserRelayResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toAdminUserRelayResponse(value))
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
	allowManagedPurge, err := s.allowManagedRelayPurge(r.Context(), value.ServerID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	mutation, err := s.relays.DeleteWithManagedPurge(r.Context(), value.ID, allowManagedPurge)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeNoContent(w)
}

func (s *server) deleteUserRelaysForClient(ctx context.Context, clientID int64) error {
	values, err := s.relays.ListUserOwnedByClient(ctx, clientID)
	if err != nil {
		return err
	}
	for _, value := range values {
		allowManagedPurge, err := s.allowManagedRelayPurge(ctx, value.ServerID)
		if err != nil {
			return err
		}
		mutation, err := s.relays.DeleteWithManagedPurge(ctx, value.ID, allowManagedPurge)
		if err != nil {
			return err
		}
		s.notifyRelayMutations([]relaystore.Mutation{mutation})
	}
	return nil
}

func (s *server) allowManagedRelayPurge(ctx context.Context, serverID int64) (bool, error) {
	serverValue, err := s.servers.Get(ctx, serverID)
	if err != nil {
		return false, err
	}
	return serverValue.AgentVersion == "" || agentcontrol.DeclaresCapability(agentcontrol.Metadata{
		Implementation: serverValue.AgentImplementation,
		Version:        serverValue.AgentVersion,
		APIVersion:     serverValue.AgentAPIVersion,
		Capabilities:   serverValue.AgentCapabilities,
	}, agentcontrol.CapabilityManagedRuntimePurge), nil
}

func toMyRelayResponse(value relaystore.Relay) myRelayResponse {
	response := myRelayResponse{
		ID: value.ID, Name: value.Name, Mode: relayMode(value),
		Source:       myRelayNodeResponse{ServerName: value.ServerName, ProxyName: value.SourceProxyName},
		EntryAddress: relaystore.JoinHostPort(value.EntryAddress, value.ListenPort),
		Enabled:      value.Enabled, CreatedAt: value.CreatedAt,
	}
	if value.TargetType == relaystore.TargetProxy {
		response.Target = &myRelayNodeResponse{ServerName: value.TargetServerName, ProxyName: value.TargetProxyName}
	} else if value.TargetType == relaystore.TargetManual {
		targetIP, targetPort := value.TargetHost, value.TargetPort
		response.TargetIP, response.TargetPort = &targetIP, &targetPort
	}
	return response
}

func toAdminUserRelayResponse(value relaystore.Relay) adminUserRelayResponse {
	response := adminUserRelayResponse{
		ID: value.ID, Name: value.Name, Username: value.OwnerUsername, Mode: relayMode(value),
		Source:       myRelayNodeResponse{ServerName: value.ServerName, ProxyName: value.SourceProxyName},
		EntryAddress: relaystore.JoinHostPort(value.EntryAddress, value.ListenPort),
		Enabled:      value.Enabled, CreatedAt: value.CreatedAt,
	}
	if value.TargetType == relaystore.TargetProxy {
		response.Target = &myRelayNodeResponse{ServerName: value.TargetServerName, ProxyName: value.TargetProxyName}
	}
	return response
}

func relayMode(value relaystore.Relay) string {
	if value.TargetType == relaystore.TargetProxy {
		return "assigned_node"
	}
	return "custom"
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
	case errors.Is(err, errUserRelayLimit):
		writeError(w, http.StatusConflict, "每个普通用户最多可创建 "+strconv.Itoa(maxUserRelays)+" 条中转")
	case errors.Is(err, errUserRelayPortsFull):
		writeError(w, http.StatusConflict, "该节点可用中转端口已用尽")
	case errors.Is(err, errInvalidPublicIP):
		writeError(w, http.StatusBadRequest, "落地地址必须是可公开访问的 IP，不能使用域名、内网或保留地址")
	default:
		writeInternalError(w)
	}
}
