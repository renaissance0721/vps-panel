package api

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type createProxyRequest struct {
	ServerID          int64  `json:"server_id"`
	Name              string `json:"name"`
	ListenPort        int    `json:"listen_port"`
	EntryHostMode     string `json:"entry_host_mode"`
	EntryHost         string `json:"entry_host"`
	Enabled           *bool  `json:"enabled"`
	Security          string `json:"security"`
	ServerName        string `json:"server_name"`
	Certificate       string `json:"certificate"`
	PrivateKey        string `json:"private_key"`
	RealityTarget     string `json:"reality_target"`
	FirstClientName   string `json:"first_client_name"`
	FirstClientUDP443 bool   `json:"first_client_udp443"`
	Protocol          string `json:"protocol"`
	Method            string `json:"method"`
}

type updateProxyRequest struct {
	Name          *string `json:"name"`
	ListenPort    *int    `json:"listen_port"`
	EntryHostMode *string `json:"entry_host_mode"`
	EntryHost     *string `json:"entry_host"`
	Enabled       *bool   `json:"enabled"`
	Security      *string `json:"security"`
	ServerName    *string `json:"server_name"`
	Certificate   *string `json:"certificate"`
	PrivateKey    *string `json:"private_key"`
	RealityTarget *string `json:"reality_target"`
	Protocol      *string `json:"protocol"`
	Method        *string `json:"method"`
}

type createClientRequest struct {
	Name         string          `json:"name"`
	ClientUDP443 bool            `json:"client_udp443"`
	Enabled      *bool           `json:"enabled"`
	ExpiresAt    json.RawMessage `json:"expires_at"`
	clientTrafficRequest
}

type updateClientRequest struct {
	Name         *string         `json:"name"`
	ClientUDP443 *bool           `json:"client_udp443"`
	Enabled      *bool           `json:"enabled"`
	ExpiresAt    json.RawMessage `json:"expires_at"`
	clientTrafficRequest
}

type clientTrafficRequest struct {
	TrafficLimit        json.RawMessage `json:"traffic_limit"`
	LimitUnit           *string         `json:"limit_unit"`
	TrafficResetMode    *string         `json:"traffic_reset_mode"`
	TrafficResetWeekday *int            `json:"traffic_reset_weekday"`
	TrafficResetDay     *int            `json:"traffic_reset_day"`
	TrafficResetTime    *string         `json:"traffic_reset_time"`
}

type proxyResponse struct {
	ID               int64                   `json:"id"`
	ServerID         int64                   `json:"server_id"`
	ServerName       string                  `json:"server_name"`
	ServerIPv4       []string                `json:"server_ipv4"`
	ServerIPv6       []string                `json:"server_ipv6"`
	ServerPublicIPv4 string                  `json:"server_public_ipv4"`
	Name             string                  `json:"name"`
	Protocol         string                  `json:"protocol"`
	ListenPort       int                     `json:"listen_port"`
	EntryHostMode    string                  `json:"entry_host_mode"`
	EntryHost        string                  `json:"entry_host"`
	EntryAddress     string                  `json:"entry_address"`
	Enabled          bool                    `json:"enabled"`
	Config           proxyConfigResponse     `json:"config"`
	Clients          []clientSummaryResponse `json:"clients,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type proxyConfigResponse struct {
	Transport                string `json:"transport,omitempty"`
	Security                 string `json:"security,omitempty"`
	ServerFlow               string `json:"server_flow,omitempty"`
	ServerName               string `json:"server_name,omitempty"`
	Fingerprint              string `json:"fingerprint,omitempty"`
	TLSCertificateConfigured bool   `json:"tls_certificate_configured"`
	RealityTarget            string `json:"reality_target,omitempty"`
	Method                   string `json:"method,omitempty"`
	Network                  string `json:"network,omitempty"`
}

type clientSummaryResponse struct {
	ID                  int64                 `json:"id"`
	ProxyID             int64                 `json:"proxy_id"`
	Name                string                `json:"name"`
	UUIDSummary         string                `json:"uuid_summary"`
	ClientUDP443        bool                  `json:"client_udp443"`
	Enabled             bool                  `json:"enabled"`
	ExpiresAt           *time.Time            `json:"expires_at"`
	Expired             bool                  `json:"expired"`
	QuotaExhausted      bool                  `json:"quota_exhausted"`
	EffectiveEnabled    bool                  `json:"effective_enabled"`
	Status              string                `json:"status"`
	TrafficLimitBytes   *int64                `json:"traffic_limit_bytes"`
	TrafficResetMode    string                `json:"traffic_reset_mode"`
	TrafficResetWeekday int                   `json:"traffic_reset_weekday"`
	TrafficResetDay     int                   `json:"traffic_reset_day"`
	TrafficResetTime    string                `json:"traffic_reset_time"`
	NextResetAt         *time.Time            `json:"next_reset_at"`
	Metrics             clientMetricsResponse `json:"metrics"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

type clientResponse struct {
	ID                  int64                 `json:"id"`
	ProxyID             int64                 `json:"proxy_id"`
	Name                string                `json:"name"`
	UUID                string                `json:"uuid,omitempty"`
	ClientUDP443        bool                  `json:"client_udp443"`
	Enabled             bool                  `json:"enabled"`
	ExpiresAt           *time.Time            `json:"expires_at"`
	Expired             bool                  `json:"expired"`
	QuotaExhausted      bool                  `json:"quota_exhausted"`
	EffectiveEnabled    bool                  `json:"effective_enabled"`
	Status              string                `json:"status"`
	TrafficLimitBytes   *int64                `json:"traffic_limit_bytes"`
	TrafficResetMode    string                `json:"traffic_reset_mode"`
	TrafficResetWeekday int                   `json:"traffic_reset_weekday"`
	TrafficResetDay     int                   `json:"traffic_reset_day"`
	TrafficResetTime    string                `json:"traffic_reset_time"`
	NextResetAt         *time.Time            `json:"next_reset_at"`
	Metrics             clientMetricsResponse `json:"metrics"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

type clientMetricsResponse struct {
	CycleUplinkBytes   int64      `json:"cycle_uplink_bytes"`
	CycleDownlinkBytes int64      `json:"cycle_downlink_bytes"`
	UsedBytes          int64      `json:"used_bytes"`
	CycleStartedAt     *time.Time `json:"cycle_started_at"`
	LastActivityAt     *time.Time `json:"last_activity_at"`
	UpdatedAt          *time.Time `json:"updated_at"`
}

type clientShareResponse struct {
	Client      clientResponse `json:"client"`
	ProxyName   string         `json:"proxy_name"`
	Address     string         `json:"address"`
	Port        int            `json:"port"`
	Protocol    string         `json:"protocol"`
	Method      string         `json:"method,omitempty"`
	Network     string         `json:"network,omitempty"`
	Security    string         `json:"security,omitempty"`
	ServerName  string         `json:"server_name,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Flow        string         `json:"flow,omitempty"`
	URI         string         `json:"uri"`
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
	if request.EntryHostMode == "" {
		request.EntryHostMode = proxystore.EntryHostAuto
	}
	value, mutation, err := s.proxies.Create(r.Context(), proxystore.CreateInput{
		ServerID: request.ServerID, Name: request.Name, ListenPort: request.ListenPort,
		EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost, Enabled: enabled, Security: request.Security,
		ServerName: request.ServerName, Certificate: request.Certificate, PrivateKey: request.PrivateKey,
		RealityTarget: request.RealityTarget, FirstClientName: request.FirstClientName,
		FirstClientUDP443: request.FirstClientUDP443, Protocol: request.Protocol, Method: request.Method,
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
		Name: request.Name, ListenPort: request.ListenPort, EntryHostMode: request.EntryHostMode, EntryHost: request.EntryHost,
		Enabled: request.Enabled, Security: request.Security, ServerName: request.ServerName,
		Certificate: request.Certificate, PrivateKey: request.PrivateKey, RealityTarget: request.RealityTarget,
		Protocol: request.Protocol, Method: request.Method,
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
		response = append(response, toClientSummaryResponse(proxystore.ClientSummary{
			ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
			UUIDSummary:  clientUUIDSummary(value.UUID),
			ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
			TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
			TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
			TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
			CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": response})
}

func clientUUIDSummary(value string) string {
	if value == "" {
		return ""
	}
	return value[:4] + "…" + value[len(value)-4:]
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
	traffic, _, err := parseClientTrafficRequest(request.clientTrafficRequest, proxystore.ClientTrafficConfig{})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	expiresAt, _, err := parseClientExpiration(request.ExpiresAt)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	value, mutation, err := s.proxies.CreateClient(r.Context(), id, proxystore.ClientCreateInput{
		Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: enabled,
		ExpiresAt: expiresAt, Traffic: traffic,
	})
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
	expiresAt, expiresAtSet, err := parseClientExpiration(request.ExpiresAt)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	var traffic *proxystore.ClientTrafficConfig
	if hasClientTrafficRequest(request.clientTrafficRequest) {
		current, err := s.proxies.GetClient(r.Context(), id)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		parsed, _, err := parseClientTrafficRequest(request.clientTrafficRequest, proxystore.ClientTrafficConfig{
			LimitBytes: current.TrafficLimitBytes, ResetMode: current.TrafficResetMode,
			Weekday: current.TrafficResetWeekday, Day: current.TrafficResetDay,
			ResetTime: current.TrafficResetTime,
		})
		if err != nil {
			writeProxyError(w, err)
			return
		}
		traffic = &parsed
	}
	value, mutation, err := s.proxies.UpdateClient(r.Context(), id, proxystore.ClientUpdateInput{
		Name: request.Name, ClientUDP443: request.ClientUDP443, Enabled: request.Enabled,
		ExpiresAtSet: expiresAtSet, ExpiresAt: expiresAt, Traffic: traffic,
	})
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.notifyProxyMutation(mutation)
	writeJSON(w, http.StatusOK, map[string]any{"client": toClientResponse(value)})
}

func (s *server) resetProxyClientTraffic(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "客户端 ID 无效")
	if !ok {
		return
	}
	value, mutation, err := s.proxies.ResetClientTrafficWithMutation(r.Context(), id)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	if mutation.Version > 0 {
		s.notifyProxyMutation(mutation)
	}
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
		Port: value.Port, Protocol: value.Protocol, Method: value.Method, Network: value.Network,
		Security: value.Security, ServerName: value.ServerName,
		Fingerprint: value.Fingerprint, Flow: value.Flow, URI: value.URI,
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
		ServerPublicIPv4: value.ServerPublicIPv4, Protocol: value.Protocol, ListenPort: value.ListenPort,
		EntryHostMode: value.EntryHostMode, EntryHost: value.EntryHost, EntryAddress: value.EntryAddress,
		Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		Config: proxyConfigResponse{
			Transport: value.Config.Transport, Security: value.Config.Security,
			ServerFlow: value.Config.ServerFlow, ServerName: value.Config.ServerName,
			Fingerprint:              value.Config.Fingerprint,
			TLSCertificateConfigured: value.Config.TLSCertificateConfigured,
			RealityTarget:            value.Config.RealityTarget, Method: value.Config.Method, Network: value.Config.Network,
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
	client := proxystore.Client{
		Enabled: value.Enabled, ExpiresAt: value.ExpiresAt, TrafficLimitBytes: value.TrafficLimitBytes,
		TrafficResetMode: value.TrafficResetMode, TrafficResetWeekday: value.TrafficResetWeekday,
		TrafficResetDay: value.TrafficResetDay, TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
	}
	lifecycle := client.LifecycleAt(time.Now())
	return clientSummaryResponse{
		ID: value.ID, ProxyID: value.ProxyID, Name: value.Name, UUIDSummary: value.UUIDSummary,
		ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
		Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
		EffectiveEnabled: lifecycle.EffectiveEnabled, Status: lifecycle.Status,
		TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
		TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime, NextResetAt: client.NextResetAt(time.Now()),
		Metrics: toClientMetricsResponse(value.Metrics), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toClientResponse(value proxystore.Client) clientResponse {
	lifecycle := value.LifecycleAt(time.Now())
	return clientResponse{
		ID: value.ID, ProxyID: value.ProxyID, Name: value.Name, UUID: value.UUID,
		ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
		Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
		EffectiveEnabled: lifecycle.EffectiveEnabled, Status: lifecycle.Status,
		TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
		TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime, NextResetAt: value.NextResetAt(time.Now()),
		Metrics: toClientMetricsResponse(value.Metrics), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toClientMetricsResponse(value *proxystore.ClientMetrics) clientMetricsResponse {
	if value == nil {
		return clientMetricsResponse{}
	}
	cycleStartedAt, updatedAt := value.CycleStartedAt, value.UpdatedAt
	return clientMetricsResponse{
		CycleUplinkBytes: value.CycleUplinkBytes, CycleDownlinkBytes: value.CycleDownlinkBytes,
		UsedBytes:      proxystore.ClientUsedBytes(value),
		CycleStartedAt: &cycleStartedAt, LastActivityAt: value.LastActivityAt, UpdatedAt: &updatedAt,
	}
}

var clientTrafficAmountPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func parseClientExpiration(raw json.RawMessage) (*time.Time, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil, true, proxystore.ErrInvalidClientExpiration
	}
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
			parsed, err = time.ParseInLocation(layout, value, shanghaiLocation)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, true, proxystore.ErrInvalidClientExpiration
	}
	result := parsed.UTC().Truncate(time.Second)
	return &result, true, nil
}

func hasClientTrafficRequest(request clientTrafficRequest) bool {
	return len(request.TrafficLimit) != 0 || request.LimitUnit != nil || request.TrafficResetMode != nil ||
		request.TrafficResetWeekday != nil || request.TrafficResetDay != nil || request.TrafficResetTime != nil
}

func parseClientTrafficRequest(
	request clientTrafficRequest,
	base proxystore.ClientTrafficConfig,
) (proxystore.ClientTrafficConfig, bool, error) {
	hasAny := hasClientTrafficRequest(request)
	if base.ResetMode == "" {
		base.ResetMode = proxystore.TrafficResetNever
		base.Weekday = 1
		base.Day = 1
		base.ResetTime = "00:00"
	}
	if len(request.TrafficLimit) != 0 {
		limit, err := parseClientTrafficLimit(request.TrafficLimit, request.LimitUnit)
		if err != nil {
			return proxystore.ClientTrafficConfig{}, hasAny, err
		}
		base.LimitBytes = limit
	} else if request.LimitUnit != nil {
		return proxystore.ClientTrafficConfig{}, hasAny, proxystore.ErrInvalidClientTrafficConfig
	}
	if request.TrafficResetMode != nil {
		base.ResetMode = strings.TrimSpace(*request.TrafficResetMode)
	}
	if request.TrafficResetWeekday != nil {
		base.Weekday = *request.TrafficResetWeekday
	}
	if request.TrafficResetDay != nil {
		base.Day = *request.TrafficResetDay
	}
	if request.TrafficResetTime != nil {
		base.ResetTime = strings.TrimSpace(*request.TrafficResetTime)
	}
	return base, hasAny, nil
}

func parseClientTrafficLimit(raw json.RawMessage, unit *string) (*int64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "null" || value == `""` || value == "0" || value == `"0"` {
		return nil, nil
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		var decoded string
		if json.Unmarshal(raw, &decoded) != nil {
			return nil, proxystore.ErrInvalidClientTrafficConfig
		}
		value = strings.TrimSpace(decoded)
	}
	if !clientTrafficAmountPattern.MatchString(value) {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || amount < 0 {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	if amount == 0 {
		return nil, nil
	}
	unitValue := "G"
	if unit != nil {
		unitValue = strings.ToUpper(strings.TrimSpace(*unit))
	}
	multiplier := float64(int64(1) << 30)
	if unitValue == "T" {
		multiplier = float64(int64(1) << 40)
	} else if unitValue != "G" {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	bytes := math.Round(amount * multiplier)
	if bytes <= 0 || bytes > float64(math.MaxInt64) {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	result := int64(bytes)
	return &result, nil
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
	case errors.Is(err, proxystore.ErrLastClient):
		writeError(w, http.StatusConflict, "代理节点必须至少保留一个客户端")
	case errors.Is(err, proxystore.ErrConnectionAddressUnavailable):
		writeError(w, http.StatusConflict, "连接地址不可用，请手动填写入口地址或等待服务器上报公网 IPv4")
	case errors.Is(err, proxystore.ErrInvalidClientTrafficConfig):
		writeError(w, http.StatusBadRequest, "客户端流量设置无效")
	case errors.Is(err, proxystore.ErrInvalidClientExpiration):
		writeError(w, http.StatusBadRequest, "客户端到期时间无效")
	default:
		writeInternalError(w)
	}
}
