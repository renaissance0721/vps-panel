package api

import (
	"time"

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
	TLSMode           string `json:"tls_mode"`
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
	TLSMode       *string `json:"tls_mode"`
	ServerName    *string `json:"server_name"`
	Certificate   *string `json:"certificate"`
	PrivateKey    *string `json:"private_key"`
	RealityTarget *string `json:"reality_target"`
	Protocol      *string `json:"protocol"`
	Method        *string `json:"method"`
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
	TLSMode                  string `json:"tls_mode,omitempty"`
	ServerFlow               string `json:"server_flow,omitempty"`
	ServerName               string `json:"server_name,omitempty"`
	Fingerprint              string `json:"fingerprint,omitempty"`
	TLSCertificateConfigured bool   `json:"tls_certificate_configured"`
	RealityTarget            string `json:"reality_target,omitempty"`
	Method                   string `json:"method,omitempty"`
	Network                  string `json:"network,omitempty"`
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
			TLSMode:                  value.Config.TLSMode,
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
