package api

import (
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func (s *server) listProxies(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.proxies.List(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]proxyResponse, 0, len(values))
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		allowed, err := s.canAccessServer(r.Context(), user, value.ServerID)
		if err != nil {
			writeInternalError(w)
			return
		}
		if !allowed {
			continue
		}
		response = append(response, toProxyResponse(value))
		ids = append(ids, value.ID)
	}
	ranks, err := s.orderRanks(r.Context(), user.ID, listorder.Proxies, ids)
	if err != nil {
		writeInternalError(w)
		return
	}
	sort.SliceStable(response, func(i, j int) bool { return ranks[response[i].ID] < ranks[response[j].ID] })
	writeJSON(w, http.StatusOK, map[string]any{"proxies": response})
}

func (s *server) createProxy(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createProxyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !s.requireServerAccess(w, r, user, request.ServerID) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	if request.FirstClientName == "" {
		request.FirstClientName = "默认客户端"
	}
	if request.EntryHostMode == "" {
		request.EntryHostMode = proxystore.EntryHostAuto
	}
	input := proxystore.CreateInput{
		ServerID: request.ServerID, Name: request.Name, ListenPort: request.ListenPort,
		EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost, Enabled: enabled, Security: request.Security,
		ServerName: request.ServerName, TLSMode: request.TLSMode, Certificate: request.Certificate, PrivateKey: request.PrivateKey,
		RealityTarget: request.RealityTarget, FirstClientName: request.FirstClientName,
		FirstClientUDP443: request.FirstClientUDP443, Protocol: request.Protocol, Method: request.Method,
	}
	if capability, message := requiredCreateProxyCapability(request); capability != "" {
		supported, err := s.serverSupportsCapability(r, request.ServerID, capability)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if !supported {
			if err := proxystore.ValidateCreateInput(input); err != nil {
				writeProxyError(w, err)
				return
			}
			writeError(w, http.StatusConflict, message)
			return
		}
	}
	value, mutation, err := s.proxies.Create(r.Context(), input)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"proxy": toProxyResponse(value)})
}

func (s *server) getProxy(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	value, err := s.proxyForUser(r.Context(), user, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxy": toProxyResponse(value)})
}

func (s *server) updateProxy(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	previous, err := s.proxyForUser(r.Context(), user, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	var request updateProxyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	input := proxystore.UpdateInput{
		Name: request.Name, ListenPort: request.ListenPort, EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost,
		Enabled: request.Enabled, Security: request.Security, ServerName: request.ServerName,
		TLSMode: request.TLSMode, Certificate: request.Certificate, PrivateKey: request.PrivateKey, RealityTarget: request.RealityTarget,
		Protocol: request.Protocol, Method: request.Method,
	}
	validated, err := s.proxies.ValidateUpdate(r.Context(), id, input)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if validated.Enabled {
		capability, message := requiredProxyCapability(validated)
		supported, err := s.serverSupportsCapability(r, validated.ServerID, capability)
		if err != nil {
			writeServerError(w, err)
			return
		}
		if !supported {
			writeError(w, http.StatusConflict, message)
			return
		}
	}
	value, mutation, err := s.proxies.Update(r.Context(), id, input)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	if previous.ListenPort != value.ListenPort || previous.EntryHostMode != value.EntryHostMode || previous.EntryHost != value.EntryHost {
		mutations, err := s.relays.BumpForProxyTarget(r.Context(), value.ID, value.ServerID)
		if err != nil {
			writeInternalError(w)
			return
		}
		s.notifyRelayMutations(mutations)
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxy": toProxyResponse(value)})
}

func requiredCreateProxyCapability(request createProxyRequest) (string, string) {
	protocol := strings.ToLower(strings.TrimSpace(request.Protocol))
	if protocol == "" {
		protocol = proxystore.ProtocolVLESS
	}
	if protocol == proxystore.ProtocolShadowsocks {
		return agentcontrol.CapabilityProxyShadowsocks, "当前 Agent 不支持 Shadowsocks"
	}
	if protocol != proxystore.ProtocolVLESS {
		return "", ""
	}
	security := strings.ToLower(strings.TrimSpace(request.Security))
	if security == proxystore.SecurityReality {
		return agentcontrol.CapabilityProxyVLESSReality, "当前 Agent 不支持 VLESS + REALITY"
	}
	if security != proxystore.SecurityTLS {
		return "", ""
	}
	mode := strings.ToLower(strings.TrimSpace(request.TLSMode))
	if mode == "" {
		if strings.TrimSpace(request.Certificate) != "" || strings.TrimSpace(request.PrivateKey) != "" {
			mode = proxystore.TLSModeManual
		} else {
			mode = proxystore.TLSModeACME
		}
	}
	if mode == proxystore.TLSModeACME {
		return agentcontrol.CapabilityProxyVLESSACME, "当前 Agent 不支持 VLESS + TLS（ACME）"
	}
	if mode == proxystore.TLSModeManual {
		return agentcontrol.CapabilityProxyVLESSManual, "当前 Agent 不支持 VLESS + TLS（手动证书）"
	}
	return "", ""
}

func requiredProxyCapability(value proxystore.Proxy) (string, string) {
	if value.Protocol == proxystore.ProtocolShadowsocks {
		return agentcontrol.CapabilityProxyShadowsocks, "当前 Agent 不支持 Shadowsocks"
	}
	if value.Config.Security == proxystore.SecurityReality {
		return agentcontrol.CapabilityProxyVLESSReality, "当前 Agent 不支持 VLESS + REALITY"
	}
	if value.Config.TLSMode == proxystore.TLSModeManual {
		return agentcontrol.CapabilityProxyVLESSManual, "当前 Agent 不支持 VLESS + TLS（手动证书）"
	}
	return agentcontrol.CapabilityProxyVLESSACME, "当前 Agent 不支持 VLESS + TLS（ACME）"
}

func (s *server) deleteProxy(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	value, err := s.proxyForUser(r.Context(), user, id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	server, err := s.servers.Get(r.Context(), value.ServerID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	allowManagedPurge := server.AgentVersion == "" || agentcontrol.DeclaresCapability(agentcontrol.Metadata{
		Implementation: server.AgentImplementation,
		Version:        server.AgentVersion,
		APIVersion:     server.AgentAPIVersion,
		Capabilities:   server.AgentCapabilities,
	}, agentcontrol.CapabilityManagedRuntimePurge)
	mutation, err := s.proxies.DeleteWithManagedPurge(r.Context(), id, allowManagedPurge)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeNoContent(w)
}

func (s *server) notifyProxyMutation(mutation proxystore.Mutation) {
	if err := s.agents.NotifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
		log.Printf("notify Agent for server %d config version %d: %v", mutation.ServerID, mutation.Version, err)
	}
}

func writeProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxystore.ErrNotFound):
		writeError(w, http.StatusNotFound, "代理节点不存在")
	case errors.Is(err, proxystore.ErrClientNotFound):
		writeError(w, http.StatusNotFound, "客户端不存在")
	case errors.Is(err, proxystore.ErrServerNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在或已移除")
	case errors.Is(err, proxystore.ErrServerDecommissioning):
		writeError(w, http.StatusConflict, "服务器正在退役，不能继续修改配置")
	case errors.Is(err, proxystore.ErrManagedRuntimePurgeUnsupported):
		writeError(w, http.StatusConflict, "当前 Agent 不支持受管运行时清理，请先升级 Agent")
	case errors.Is(err, proxystore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "名称不能为空且不能超过 100 个字符")
	case errors.Is(err, proxystore.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "监听端口必须在 1–65535 之间")
	case errors.Is(err, proxystore.ErrPortConflict):
		writeError(w, http.StatusConflict, "该服务器上的监听端口已被其他代理节点使用")
	case errors.Is(err, proxystore.ErrInvalidEntryHostMode):
		writeError(w, http.StatusBadRequest, "入口地址模式仅支持自动检测或手动输入")
	case errors.Is(err, proxystore.ErrInvalidEntryHost):
		writeError(w, http.StatusBadRequest, "手动入口地址必须是有效 IPv4、IPv6 或域名，且不能包含协议、路径或端口")
	case errors.Is(err, proxystore.ErrInvalidServerName):
		writeError(w, http.StatusBadRequest, "SNI 必须是有效域名或 IP")
	case errors.Is(err, proxystore.ErrInvalidSecurity):
		writeError(w, http.StatusBadRequest, "安全层仅支持 TLS 或 REALITY")
	case errors.Is(err, proxystore.ErrInvalidTLS):
		writeError(w, http.StatusBadRequest, "TLS 证书和私钥不能为空且必须匹配")
	case errors.Is(err, proxystore.ErrInvalidTLSMode):
		writeError(w, http.StatusBadRequest, "TLS 证书来源仅支持自动 ACME 或手动证书")
	case errors.Is(err, proxystore.ErrInvalidACMEDomain):
		writeError(w, http.StatusBadRequest, "自动 ACME 的 SNI 必须是有效公网域名，不能使用 IP 或 localhost")
	case errors.Is(err, proxystore.ErrInvalidReality):
		writeError(w, http.StatusBadRequest, "REALITY SNI 或目标地址无效")
	case errors.Is(err, proxystore.ErrInvalidProtocol):
		writeError(w, http.StatusBadRequest, "协议仅支持 VLESS 或 Shadowsocks")
	case errors.Is(err, proxystore.ErrInvalidShadowsocksMethod):
		writeError(w, http.StatusBadRequest, "Shadowsocks 加密方法无效")
	case errors.Is(err, proxystore.ErrImmutableProtocol):
		writeError(w, http.StatusConflict, "代理协议创建后不能修改")
	case errors.Is(err, proxystore.ErrImmutableShadowsocksMethod):
		writeError(w, http.StatusConflict, "Shadowsocks 加密方法创建后不能修改")
	case errors.Is(err, proxystore.ErrShadowsocksClientUDP443):
		writeError(w, http.StatusBadRequest, "Shadowsocks 客户端不支持 UDP 443 流控选项")
	case errors.Is(err, proxystore.ErrInvalidShadowsocksUpdate):
		writeError(w, http.StatusBadRequest, "Shadowsocks 不支持 TLS 或 REALITY 配置")
	case errors.Is(err, proxystore.ErrConnectionAddressUnavailable):
		writeError(w, http.StatusConflict, "连接地址不可用，请手动填写入口地址或等待服务器上报公网 IPv4")
	case errors.Is(err, proxystore.ErrInvalidClientTrafficConfig):
		writeError(w, http.StatusBadRequest, "客户端流量设置无效")
	case errors.Is(err, proxystore.ErrInvalidClientExpiration):
		writeError(w, http.StatusBadRequest, "客户端到期时间无效")
	case errors.Is(err, proxystore.ErrReferencedByRelay):
		writeError(w, http.StatusConflict, "代理节点正在被中转规则使用，请先修改或删除相关中转规则")
	default:
		writeInternalError(w)
	}
}
