package proxy

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestTLSShareUsesManualEntryHostAndPerClientUDPFlow(t *testing.T) {
	_, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "TLS 节点", ListenPort: 2053,
		EntryHostMode: EntryHostManual, EntryHost: "node.example.com",
		Enabled: true, Security: SecurityTLS, ServerName: "sni.example.com",
		Certificate: certificate, PrivateKey: privateKey, FirstClientName: "默认客户端",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreateClient(t.Context(), value.ID, ClientCreateInput{Name: "手机 用户", ClientUDP443: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	firstShare, err := service.GetClientShare(t.Context(), value.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	secondShare, err := service.GetClientShare(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstURI, _ := url.Parse(firstShare.URI)
	secondURI, _ := url.Parse(secondShare.URI)
	if firstURI.Host != "node.example.com:2053" || firstURI.Query().Get("sni") != "sni.example.com" ||
		firstURI.Query().Get("type") != TransportTCP || firstURI.Query().Get("security") != SecurityTLS ||
		firstURI.Query().Get("flow") != ServerFlow || secondURI.Query().Get("flow") != ServerFlow+"-udp443" ||
		firstShare.UUID == secondShare.UUID || secondURI.Fragment != "TLS 节点 - 手机 用户" {
		t.Fatalf("TLS shares = %q and %q", firstShare.URI, secondShare.URI)
	}
	if strings.Contains(firstShare.URI, privateKey) || firstURI.Query().Has("pbk") || firstURI.Query().Has("sid") {
		t.Fatal("TLS share leaked server material or REALITY parameters")
	}
	if firstURI.Query().Has("alpn") || firstURI.Query().Has("headerType") {
		t.Fatal("TLS share unexpectedly contains REALITY transport parameters")
	}
}

func TestVLESSShareAutoUsesOnlyPublicIPv4AndManualOverridesIt(t *testing.T) {
	db, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "东京 节点")
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (?, '', '', '', '', '', ?, ?, '', '', 1)`, serverID,
		`["10.0.0.1","172.26.13.110","192.168.1.20"]`, `["2001:db8::20"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetClientShare(t.Context(), proxyValue.Clients[0].ID); !errors.Is(err, ErrConnectionAddressUnavailable) {
		t.Fatalf("auto share fell back to NIC address: %v", err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = '198.51.100.44' WHERE server_id = ?`, serverID); err != nil {
		t.Fatal(err)
	}
	share, err := service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(share.URI)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "vless" || parsed.Host != "198.51.100.44:443" || parsed.Query().Get("security") != "reality" ||
		parsed.Query().Get("pbk") != share.RealityPublicKey || parsed.Query().Get("sid") != share.RealityShortID ||
		parsed.Query().Get("alpn") != "h2,http/1.1" || parsed.Query().Get("headerType") != "none" ||
		parsed.Query().Get("type") != TransportTCP ||
		parsed.Query().Get("flow") != ServerFlow || parsed.Fragment != "东京 节点 - 默认客户端" {
		t.Fatalf("share URI = %s", share.URI)
	}
	_, config, err := getProxyForTest(service, proxyValue.ID)
	if err != nil || config.Reality == nil {
		t.Fatalf("stored REALITY config = %+v, %v", config, err)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(mustDecodeBase64(t, config.Reality.PrivateKey))
	if err != nil || base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != config.Reality.PublicKey ||
		config.Reality.PublicKey != share.RealityPublicKey {
		t.Fatal("stored and shared REALITY public keys do not match the private key")
	}
	if strings.Contains(share.URI, config.Reality.PrivateKey) || parsed.Query().Has("proxy_id") || parsed.Query().Has("client_id") {
		t.Fatal("share URI leaked server secret or internal ID")
	}
	manualMode, manualHost := EntryHostManual, "1.2.3.4"
	if _, _, err := service.Update(t.Context(), proxyValue.ID, UpdateInput{EntryHostMode: &manualMode, EntryHost: &manualHost}); err != nil {
		t.Fatal(err)
	}
	manualShare, err := service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil || manualShare.Address != "1.2.3.4" || !strings.Contains(manualShare.URI, "@1.2.3.4:443") ||
		manualShare.ServerName != share.ServerName || manualShare.RealityPublicKey != share.RealityPublicKey ||
		manualShare.RealityShortID != share.RealityShortID || manualShare.Flow != share.Flow {
		t.Fatalf("manual IPv4 share = %+v, %v", manualShare, err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = '203.0.113.18' WHERE server_id = ?`, serverID); err != nil {
		t.Fatal(err)
	}
	manualShareAfterPublicChange, err := service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil || manualShareAfterPublicChange.Address != "1.2.3.4" || manualShareAfterPublicChange.URI != manualShare.URI {
		t.Fatalf("manual share changed with public IPv4 = %+v, %v", manualShareAfterPublicChange, err)
	}
	manualHost = "[2001:db8::1]"
	if _, _, err := service.Update(t.Context(), proxyValue.ID, UpdateInput{EntryHost: &manualHost}); err != nil {
		t.Fatal(err)
	}
	manualShare, err = service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil || manualShare.Address != "2001:db8::1" || !strings.Contains(manualShare.URI, "@[2001:db8::1]:443") {
		t.Fatalf("manual IPv6 share = %+v, %v", manualShare, err)
	}
	manualHost = "node.example.com"
	if _, _, err := service.Update(t.Context(), proxyValue.ID, UpdateInput{EntryHost: &manualHost}); err != nil {
		t.Fatal(err)
	}
	manualShare, err = service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil || manualShare.Address != "node.example.com" || !strings.Contains(manualShare.URI, "@node.example.com:443") {
		t.Fatalf("hostname share = %+v, %v", manualShare, err)
	}
	udp443 := true
	if _, _, err := service.UpdateClient(t.Context(), proxyValue.Clients[0].ID, ClientUpdateInput{ClientUDP443: &udp443}); err != nil {
		t.Fatal(err)
	}
	udpShare, err := service.GetClientShare(t.Context(), proxyValue.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	udpURI, err := url.Parse(udpShare.URI)
	if err != nil || udpURI.Query().Get("flow") != ServerFlow+"-udp443" || udpURI.Query().Get("pbk") != share.RealityPublicKey ||
		udpURI.Query().Get("alpn") != "h2,http/1.1" || udpURI.Query().Get("headerType") != "none" {
		t.Fatalf("REALITY UDP/443 share = %s, %v", udpShare.URI, err)
	}
}

func TestClientShareEndpointOverridePreservesCredentialsAndProtocolParameters(t *testing.T) {
	_, service, serverID := newTestService(t)
	reality := createRealityProxy(t, service, serverID, 443, "REALITY")
	direct, err := service.GetClientShareAtEndpoint(t.Context(), reality.Clients[0].ID, ShareEndpoint{
		Address: "target.example.com", Port: 443,
	})
	if err != nil {
		t.Fatal(err)
	}
	relayed, err := service.GetClientShareAtEndpoint(t.Context(), reality.Clients[0].ID, ShareEndpoint{
		Address: "2001:db8::10", Port: 35152,
	})
	if err != nil {
		t.Fatal(err)
	}
	directURI, _ := url.Parse(direct.URI)
	relayedURI, _ := url.Parse(relayed.URI)
	if directURI.Host != "target.example.com:443" || relayedURI.Host != "[2001:db8::10]:35152" ||
		directURI.User.String() != relayedURI.User.String() || directURI.RawQuery != relayedURI.RawQuery ||
		directURI.Fragment != relayedURI.Fragment || direct.UUID != relayed.UUID ||
		directURI.Query().Get("sni") != relayedURI.Query().Get("sni") ||
		directURI.Query().Get("pbk") != relayedURI.Query().Get("pbk") ||
		directURI.Query().Get("sid") != relayedURI.Query().Get("sid") ||
		directURI.Query().Get("fp") != relayedURI.Query().Get("fp") {
		t.Fatalf("VLESS endpoint override changed protocol material: direct %q, Relay %q", direct.URI, relayed.URI)
	}

	for index, method := range []string{ShadowsocksMethodAES128GCM, ShadowsocksMethodAES256GCM} {
		port := 8388 + index
		value, _, err := service.Create(t.Context(), CreateInput{
			ServerID: serverID, Name: "SS", Protocol: ProtocolShadowsocks, Method: method,
			ListenPort: port, EntryHostMode: EntryHostManual, EntryHost: "target.example.com",
			Enabled: true, FirstClientName: "Client",
		})
		if err != nil {
			t.Fatal(err)
		}
		direct, err := service.GetClientShareAtEndpoint(t.Context(), value.Clients[0].ID, ShareEndpoint{Address: "target.example.com", Port: port})
		if err != nil {
			t.Fatal(err)
		}
		relayed, err := service.GetClientShareAtEndpoint(t.Context(), value.Clients[0].ID, ShareEndpoint{Address: "relay.example.com", Port: 9502 + index})
		if err != nil {
			t.Fatal(err)
		}
		directURI, _ := url.Parse(direct.URI)
		relayedURI, _ := url.Parse(relayed.URI)
		if relayedURI.Host != "relay.example.com:"+strconv.Itoa(9502+index) ||
			directURI.User.String() != relayedURI.User.String() || directURI.Fragment != relayedURI.Fragment ||
			direct.Method != method || relayed.Method != method || direct.Password != relayed.Password {
			t.Fatalf("Shadowsocks endpoint override changed credentials for %s: direct %q, Relay %q", method, direct.URI, relayed.URI)
		}
	}
}

func TestShadowsocksSIP002ShareUsesMasterAndUserPassword(t *testing.T) {
	db, service, serverID := newTestService(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "东京 节点", Protocol: ProtocolShadowsocks,
		Method: ShadowsocksMethodAES128GCM, ListenPort: 8388, EntryHostMode: EntryHostManual,
		EntryHost: "[2001:db8::8]", Enabled: true, FirstClientName: "手机 + 用户",
	})
	if err != nil {
		t.Fatal(err)
	}
	master := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 16))
	userPassword := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 16))
	config := storedConfig{Shadowsocks: &storedShadowsocks{Method: ShadowsocksMethodAES128GCM, Network: ShadowsocksNetwork, Password: master}}
	configJSON, _ := json.Marshal(config)
	credentialJSON, _ := json.Marshal(storedCredential{Password: userPassword})
	if _, err := db.Exec(`UPDATE proxies SET config_json = ? WHERE id = ?`, string(configJSON), value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clients SET credential_json = ? WHERE id = ?`, string(credentialJSON), value.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	share, err := service.GetClientShare(t.Context(), value.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(share.URI)
	if err != nil {
		t.Fatal(err)
	}
	password, ok := parsed.User.Password()
	if !ok || parsed.Scheme != "ss" || parsed.User.Username() != ShadowsocksMethodAES128GCM ||
		password != master+":"+userPassword || parsed.Host != "[2001:db8::8]:8388" ||
		parsed.Fragment != "东京 节点 - 手机 + 用户" || share.Protocol != ProtocolShadowsocks || share.Network != ShadowsocksNetwork {
		t.Fatalf("Shadowsocks share = %+v, URI %q", share, share.URI)
	}
	if strings.Contains(share.URI, base64.RawURLEncoding.EncodeToString([]byte(ShadowsocksMethodAES128GCM+":"+master+":"+userPassword))) {
		t.Fatal("SS2022 URI incorrectly used legacy whole-userinfo Base64")
	}
}

func TestShadowsocksSIP002ShareSupportsBothMethodsAndEntryHostKinds(t *testing.T) {
	_, service, serverID := newTestService(t)
	tests := []struct {
		method   string
		host     string
		wantHost string
		port     int
	}{
		{ShadowsocksMethodAES128GCM, "198.51.100.10", "198.51.100.10:8388", 8388},
		{ShadowsocksMethodAES256GCM, "ss.example.com", "ss.example.com:8389", 8389},
	}
	for _, test := range tests {
		value, _, err := service.Create(t.Context(), CreateInput{
			ServerID: serverID, Name: "SS", Protocol: ProtocolShadowsocks, Method: test.method,
			ListenPort: test.port, EntryHostMode: EntryHostManual, EntryHost: test.host,
			Enabled: true, FirstClientName: "Client",
		})
		if err != nil {
			t.Fatal(err)
		}
		share, err := service.GetClientShare(t.Context(), value.Clients[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(share.URI)
		if err != nil {
			t.Fatal(err)
		}
		password, ok := parsed.User.Password()
		parts := strings.Split(password, ":")
		if !ok || parsed.User.Username() != test.method || parsed.Host != test.wantHost || len(parts) != 2 ||
			!validShadowsocksKey(parts[0], test.method) || !validShadowsocksKey(parts[1], test.method) {
			t.Fatalf("Shadowsocks share for %s = %q", test.method, share.URI)
		}
	}
}
