package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type createProxyRequest struct {
	ServerID          int64  `json:"server_id"`
	Name              string `json:"name"`
	ListenPort        int    `json:"listen_port"`
	PublicHost        string `json:"public_host"`
	Enabled           *bool  `json:"enabled"`
	Security          string `json:"security"`
	ServerName        string `json:"server_name"`
	Certificate       string `json:"certificate"`
	PrivateKey        string `json:"private_key"`
	RealityTarget     string `json:"reality_target"`
	FirstClientName   string `json:"first_client_name"`
	FirstClientUDP443 bool   `json:"first_client_udp443"`
}

type updateProxyRequest struct {
	Name          *string `json:"name"`
	ListenPort    *int    `json:"listen_port"`
	PublicHost    *string `json:"public_host"`
	Enabled       *bool   `json:"enabled"`
	Security      *string `json:"security"`
	ServerName    *string `json:"server_name"`
	Certificate   *string `json:"certificate"`
	PrivateKey    *string `json:"private_key"`
	RealityTarget *string `json:"reality_target"`
}

type createClientRequest struct {
	Name         string `json:"name"`
	ClientUDP443 bool   `json:"client_udp443"`
	Enabled      *bool  `json:"enabled"`
}

type updateClientRequest struct {
	Name         *string `json:"name"`
	ClientUDP443 *bool   `json:"client_udp443"`
	Enabled      *bool   `json:"enabled"`
}

type proxyResponse struct {
	ID         int64                   `json:"id"`
	ServerID   int64                   `json:"server_id"`
	ServerName string                  `json:"server_name"`
	ServerIPv4 []string                `json:"server_ipv4"`
	ServerIPv6 []string                `json:"server_ipv6"`
	Name       string                  `json:"name"`
	Protocol   string                  `json:"protocol"`
	ListenPort int                     `json:"listen_port"`
	PublicHost string                  `json:"public_host"`
	Enabled    bool                    `json:"enabled"`
	Config     proxyConfigResponse     `json:"config"`
	Clients    []clientSummaryResponse `json:"clients,omitempty"`
	CreatedAt  time.Time               `json:"created_at"`
	UpdatedAt  time.Time               `json:"updated_at"`
}

type proxyConfigResponse struct {
	Transport                string `json:"transport"`
	Security                 string `json:"security"`
	ServerFlow               string `json:"server_flow"`
	ServerName               string `json:"server_name"`
	Fingerprint              string `json:"fingerprint"`
	TLSCertificateConfigured bool   `json:"tls_certificate_configured"`
	RealityTarget            string `json:"reality_target,omitempty"`
	RealityPublicKey         string `json:"reality_public_key,omitempty"`
	RealityShortID           string `json:"reality_short_id,omitempty"`
}

type clientSummaryResponse struct {
	ID           int64     `json:"id"`
	ProxyID      int64     `json:"proxy_id"`
	Name         string    `json:"name"`
	UUIDSummary  string    `json:"uuid_summary"`
	ClientUDP443 bool      `json:"client_udp443"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type clientResponse struct {
	ID           int64     `json:"id"`
	ProxyID      int64     `json:"proxy_id"`
	Name         string    `json:"name"`
	UUID         string    `json:"uuid"`
	ClientUDP443 bool      `json:"client_udp443"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type clientShareResponse struct {
	Client           clientResponse `json:"client"`
	ProxyName        string         `json:"proxy_name"`
	Address          string         `json:"address"`
	Port             int            `json:"port"`
	Security         string         `json:"security"`
	ServerName       string         `json:"server_name"`
	Fingerprint      string         `json:"fingerprint"`
	Flow             string         `json:"flow"`
	RealityPublicKey string         `json:"reality_public_key,omitempty"`
	RealityShortID   string         `json:"reality_short_id,omitempty"`
	URI              string         `json:"uri"`
}

func (s *server) listProxies(w http.ResponseWriter, r *http.Request, _ auth.User) {
	values, err := s.proxies.List(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]proxyResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toProxyResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxies": response})
}

func (s *server) createProxy(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var request createProxyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	if request.FirstClientName == "" {
		request.FirstClientName = "默认客户端"
	}
	value, mutation, err := s.proxies.Create(r.Context(), proxystore.CreateInput{
		ServerID: request.ServerID, Name: request.Name, ListenPort: request.ListenPort,
		PublicHost: request.PublicHost, Enabled: enabled, Security: request.Security,
		ServerName: request.ServerName, Certificate: request.Certificate, PrivateKey: request.PrivateKey,
		RealityTarget: request.RealityTarget, FirstClientName: request.FirstClientName,
		FirstClientUDP443: request.FirstClientUDP443,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"proxy": toProxyResponse(value)})
}

func (s *server) getProxy(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	value, err := s.proxies.Get(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxy": toProxyResponse(value)})
}

func (s *server) updateProxy(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	var request updateProxyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutation, err := s.proxies.Update(r.Context(), id, proxystore.UpdateInput{
		Name: request.Name, ListenPort: request.ListenPort, PublicHost: request.PublicHost,
		Enabled: request.Enabled, Security: request.Security, ServerName: request.ServerName,
		Certificate: request.Certificate, PrivateKey: request.PrivateKey, RealityTarget: request.RealityTarget,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusOK, map[string]any{"proxy": toProxyResponse(value)})
}

func (s *server) deleteProxy(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	mutation, err := s.proxies.Delete(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeNoContent(w)
}

func (s *server) listProxyClients(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	values, err := s.proxies.ListClients(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	response := make([]clientSummaryResponse, 0, len(values))
	for _, value := range values {
		response = append(response, clientSummaryResponse{
			ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
			UUIDSummary:  value.UUID[:4] + "…" + value.UUID[len(value.UUID)-4:],
			ClientUDP443: value.ClientUDP443, Enabled: value.Enabled,
			CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": response})
}

func (s *server) createProxyClient(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "代理节点 ID 无效")
	if !ok {
		return
	}
	var request createClientRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	value, mutation, err := s.proxies.CreateClient(r.Context(), id, proxystore.ClientCreateInput{Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: enabled})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusCreated, map[string]any{"client": toClientResponse(value)})
}

func (s *server) getProxyClient(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	value, err := s.proxies.GetClient(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) updateProxyClient(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	var request updateClientRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, mutation, err := s.proxies.UpdateClient(r.Context(), id, proxystore.ClientUpdateInput{Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: request.Enabled})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) deleteProxyClient(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	mutation, err := s.proxies.DeleteClient(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeNoContent(w)
}

func (s *server) getProxyClientShare(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	value, err := s.proxies.GetClientShare(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share": clientShareResponse{
		Client: toClientResponse(value.Client), ProxyName: value.ProxyName, Address: value.Address,
		Port: value.Port, Security: value.Security, ServerName: value.ServerName,
		Fingerprint: value.Fingerprint, Flow: value.Flow, RealityPublicKey: value.RealityPublicKey,
		RealityShortID: value.RealityShortID, URI: value.URI,
	}})
}

func (s *server) notifyProxyMutation(mutation proxystore.Mutation) {
	if err := s.notifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
		log.Printf("notify Agent for server %d config version %d: %v", mutation.ServerID, mutation.Version, err)
	}
}

func toProxyResponse(value proxystore.Proxy) proxyResponse {
	response := proxyResponse{
		ID: value.ID, ServerID: value.ServerID, ServerName: value.ServerName,
		ServerIPv4: value.ServerIPv4, ServerIPv6: value.ServerIPv6, Name: value.Name,
		Protocol: value.Protocol, ListenPort: value.ListenPort, PublicHost: value.PublicHost,
		Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		Config: proxyConfigResponse{
			Transport: value.Config.Transport, Security: value.Config.Security,
			ServerFlow: value.Config.ServerFlow, ServerName: value.Config.ServerName,
			Fingerprint:              value.Config.Fingerprint,
			TLSCertificateConfigured: value.Config.TLSCertificateConfigured,
			RealityTarget:            value.Config.RealityTarget, RealityPublicKey: value.Config.RealityPublicKey,
			RealityShortID: value.Config.RealityShortID,
		},
	}
	if value.Clients != nil {
		response.Clients = make([]clientSummaryResponse, 0, len(value.Clients))
		for _, client := range value.Clients {
			response.Clients = append(response.Clients, toClientSummaryResponse(client))
		}
	}
	return response
}

func toClientSummaryResponse(value proxystore.ClientSummary) clientSummaryResponse {
	return clientSummaryResponse{ID: value.ID, ProxyID: value.ProxyID, Name: value.Name, UUIDSummary: value.UUIDSummary, ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func toClientResponse(value proxystore.Client) clientResponse {
	return clientResponse{ID: value.ID, ProxyID: value.ProxyID, Name: value.Name, UUID: value.UUID, ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func writeProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxystore.ErrNotFound):
		writeError(w, http.StatusNotFound, "代理节点不存在")
	case errors.Is(err, proxystore.ErrClientNotFound):
		writeError(w, http.StatusNotFound, "客户端不存在")
	case errors.Is(err, proxystore.ErrServerNotFound):
		writeError(w, http.StatusNotFound, "服务器不存在或已移除")
	case errors.Is(err, proxystore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "名称不能为空且不能超过 100 个字符")
	case errors.Is(err, proxystore.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "监听端口必须在 1–65535 之间")
	case errors.Is(err, proxystore.ErrPortConflict):
		writeError(w, http.StatusConflict, "该服务器上的监听端口已被其他代理节点使用")
	case errors.Is(err, proxystore.ErrInvalidPublicHost):
		writeError(w, http.StatusBadRequest, "节点域名必须是有效域名或 IP，且不能包含协议、路径或端口")
	case errors.Is(err, proxystore.ErrInvalidServerName):
		writeError(w, http.StatusBadRequest, "SNI 必须是有效域名或 IP")
	case errors.Is(err, proxystore.ErrInvalidSecurity):
		writeError(w, http.StatusBadRequest, "安全层仅支持 TLS 或 REALITY")
	case errors.Is(err, proxystore.ErrInvalidTLS):
		writeError(w, http.StatusBadRequest, "TLS 证书和私钥不能为空且必须匹配")
	case errors.Is(err, proxystore.ErrInvalidReality):
		writeError(w, http.StatusBadRequest, "REALITY SNI 或目标地址无效")
	case errors.Is(err, proxystore.ErrLastClient):
		writeError(w, http.StatusConflict, "代理节点必须至少保留一个客户端")
	case errors.Is(err, proxystore.ErrConnectionAddressUnavailable):
		writeError(w, http.StatusConflict, "连接地址不可用，请先填写节点域名或等待服务器上报公网 IP")
	default:
		writeInternalError(w)
	}
}
