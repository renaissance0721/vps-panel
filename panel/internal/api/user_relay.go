package api

import (
	"context"
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
	errUserRelayLimit     = errors.New("user relay limit reached")
	errUserRelayPortsFull = errors.New("user relay port range exhausted")
	errInvalidPublicIP    = errors.New("target must be a public IP address")
)

type createMyRelayRequest struct {
	SourceClientID int64  `json:"source_client_id"`
	Name           string `json:"name"`
	TargetIP       string `json:"target_ip"`
	TargetPort     int    `json:"target_port"`
}

type myRelayResponse struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	SourceClientID *int64    `json:"source_client_id,omitempty"`
	ServerName     string    `json:"server_name"`
	ProxyName      string    `json:"proxy_name"`
	EntryAddress   string    `json:"entry_address"`
	TargetIP       string    `json:"target_ip"`
	TargetPort     int       `json:"target_port"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *server) listMyRelaySources(w http.ResponseWriter, r *http.Request, user auth.User) {
	clients, err := s.proxies.ListAssignedClients(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	type sourceResponse struct {
		ClientID   int64  `json:"client_id"`
		ServerName string `json:"server_name"`
		ProxyName  string `json:"proxy_name"`
		Protocol   string `json:"protocol"`
	}
	values := make([]sourceResponse, 0, len(clients))
	now := time.Now()
	for _, client := range clients {
		if client.LifecycleAt(now).EffectiveEnabled {
			values = append(values, sourceResponse{
				ClientID: client.ID, ServerName: client.ServerName,
				ProxyName: client.ProxyName, Protocol: client.Protocol,
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
	targetIP, err := validatePublicTargetIP(request.TargetIP)
	if err != nil {
		writeUserRelayError(w, err)
		return
	}
	if request.TargetPort < 1 || request.TargetPort > 65535 {
		writeRelayError(w, relaystore.ErrInvalidPort)
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
	for port := relaystore.UserRelayPortStart; port <= relaystore.UserRelayPortEnd; port++ {
		value, mutation, err := s.relays.Create(r.Context(), relaystore.CreateInput{
			ServerID: source.ServerID, OwnerUserID: &user.ID, SourceClientID: &source.ID, Name: request.Name,
			ListenAddress: "0.0.0.0", ListenPort: port,
			EntryHostMode: source.ProxyEntryHostMode, EntryHost: source.ProxyEntryHost,
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

func (s *server) deleteUserRelaysForSourceClient(ctx context.Context, clientID int64) error {
	values, err := s.relays.ListBySourceClient(ctx, clientID)
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
	return myRelayResponse{
		ID: value.ID, Name: value.Name, SourceClientID: value.SourceClientID,
		ServerName: value.ServerName, ProxyName: value.SourceProxyName,
		EntryAddress: relaystore.JoinHostPort(value.EntryAddress, value.ListenPort),
		TargetIP:     value.TargetHost, TargetPort: value.TargetPort,
		Enabled: value.Enabled, CreatedAt: value.CreatedAt,
	}
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
		writeError(w, http.StatusConflict, "中转端口范围已用尽")
	case errors.Is(err, errInvalidPublicIP):
		writeError(w, http.StatusBadRequest, "落地地址必须是可公开访问的 IP，不能使用域名、内网或保留地址")
	default:
		writeInternalError(w)
	}
}
