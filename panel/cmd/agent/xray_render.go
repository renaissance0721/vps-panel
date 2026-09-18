package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
)

type renderedXrayConfig struct {
	Log       renderedXrayLog        `json:"log"`
	API       renderedXrayAPI        `json:"api"`
	Policy    renderedXrayPolicy     `json:"policy"`
	Stats     renderedXrayStats      `json:"stats"`
	Inbounds  []renderedXrayInbound  `json:"inbounds"`
	Outbounds []renderedXrayOutbound `json:"outbounds"`
}

type renderedXrayLog struct {
	LogLevel string `json:"loglevel"`
}

type renderedXrayAPI struct {
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen"`
	Services []string `json:"services"`
}

type renderedXrayPolicy struct {
	Levels map[string]renderedXrayLevelPolicy `json:"levels"`
}

type renderedXrayLevelPolicy struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
}

type renderedXrayStats struct{}

type renderedXrayInbound struct {
	Tag            string                      `json:"tag"`
	Listen         string                      `json:"listen"`
	Port           int                         `json:"port"`
	Protocol       string                      `json:"protocol"`
	Settings       renderedInboundSettings     `json:"settings"`
	StreamSettings *renderedXrayStreamSettings `json:"streamSettings,omitempty"`
}

type renderedInboundSettings struct {
	Clients    []renderedInboundClient `json:"clients"`
	Decryption string                  `json:"decryption,omitempty"`
	Method     string                  `json:"method,omitempty"`
	Password   string                  `json:"password,omitempty"`
	Network    string                  `json:"network,omitempty"`
}

type renderedInboundClient struct {
	ID       string `json:"id,omitempty"`
	Flow     string `json:"flow,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
}

type renderedXrayStreamSettings struct {
	Network         string                   `json:"network"`
	Security        string                   `json:"security"`
	TLSSettings     *renderedTLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *renderedRealitySettings `json:"realitySettings,omitempty"`
}

type renderedTLSSettings struct {
	ServerName   string                   `json:"serverName"`
	Certificates []renderedTLSCertificate `json:"certificates"`
}

type renderedTLSCertificate struct {
	Certificate     []string `json:"certificate,omitempty"`
	Key             []string `json:"key,omitempty"`
	CertificateFile string   `json:"certificateFile,omitempty"`
	KeyFile         string   `json:"keyFile,omitempty"`
}

type renderedRealitySettings struct {
	Show        bool     `json:"show"`
	Target      string   `json:"target"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIDs    []string `json:"shortIds"`
}

type renderedXrayOutbound struct {
	Protocol       string                              `json:"protocol"`
	Tag            string                              `json:"tag"`
	StreamSettings *renderedXrayOutboundStreamSettings `json:"streamSettings,omitempty"`
}

type renderedXrayOutboundStreamSettings struct {
	Sockopt renderedXraySockopt `json:"sockopt"`
}

type renderedXraySockopt struct {
	DomainStrategy string `json:"domainStrategy"`
}

func renderManagedXrayConfig(proxies []desiredProxy, outboundPreference string) ([]byte, error) {
	direct := renderedXrayOutbound{Protocol: "freedom", Tag: "direct"}
	switch outboundPreference {
	case "", "auto":
	case "prefer_ipv4":
		direct.StreamSettings = &renderedXrayOutboundStreamSettings{Sockopt: renderedXraySockopt{DomainStrategy: "UseIPv4v6"}}
	case "prefer_ipv6":
		direct.StreamSettings = &renderedXrayOutboundStreamSettings{Sockopt: renderedXraySockopt{DomainStrategy: "UseIPv6v4"}}
	default:
		return nil, errUnsupportedManagedConfig
	}
	config := renderedXrayConfig{
		Log: renderedXrayLog{LogLevel: "warning"},
		API: renderedXrayAPI{Tag: "api", Listen: managedXrayStatsAPIAddress, Services: []string{"StatsService"}},
		Policy: renderedXrayPolicy{Levels: map[string]renderedXrayLevelPolicy{
			"0": {StatsUserUplink: true, StatsUserDownlink: true},
		}},
		Stats:     renderedXrayStats{},
		Inbounds:  make([]renderedXrayInbound, 0, len(proxies)),
		Outbounds: []renderedXrayOutbound{direct},
	}
	ports := make(map[int]struct{}, len(proxies))
	for _, proxy := range proxies {
		var inbound renderedXrayInbound
		var include bool
		var err error
		switch proxy.Protocol {
		case "vless":
			inbound, err = renderVLESSInbound(proxy)
			include = true
		case "shadowsocks":
			inbound, include, err = renderShadowsocksInbound(proxy)
		default:
			err = errUnsupportedManagedConfig
		}
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		if _, exists := ports[proxy.Port]; exists {
			return nil, errUnsupportedManagedConfig
		}
		ports[proxy.Port] = struct{}{}
		config.Inbounds = append(config.Inbounds, inbound)
	}
	value, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render managed Xray config: %w", err)
	}
	return append(value, '\n'), nil
}

func renderVLESSInbound(proxy desiredProxy) (renderedXrayInbound, error) {
	if err := validateDesiredVLESSProxy(proxy); err != nil {
		return renderedXrayInbound{}, err
	}
	inbound := renderedXrayInbound{
		Tag: "proxy-" + strconv.FormatInt(proxy.ID, 10), Listen: proxy.Listen, Port: proxy.Port,
		Protocol: "vless", Settings: renderedInboundSettings{Decryption: "none", Clients: make([]renderedInboundClient, 0, len(proxy.Clients))},
		StreamSettings: &renderedXrayStreamSettings{Network: "tcp", Security: proxy.Security},
	}
	for _, client := range proxy.Clients {
		if !validDesiredUUID(client.UUID) || client.Password != "" || !validClientStatsIdentifier(client.ID, client.StatsID) {
			return renderedXrayInbound{}, errUnsupportedManagedConfig
		}
		inbound.Settings.Clients = append(inbound.Settings.Clients, renderedInboundClient{
			ID: client.UUID, Flow: "xtls-rprx-vision", Email: client.StatsID,
		})
	}
	if proxy.Security == "tls" {
		certificate := renderedTLSCertificate{}
		if desiredTLSMode(proxy.TLS) == "acme" {
			certificate.CertificateFile = path.Join(managedACMECertDir, proxy.ServerName, "fullchain.pem")
			certificate.KeyFile = path.Join(managedACMECertDir, proxy.ServerName, "private.key")
		} else {
			certificate.Certificate, certificate.Key = pemLines(proxy.TLS.Certificate), pemLines(proxy.TLS.PrivateKey)
		}
		inbound.StreamSettings.TLSSettings = &renderedTLSSettings{
			ServerName:   proxy.ServerName,
			Certificates: []renderedTLSCertificate{certificate},
		}
	} else {
		inbound.StreamSettings.Network = "raw"
		inbound.StreamSettings.RealitySettings = &renderedRealitySettings{
			Show: false, Target: proxy.Reality.Target, Xver: 0,
			ServerNames: []string{proxy.ServerName}, PrivateKey: proxy.Reality.PrivateKey,
			ShortIDs: []string{proxy.Reality.ShortID},
		}
	}
	return inbound, nil
}

func renderShadowsocksInbound(proxy desiredProxy) (renderedXrayInbound, bool, error) {
	if proxy.ID <= 0 || proxy.Listen != "0.0.0.0" || proxy.Port < 1 || proxy.Port > 65535 ||
		proxy.Protocol != "shadowsocks" || proxy.Shadowsocks == nil || proxy.Transport != "" ||
		proxy.Security != "" || proxy.ServerFlow != "" || proxy.ServerName != "" || proxy.TLS != nil || proxy.Reality != nil ||
		proxy.Shadowsocks.Network != "tcp,udp" || !validShadowsocksDesiredKey(proxy.Shadowsocks.Password, proxy.Shadowsocks.Method) {
		return renderedXrayInbound{}, false, errUnsupportedManagedConfig
	}
	if len(proxy.Clients) == 0 {
		return renderedXrayInbound{}, false, nil
	}
	clients := make([]renderedInboundClient, 0, len(proxy.Clients))
	for _, client := range proxy.Clients {
		if client.UUID != "" || !validShadowsocksDesiredKey(client.Password, proxy.Shadowsocks.Method) ||
			!validClientStatsIdentifier(client.ID, client.StatsID) {
			return renderedXrayInbound{}, false, errUnsupportedManagedConfig
		}
		clients = append(clients, renderedInboundClient{
			Password: client.Password, Email: client.StatsID,
		})
	}
	return renderedXrayInbound{
		Tag: "proxy-" + strconv.FormatInt(proxy.ID, 10), Listen: proxy.Listen, Port: proxy.Port,
		Protocol: "shadowsocks", Settings: renderedInboundSettings{
			Method: proxy.Shadowsocks.Method, Password: proxy.Shadowsocks.Password,
			Network: proxy.Shadowsocks.Network, Clients: clients,
		},
	}, true, nil
}

func validateDesiredVLESSProxy(proxy desiredProxy) error {
	if proxy.ID <= 0 || proxy.Listen != "0.0.0.0" || proxy.Port < 1 || proxy.Port > 65535 ||
		proxy.Protocol != "vless" || proxy.Transport != "tcp" || proxy.ServerFlow != "xtls-rprx-vision" ||
		strings.TrimSpace(proxy.ServerName) == "" || proxy.Shadowsocks != nil {
		return errUnsupportedManagedConfig
	}
	switch proxy.Security {
	case "tls":
		if proxy.TLS == nil || proxy.Reality != nil {
			return errUnsupportedManagedConfig
		}
		switch desiredTLSMode(proxy.TLS) {
		case "acme":
			if !validACMEDomain(proxy.ServerName) || proxy.TLS.Certificate != "" || proxy.TLS.PrivateKey != "" {
				return errUnsupportedManagedConfig
			}
		case "manual":
			if strings.TrimSpace(proxy.TLS.Certificate) == "" || strings.TrimSpace(proxy.TLS.PrivateKey) == "" {
				return errUnsupportedManagedConfig
			}
		default:
			return errUnsupportedManagedConfig
		}
	case "reality":
		if proxy.Reality == nil || proxy.TLS != nil || !validRealityTarget(proxy.Reality.Target) ||
			strings.TrimSpace(proxy.Reality.PrivateKey) == "" || !validShortID(proxy.Reality.ShortID) {
			return errUnsupportedManagedConfig
		}
	default:
		return errUnsupportedManagedConfig
	}
	return nil
}

func desiredTLSMode(value *desiredTLS) string {
	if value != nil && value.Mode == "" && value.Certificate != "" && value.PrivateKey != "" {
		return "manual"
	}
	if value == nil {
		return ""
	}
	return value.Mode
}

func validShadowsocksDesiredKey(value, method string) bool {
	length := 0
	switch method {
	case "2022-blake3-aes-128-gcm":
		length = 16
	case "2022-blake3-aes-256-gcm":
		length = 32
	default:
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == length
}

func validDesiredUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func validClientStatsIdentifier(clientID int64, value string) bool {
	return clientID > 0 && value == "vp-client-"+strconv.FormatInt(clientID, 10)
}

func validRealityTarget(value string) bool {
	host, portValue, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	port, err := strconv.Atoi(portValue)
	return err == nil && port >= 1 && port <= 65535
}

func validShortID(value string) bool {
	if len(value) == 0 || len(value) > 16 || len(value)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func pemLines(value string) []string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n")
	return strings.Split(value, "\n")
}
