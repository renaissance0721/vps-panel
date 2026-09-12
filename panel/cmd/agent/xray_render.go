package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type renderedXrayConfig struct {
	Log       renderedXrayLog        `json:"log"`
	Inbounds  []renderedXrayInbound  `json:"inbounds"`
	Outbounds []renderedXrayOutbound `json:"outbounds"`
}

type renderedXrayLog struct {
	LogLevel string `json:"loglevel"`
}

type renderedXrayInbound struct {
	Tag            string                     `json:"tag"`
	Listen         string                     `json:"listen"`
	Port           int                        `json:"port"`
	Protocol       string                     `json:"protocol"`
	Settings       renderedVLESSSettings      `json:"settings"`
	StreamSettings renderedXrayStreamSettings `json:"streamSettings"`
}

type renderedVLESSSettings struct {
	Clients    []renderedVLESSClient `json:"clients"`
	Decryption string                `json:"decryption"`
}

type renderedVLESSClient struct {
	ID   string `json:"id"`
	Flow string `json:"flow"`
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
	Certificate []string `json:"certificate"`
	Key         []string `json:"key"`
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
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

func renderManagedXrayConfig(proxies []desiredProxy) ([]byte, error) {
	config := renderedXrayConfig{
		Log:       renderedXrayLog{LogLevel: "warning"},
		Inbounds:  make([]renderedXrayInbound, 0, len(proxies)),
		Outbounds: []renderedXrayOutbound{{Protocol: "freedom", Tag: "direct"}},
	}
	ports := make(map[int]struct{}, len(proxies))
	for _, proxy := range proxies {
		if err := validateDesiredProxy(proxy); err != nil {
			return nil, err
		}
		if _, exists := ports[proxy.Port]; exists {
			return nil, errUnsupportedManagedConfig
		}
		ports[proxy.Port] = struct{}{}
		inbound := renderedXrayInbound{
			Tag: "proxy-" + strconv.FormatInt(proxy.ID, 10), Listen: proxy.Listen, Port: proxy.Port,
			Protocol: "vless", Settings: renderedVLESSSettings{Decryption: "none", Clients: make([]renderedVLESSClient, 0, len(proxy.Clients))},
			StreamSettings: renderedXrayStreamSettings{Network: "tcp", Security: proxy.Security},
		}
		for _, client := range proxy.Clients {
			if !validDesiredUUID(client.UUID) {
				return nil, errUnsupportedManagedConfig
			}
			inbound.Settings.Clients = append(inbound.Settings.Clients, renderedVLESSClient{ID: client.UUID, Flow: "xtls-rprx-vision"})
		}
		if proxy.Security == "tls" {
			inbound.StreamSettings.TLSSettings = &renderedTLSSettings{
				ServerName: proxy.ServerName,
				Certificates: []renderedTLSCertificate{{
					Certificate: pemLines(proxy.TLS.Certificate), Key: pemLines(proxy.TLS.PrivateKey),
				}},
			}
		} else {
			inbound.StreamSettings.RealitySettings = &renderedRealitySettings{
				Show: false, Target: proxy.Reality.Target, Xver: 0,
				ServerNames: []string{proxy.ServerName}, PrivateKey: proxy.Reality.PrivateKey,
				ShortIDs: []string{proxy.Reality.ShortID},
			}
		}
		config.Inbounds = append(config.Inbounds, inbound)
	}
	value, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render managed Xray config: %w", err)
	}
	return append(value, '\n'), nil
}

func validateDesiredProxy(proxy desiredProxy) error {
	if proxy.ID <= 0 || proxy.Listen != "0.0.0.0" || proxy.Port < 1 || proxy.Port > 65535 ||
		proxy.Protocol != "vless" || proxy.Transport != "tcp" || proxy.ServerFlow != "xtls-rprx-vision" ||
		strings.TrimSpace(proxy.ServerName) == "" {
		return errUnsupportedManagedConfig
	}
	switch proxy.Security {
	case "tls":
		if proxy.TLS == nil || proxy.Reality != nil || strings.TrimSpace(proxy.TLS.Certificate) == "" || strings.TrimSpace(proxy.TLS.PrivateKey) == "" {
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

func validDesiredUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
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
