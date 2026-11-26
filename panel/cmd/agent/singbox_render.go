package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type singboxConfig struct {
	Inbounds []singboxInbound `json:"inbounds"`
}

type singboxInbound struct {
	Type       string            `json:"type"`
	Tag        string            `json:"tag"`
	Listen     string            `json:"listen"`
	ListenPort int               `json:"listen_port"`
	UpMbps     int               `json:"up_mbps,omitempty"`
	DownMbps   int               `json:"down_mbps,omitempty"`
	Obfs       *singboxObfs      `json:"obfs,omitempty"`
	Users      []singboxUser     `json:"users"`
	TLS        singboxInboundTLS `json:"tls"`
}

type singboxObfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

type singboxUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type singboxInboundTLS struct {
	Enabled         bool     `json:"enabled"`
	Certificate     []string `json:"certificate,omitempty"`
	Key             []string `json:"key,omitempty"`
	CertificatePath string   `json:"certificate_path,omitempty"`
	KeyPath         string   `json:"key_path,omitempty"`
}

// renderManagedSingboxConfig rejects empty user lists: sing-box treats an empty
// list as an unauthenticated inbound, which is unsafe for managed proxies.
func renderManagedSingboxConfig(proxies []desiredProxy) ([]byte, error) {
	values := append([]desiredProxy(nil), proxies...)
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	config := singboxConfig{Inbounds: make([]singboxInbound, 0, len(values))}
	ids := make(map[int64]bool)
	ports := make(map[int]bool)
	for _, p := range values {
		if p.ID <= 0 || ids[p.ID] || ports[p.Port] || net.ParseIP(p.Listen) == nil || p.Port < 1 || p.Port > 65535 || p.Protocol != "hysteria2" || p.Security != "tls" || p.TLS == nil || p.Hysteria2 == nil || len(p.Clients) == 0 || p.Hysteria2.UpMbps < 0 || p.Hysteria2.DownMbps < 0 {
			return nil, errUnsupportedManagedConfig
		}
		ids[p.ID], ports[p.Port] = true, true
		inbound := singboxInbound{Type: "hysteria2", Tag: fmt.Sprintf("proxy-%d", p.ID), Listen: p.Listen, ListenPort: p.Port, UpMbps: p.Hysteria2.UpMbps, DownMbps: p.Hysteria2.DownMbps, TLS: singboxInboundTLS{Enabled: true}}
		switch desiredTLSMode(p.TLS) {
		case "acme":
			if !validACMEDomain(p.ServerName) || p.TLS.Certificate != "" || p.TLS.PrivateKey != "" {
				return nil, errUnsupportedManagedConfig
			}
			paths := managedACMECertificatePaths(managedSingboxACMECertDir, p.ServerName)
			inbound.TLS.CertificatePath, inbound.TLS.KeyPath = filepath.ToSlash(paths.Fullchain), filepath.ToSlash(paths.PrivateKey)
		case "manual":
			pair, err := tls.X509KeyPair([]byte(p.TLS.Certificate), []byte(p.TLS.PrivateKey))
			if err != nil || len(pair.Certificate) == 0 {
				return nil, errSingboxTLS
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil || leaf.VerifyHostname(p.ServerName) != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
				return nil, errSingboxTLS
			}
			inbound.TLS.Certificate = strings.Split(strings.TrimSpace(p.TLS.Certificate), "\n")
			inbound.TLS.Key = strings.Split(strings.TrimSpace(p.TLS.PrivateKey), "\n")
		default:
			return nil, errUnsupportedManagedConfig
		}
		if p.Hysteria2.ObfsPassword != "" {
			inbound.Obfs = &singboxObfs{Type: "salamander", Password: p.Hysteria2.ObfsPassword}
		}
		clients := append([]desiredClient(nil), p.Clients...)
		sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
		clientIDs := map[int64]bool{}
		passwords := map[string]bool{}
		for _, client := range clients {
			if client.ID <= 0 || clientIDs[client.ID] || client.Password == "" || passwords[client.Password] {
				return nil, errUnsupportedManagedConfig
			}
			clientIDs[client.ID], passwords[client.Password] = true, true
			inbound.Users = append(inbound.Users, singboxUser{Name: fmt.Sprintf("client-%d", client.ID), Password: client.Password})
		}
		config.Inbounds = append(config.Inbounds, inbound)
	}
	return json.MarshalIndent(config, "", "  ")
}
