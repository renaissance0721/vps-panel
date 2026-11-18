package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

const maxConnectorProxyIPs = 32

type connectorProxyConfig struct {
	TrustedProxyIPs []netip.Addr `json:"trusted_proxy_ips"`
	Source          string       `json:"source"`
}

func loadConnectorProxyConfig(db *sql.DB, legacy string) (connectorProxyConfig, error) {
	var saved string
	err := db.QueryRow(`SELECT trusted_proxy_ips_json FROM connector_proxy_settings WHERE id=1`).Scan(&saved)
	if errors.Is(err, sql.ErrNoRows) {
		ips, err := ParseConnectorTrustedProxyIPs(legacy)
		if err != nil {
			return connectorProxyConfig{}, fmt.Errorf("invalid PANEL_CONNECTOR_TRUSTED_PROXY_IPS: %w", err)
		}
		source := "default"
		if len(ips) > 0 {
			source = "environment"
		}
		return connectorProxyConfig{TrustedProxyIPs: ips, Source: source}, nil
	}
	if err != nil {
		return connectorProxyConfig{}, err
	}
	var raw []string
	if json.Unmarshal([]byte(saved), &raw) != nil || raw == nil {
		return connectorProxyConfig{}, errors.New("invalid saved connector proxy list")
	}
	ips, err := normalizeConnectorProxyIPs(raw)
	if err != nil {
		return connectorProxyConfig{}, fmt.Errorf("invalid saved connector proxy list: %w", err)
	}
	return connectorProxyConfig{TrustedProxyIPs: ips, Source: "database"}, nil
}

func ParseConnectorTrustedProxyIPs(value string) ([]netip.Addr, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return normalizeConnectorProxyIPs(strings.Split(value, ","))
}

func normalizeConnectorProxyIPs(values []string) ([]netip.Addr, error) {
	ips := make([]netip.Addr, 0, len(values))
	seen := make(map[netip.Addr]bool, len(values))
	for index, value := range values {
		ip, err := netip.ParseAddr(strings.TrimSpace(value))
		ip = ip.Unmap()
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return nil, fmt.Errorf("entry %d must be a single unicast IP address", index+1)
		}
		if !seen[ip] {
			ips = append(ips, ip)
			seen[ip] = true
		}
	}
	return ips, nil
}

func (s *server) currentConnectorProxyConfig() connectorProxyConfig {
	s.connectorProxyMu.RLock()
	defer s.connectorProxyMu.RUnlock()
	return s.connectorProxy
}

func writeConnectorProxyConfig(w http.ResponseWriter, config connectorProxyConfig) {
	if config.TrustedProxyIPs == nil {
		config.TrustedProxyIPs = []netip.Addr{}
	}
	writeJSON(w, http.StatusOK, struct {
		connectorProxyConfig
		MaxTrustedProxyIPs int `json:"max_trusted_proxy_ips"`
	}{config, maxConnectorProxyIPs})
}

func (s *server) getConnectorProxySettings(w http.ResponseWriter, _ *http.Request, _ auth.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeConnectorProxyConfig(w, s.currentConnectorProxyConfig())
}

func (s *server) updateConnectorProxySettings(w http.ResponseWriter, r *http.Request, user auth.User) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var input struct {
		TrustedProxyIPs []string `json:"trusted_proxy_ips"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.TrustedProxyIPs == nil {
		writeError(w, http.StatusBadRequest, "请提供可信代理地址列表")
		return
	}
	ips, err := normalizeConnectorProxyIPs(input.TrustedProxyIPs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "每项须为单个有效的 IPv4 或 IPv6 地址")
		return
	}
	if len(ips) > maxConnectorProxyIPs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("最多可保存 %d 个可信代理地址", maxConnectorProxyIPs))
		return
	}
	encoded, err := json.Marshal(ips)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	config := connectorProxyConfig{TrustedProxyIPs: ips, Source: "database"}
	// Serialize writes so the published snapshot always follows SQLite's order.
	s.connectorProxyMu.Lock()
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO connector_proxy_settings (id, trusted_proxy_ips_json) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET trusted_proxy_ips_json=excluded.trusted_proxy_ips_json`, string(encoded))
	if err == nil {
		s.connectorProxy = config
	}
	s.connectorProxyMu.Unlock()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	s.recordAudit(r, user, "connector_proxy.update", "connector_proxy_settings", 1, fmt.Sprintf("保存连接器可信代理配置（%d 个地址）", len(ips)))
	writeConnectorProxyConfig(w, config)
}

type connectorProxyDiagnostics struct {
	PeerIP          string `json:"peer_ip"`
	PeerLoopback    bool   `json:"peer_loopback"`
	PeerTrusted     bool   `json:"peer_trusted"`
	BackendTLS      bool   `json:"backend_tls"`
	ForwardedStatus string `json:"forwarded_status"`
	HTTPSAccepted   bool   `json:"https_accepted"`
}

func (s *server) inspectConnectorHTTPS(r *http.Request) connectorProxyDiagnostics {
	peer := requestPeerIP(r.RemoteAddr).Unmap()
	result := connectorProxyDiagnostics{BackendTLS: r.TLS != nil, ForwardedStatus: connectorForwardedStatus(r.Header)}
	if peer.IsValid() {
		result.PeerIP = peer.String()
		result.PeerLoopback = peer.IsLoopback()
		result.PeerTrusted = result.PeerLoopback || slices.Contains(s.currentConnectorProxyConfig().TrustedProxyIPs, peer)
	}
	result.HTTPSAccepted = result.BackendTLS || (result.PeerTrusted && result.ForwardedStatus == "https")
	return result
}

func (s *server) inspectConnectorProxy(w http.ResponseWriter, r *http.Request, _ auth.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.inspectConnectorHTTPS(r))
}
