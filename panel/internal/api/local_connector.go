package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

// ParseConnectorTrustedProxyIPs accepts exact peer addresses. A bad entry
// invalidates the whole list so a mistyped configuration cannot be used.
func ParseConnectorTrustedProxyIPs(value string) ([]netip.Addr, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var ips []netip.Addr
	for index, entry := range strings.Split(value, ",") {
		ip, err := netip.ParseAddr(strings.TrimSpace(entry))
		ip = ip.Unmap()
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return nil, fmt.Errorf("entry %d must be a single unicast IP address", index+1)
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func (s *server) requireConnectorHTTPS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS != nil {
		return true
	}
	// Only the immediate TCP peer can attest to TLS termination.
	peer := requestPeerIP(r.RemoteAddr).Unmap()
	if peer.IsValid() && (peer.IsLoopback() || slices.Contains(s.connectorProxyIPs, peer)) && connectorForwardedHTTPS(r.Header) {
		return true
	}
	writeError(w, http.StatusForbidden, "本地连接器需要通过 HTTPS 访问 Panel")
	return false
}

func connectorForwardedHTTPS(header http.Header) bool {
	isHTTPS := func(values []string) bool {
		if len(values) != 1 {
			return false
		}
		scheme := strings.Trim(values[0], " \t")
		return len(scheme) == len("https") && strings.EqualFold(scheme, "https")
	}
	// Use a single scheme assertion. NPM also emits X-Forwarded-Scheme
	// and it must agree whenever present.
	scheme := header.Values("X-Forwarded-Scheme")
	return isHTTPS(header.Values("X-Forwarded-Proto")) &&
		(len(scheme) == 0 || isHTTPS(scheme)) && len(header.Values("Forwarded")) == 0
}

func (s *server) listLocalConnectors(w http.ResponseWriter, r *http.Request, user auth.User) {
	w.Header().Set("Cache-Control", "no-store")
	values, err := s.remoteSubscriptions.ListConnectors(r.Context(), personalActor(user))
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": values})
}

func (s *server) createLocalConnectorPairing(w http.ResponseWriter, r *http.Request, user auth.User) {
	if !s.requireConnectorHTTPS(w, r) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, code, err := s.remoteSubscriptions.CreateConnectorPairing(r.Context(), personalActor(user), strings.TrimSpace(input.Name))
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "local_connector.pair", "local_connector", value.ID, "创建本地连接器配对码")
	writeJSON(w, http.StatusCreated, map[string]any{"connector": value, "pairing_code": code})
}

func (s *server) revokeLocalConnector(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "本地连接器 ID 无效")
	if !ok {
		return
	}
	if err := s.remoteSubscriptions.RevokeConnector(r.Context(), personalActor(user), id); err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	s.recordAudit(r, user, "local_connector.revoke", "local_connector", id, "撤销本地连接器")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) pairLocalConnector(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnectorHTTPS(w, r) {
		return
	}
	code := connectorBearer(r)
	value, err := s.remoteSubscriptions.PairConnector(r.Context(), code)
	if errors.Is(err, subscriptionstore.ErrLocalConnectorAuth) {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		writeRemoteSubscriptionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, localconnector.PairResponse{Credential: value})
}

func (s *server) connectLocalConnector(w http.ResponseWriter, r *http.Request) {
	if !s.requireConnectorHTTPS(w, r) {
		return
	}
	credential := connectorBearer(r)
	// Browser sessions and browser-originated upgrades are never connector auth.
	if r.Header.Get("Origin") != "" {
		writeError(w, http.StatusForbidden, "本地连接器需要独立认证")
		return
	}
	if _, err := s.remoteSubscriptions.AuthenticateConnector(r.Context(), credential); err != nil {
		if errors.Is(err, subscriptionstore.ErrLocalConnectorAuth) {
			writeError(w, http.StatusUnauthorized, err.Error())
		} else {
			writeRemoteSubscriptionError(w, err)
		}
		return
	}
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer socket.CloseNow()
	s.remoteSubscriptions.ServeConnector(r.Context(), credential, socket)
}

func connectorBearer(r *http.Request) string {
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return value
}
