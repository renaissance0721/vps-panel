package api

import (
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type createRelayRequest struct {
	ServerID       int64  `json:"server_id"`
	Name           string `json:"name"`
	ListenAddress  string `json:"listen_address"`
	ListenPort     int    `json:"listen_port"`
	EntryHostMode  string `json:"entry_host_mode"`
	EntryHost      string `json:"entry_host"`
	TargetType     string `json:"target_type"`
	TargetProxyID  *int64 `json:"target_proxy_id"`
	TargetClientID *int64 `json:"target_client_id"`
	TargetHost     string `json:"target_host"`
	TargetPort     int    `json:"target_port"`
	Network        string `json:"network"`
	Enabled        *bool  `json:"enabled"`
}

type updateRelayRequest struct {
	Name           *string `json:"name"`
	ListenAddress  *string `json:"listen_address"`
	ListenPort     *int    `json:"listen_port"`
	EntryHostMode  *string `json:"entry_host_mode"`
	EntryHost      *string `json:"entry_host"`
	TargetType     *string `json:"target_type"`
	TargetProxyID  *int64  `json:"target_proxy_id"`
	TargetClientID *int64  `json:"target_client_id"`
	TargetHost     *string `json:"target_host"`
	TargetPort     *int    `json:"target_port"`
	Network        *string `json:"network"`
	Enabled        *bool   `json:"enabled"`
}

type relayResponse struct {
	ID                 int64     `json:"id"`
	ServerID           int64     `json:"server_id"`
	ServerName         string    `json:"server_name"`
	ServerPublicIPv4   string    `json:"server_public_ipv4"`
	Name               string    `json:"name"`
	ListenAddress      string    `json:"listen_address"`
	ListenPort         int       `json:"listen_port"`
	EntryHostMode      string    `json:"entry_host_mode"`
	EntryHost          string    `json:"entry_host"`
	EntryAddress       string    `json:"entry_address"`
	TargetType         string    `json:"target_type"`
	TargetProxyID      *int64    `json:"target_proxy_id"`
	TargetClientID     *int64    `json:"target_client_id"`
	TargetProxyName    string    `json:"target_proxy_name"`
	TargetClientName   string    `json:"target_client_name"`
	TargetHost         string    `json:"target_host"`
	TargetPort         int       `json:"target_port"`
	TargetAddressReady bool      `json:"target_address_ready"`
	Network            string    `json:"network"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type relayClientShareResponse struct {
	Client            clientResponse `json:"client"`
	Protocol          string         `json:"protocol"`
	URI               string         `json:"uri"`
	NetworkCompatible bool           `json:"network_compatible"`
	NetworkNotice     string         `json:"network_notice,omitempty"`
}

func (s *server) listRelays(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.relays.List(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]relayResponse, 0, len(values))
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		if _, err := s.relayForUser(r.Context(), user, value.ID); err != nil {
			if errors.Is(err, relaystore.ErrNotFound) {
				continue
			}
			writeInternalError(w)
			return
		}
		response = append(response, toRelayResponse(value))
		ids = append(ids, value.ID)
	}
	ranks, err := s.orderRanks(r.Context(), user.ID, listorder.Relays, ids)
	if err != nil {
		writeInternalError(w)
		return
	}
	sort.SliceStable(response, func(i, j int) bool { return ranks[response[i].ID] < ranks[response[j].ID] })
	writeJSON(w, http.StatusOK, map[string]any{"relays": response})
}

func (s *server) createRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createRelayRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !s.requireServerAccess(w, r, user, request.ServerID) {
		return
	}
	if request.TargetType == relaystore.TargetProxy && request.TargetProxyID != nil {
		if _, err := s.proxyForUser(r.Context(), user, *request.TargetProxyID); err != nil {
			writeProxyError(w, err)
			return
		}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, mutation, err := s.relays.Create(r.Context(), relaystore.CreateInput{
		ServerID: request.ServerID, Name: request.Name, ListenAddress: request.ListenAddress,
		ListenPort: request.ListenPort, EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost,
		TargetType:    request.TargetType,
		TargetProxyID: request.TargetProxyID, TargetClientID: request.TargetClientID, TargetHost: request.TargetHost,
		TargetPort: request.TargetPort, Network: request.Network, Enabled: enabled,
	})
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeJSON(w, http.StatusCreated, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) getRelayClients(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	value, err := s.relayForUser(r.Context(), user, id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	response := make([]relayClientShareResponse, 0)
	if value.TargetType == relaystore.TargetManual {
		writeJSON(w, http.StatusOK, map[string]any{"clients": response})
		return
	}
	if value.EntryAddress == "" {
		writeRelayError(w, relaystore.ErrEntryUnavailable)
		return
	}
	if !value.TargetAddressReady || value.TargetProxyID == nil {
		writeRelayError(w, relaystore.ErrTargetUnavailable)
		return
	}
	if value.TargetClientID == nil {
		writeJSON(w, http.StatusOK, map[string]any{"clients": response})
		return
	}
	share, err := s.proxies.GetClientShareAtEndpoint(r.Context(), *value.TargetClientID, proxystore.ShareEndpoint{
		Address: value.EntryAddress,
		Port:    value.ListenPort,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if share.Client.ProxyID != *value.TargetProxyID {
		writeRelayError(w, relaystore.ErrInvalidTargetClient)
		return
	}
	compatible, notice := relayNetworkCompatibility(value.Network, share.Protocol)
	response = append(response, relayClientShareResponse{
		Client: toClientResponse(share.Client), Protocol: share.Protocol, URI: share.URI,
		NetworkCompatible: compatible, NetworkNotice: notice,
	})
	writeJSON(w, http.StatusOK, map[string]any{"clients": response})
}

func relayNetworkCompatibility(network, protocol string) (bool, string) {
	if protocol == proxystore.ProtocolVLESS && network == relaystore.NetworkUDP {
		return false, "当前中转 Network 与该 Proxy 不兼容"
	}
	if protocol == proxystore.ProtocolShadowsocks && network == relaystore.NetworkTCP {
		return true, "此中转仅转发 TCP，UDP 不可用"
	}
	return true, ""
}

func (s *server) getRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	value, err := s.relayForUser(r.Context(), user, id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) updateRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	current, err := s.relayForUser(r.Context(), user, id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	var request updateRelayRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	targetType := current.TargetType
	if request.TargetType != nil {
		targetType = *request.TargetType
	}
	targetProxyID := current.TargetProxyID
	if request.TargetProxyID != nil {
		targetProxyID = request.TargetProxyID
	}
	if targetType == relaystore.TargetProxy && targetProxyID != nil {
		if _, err := s.proxyForUser(r.Context(), user, *targetProxyID); err != nil {
			writeProxyError(w, err)
			return
		}
	}
	value, mutation, err := s.relays.Update(r.Context(), id, relaystore.UpdateInput{
		Name: request.Name, ListenAddress: request.ListenAddress, ListenPort: request.ListenPort,
		EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost,
		TargetType: request.TargetType, TargetProxyID: request.TargetProxyID, TargetClientID: request.TargetClientID,
		TargetHost: request.TargetHost, TargetPort: request.TargetPort,
		Network: request.Network, Enabled: request.Enabled,
	})
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeJSON(w, http.StatusOK, map[string]any{"relay": toRelayResponse(value)})
}

func (s *server) deleteRelay(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "中转 ID 无效")
	if !ok {
		return
	}
	if _, err := s.relayForUser(r.Context(), user, id); err != nil {
		writeRelayError(w, err)
		return
	}
	mutation, err := s.relays.Delete(r.Context(), id)
	if err != nil {
		writeRelayError(w, err)
		return
	}
	s.notifyRelayMutations([]relaystore.Mutation{mutation})
	writeNoContent(w)
}

func (s *server) notifyRelayMutations(mutations []relaystore.Mutation) {
	for _, mutation := range mutations {
		if err := s.agents.NotifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
			log.Printf("notify Agent for server %d config version %d: %v", mutation.ServerID, mutation.Version, err)
		}
	}
}

func toRelayResponse(value relaystore.Relay) relayResponse {
	return relayResponse{
		ID: value.ID, ServerID: value.ServerID, ServerName: value.ServerName,
		ServerPublicIPv4: value.ServerPublicIPv4, Name: value.Name,
		ListenAddress: value.ListenAddress, ListenPort: value.ListenPort,
		EntryHostMode: value.EntryHostMode, EntryHost: value.EntryHost, EntryAddress: value.EntryAddress,
		TargetType: value.TargetType, TargetProxyID: value.TargetProxyID, TargetClientID: value.TargetClientID,
		TargetProxyName: value.TargetProxyName, TargetClientName: value.TargetClientName, TargetHost: value.TargetHost,
		TargetPort: value.TargetPort, TargetAddressReady: value.TargetAddressReady,
		Network: value.Network, Enabled: value.Enabled,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func writeRelayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, relaystore.ErrNotFound):
		writeError(w, http.StatusNotFound, "中转规则不存在")
	case errors.Is(err, relaystore.ErrServerNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在或已移除")
	case errors.Is(err, relaystore.ErrProxyNotFound):
		writeError(w, http.StatusNotFound, "目标代理节点不存在或已移除")
	case errors.Is(err, relaystore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "名称不能为空且不能超过 100 个字符")
	case errors.Is(err, relaystore.ErrInvalidListenIP):
		writeError(w, http.StatusBadRequest, "监听地址必须是有效 IP")
	case errors.Is(err, relaystore.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "端口必须在 1–65535 之间")
	case errors.Is(err, relaystore.ErrInvalidEntryHostMode):
		writeError(w, http.StatusBadRequest, "入口地址模式仅支持自动检测或手动输入")
	case errors.Is(err, relaystore.ErrInvalidEntryHost):
		writeError(w, http.StatusBadRequest, "手动入口地址必须是有效 IPv4、IPv6 或域名，且不能包含协议、路径或端口")
	case errors.Is(err, relaystore.ErrEntryUnavailable):
		writeError(w, http.StatusConflict, "中转入口地址不可用，请填写手动入口地址或等待源服务器上报公网 IPv4")
	case errors.Is(err, relaystore.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, "目标必须是有效代理节点或 Host/IP 与端口")
	case errors.Is(err, relaystore.ErrInvalidTargetClient):
		writeError(w, http.StatusBadRequest, "目标客户端必须属于所选目标 Proxy")
	case errors.Is(err, relaystore.ErrInvalidNetwork):
		writeError(w, http.StatusBadRequest, "Network 仅支持 TCP、UDP 或 TCP + UDP")
	case errors.Is(err, relaystore.ErrPortConflict):
		writeError(w, http.StatusConflict, "该服务器上的监听端口与现有代理节点或中转规则冲突")
	case errors.Is(err, relaystore.ErrTargetUnavailable):
		writeError(w, http.StatusConflict, "目标代理节点入口地址不可用，请填写手动入口地址或等待目标服务器上报公网 IPv4")
	default:
		writeInternalError(w)
	}
}
