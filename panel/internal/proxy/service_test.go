package proxy

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestCreateTLSProxyWithFirstClientAndVersion(t *testing.T) {
	db, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	value, mutation, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "TLS 节点", ListenPort: 443,
		EntryHostMode: EntryHostManual, EntryHost: "node.example.com",
		Enabled: true, Security: SecurityTLS, ServerName: "tls.example.com",
		Certificate: certificate, PrivateKey: privateKey, FirstClientName: "默认客户端",
	})
	if err != nil {
		t.Fatalf("create TLS proxy: %v", err)
	}
	if value.Protocol != ProtocolVLESS || value.Config.Transport != TransportTCP ||
		value.Config.Security != SecurityTLS || value.Config.ServerFlow != ServerFlow ||
		!value.Config.TLSCertificateConfigured || len(value.Clients) != 1 || value.Clients[0].ClientUDP443 {
		t.Fatalf("created proxy = %+v", value)
	}
	if mutation.ServerID != serverID || mutation.Version != 2 {
		t.Fatalf("mutation = %+v, want server %d version 2", mutation, serverID)
	}
	client, err := service.GetClient(t.Context(), value.Clients[0].ID)
	if err != nil || !validUUID(client.UUID) {
		t.Fatalf("first client = %+v, %v", client, err)
	}
	var stored string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, value.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	storedConfig, err := decodeConfig(ProtocolVLESS, stored)
	if err != nil || storedConfig.TLS == nil || storedConfig.TLS.PrivateKey != strings.TrimSpace(privateKey) {
		t.Fatal("TLS private key was not persisted for desired state")
	}
}

func TestCreateRealityProxyGeneratesCompatibleSecrets(t *testing.T) {
	_, service, serverID := newTestService(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "Reality", ListenPort: 8443, EntryHostMode: EntryHostAuto, Enabled: true,
		Security: SecurityReality, ServerName: "www.example.com", RealityTarget: "www.example.com:443",
		FirstClientName: "Phone",
	})
	if err != nil {
		t.Fatalf("create REALITY proxy: %v", err)
	}
	if value.Config.RealityPublicKey == "" || len(value.Config.RealityShortID) != 16 {
		t.Fatalf("public REALITY config = %+v", value.Config)
	}
	_, config, err := getProxyForTest(service, value.ID)
	if err != nil || config.Reality == nil || validateReality(config.Reality) != nil {
		t.Fatalf("stored REALITY config = %+v, %v", config, err)
	}
	privateBytes, _ := base64.RawURLEncoding.DecodeString(config.Reality.PrivateKey)
	if len(privateBytes) != 32 || privateBytes[0]&7 != 0 || privateBytes[31]&128 != 0 || privateBytes[31]&64 == 0 {
		t.Fatalf("private key is not Xray-compatible: length %d", len(privateBytes))
	}
}

func TestProxyCreationRollsBackAndRejectsPortConflict(t *testing.T) {
	db, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	input := CreateInput{ServerID: serverID, Name: "one", ListenPort: 443, EntryHostMode: EntryHostAuto, Enabled: true, Security: SecurityTLS, ServerName: "example.com", Certificate: certificate, PrivateKey: privateKey, FirstClientName: "default"}
	if _, _, err := service.Create(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input.Name = "two"
	if _, _, err := service.Create(t.Context(), input); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("duplicate port error = %v", err)
	}
	var proxyCount, clientCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&proxyCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&clientCount)
	if proxyCount != 1 || clientCount != 1 {
		t.Fatalf("counts after conflict = proxies %d clients %d", proxyCount, clientCount)
	}

	if _, err := db.Exec(`CREATE TRIGGER reject_client BEFORE INSERT ON clients BEGIN SELECT RAISE(ABORT, 'no client'); END`); err != nil {
		t.Fatal(err)
	}
	input.ListenPort = 444
	if _, _, err := service.Create(t.Context(), input); err == nil {
		t.Fatal("client insert failure did not fail proxy creation")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&proxyCount)
	if proxyCount != 1 {
		t.Fatalf("failed transaction left %d proxies", proxyCount)
	}
}

func TestClientLifecycleKeepsUUIDAndOneClient(t *testing.T) {
	_, service, serverID := newTestService(t)
	proxyValue := createRealityProxy(t, service, serverID, 443, "Multi")
	first, err := service.GetClient(t.Context(), proxyValue.Clients[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	second, mutation, err := service.CreateClient(t.Context(), proxyValue.ID, ClientCreateInput{Name: "Laptop", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	secondUUID := second.UUID
	if second.UUID == first.UUID || mutation.Version != 3 {
		t.Fatalf("second client = %+v, mutation = %+v", second, mutation)
	}
	name := "Laptop 2"
	udp := true
	disabled := false
	second, mutation, err = service.UpdateClient(t.Context(), second.ID, ClientUpdateInput{Name: &name, ClientUDP443: &udp, Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if second.UUID != secondUUID || second.Name != name || !second.ClientUDP443 || second.Enabled || mutation.Version != 4 {
		t.Fatalf("updated client = %+v, mutation = %+v", second, mutation)
	}
	if _, err := service.DeleteClient(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteClient(t.Context(), second.ID); !errors.Is(err, ErrLastClient) {
		t.Fatalf("last client deletion error = %v", err)
	}
}

func TestProxyUpdatesPreserveTLSAndRealitySecrets(t *testing.T) {
	_, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	tlsProxy, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "TLS", ListenPort: 443, EntryHostMode: EntryHostAuto, Enabled: true,
		Security: SecurityTLS, ServerName: "tls.example.com", Certificate: certificate,
		PrivateKey: privateKey, FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	newName := "TLS renamed"
	if _, _, err := service.Update(t.Context(), tlsProxy.ID, UpdateInput{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	_, tlsConfig, err := getProxyForTest(service, tlsProxy.ID)
	if err != nil || tlsConfig.TLS == nil || tlsConfig.TLS.Certificate != strings.TrimSpace(certificate) || tlsConfig.TLS.PrivateKey != strings.TrimSpace(privateKey) {
		t.Fatalf("TLS material changed during ordinary edit: %v", err)
	}

	realityProxy := createRealityProxy(t, service, serverID, 8443, "REALITY")
	_, before, err := getProxyForTest(service, realityProxy.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := "www.cloudflare.com:443"
	if _, _, err := service.Update(t.Context(), realityProxy.ID, UpdateInput{RealityTarget: &target}); err != nil {
		t.Fatal(err)
	}
	_, after, err := getProxyForTest(service, realityProxy.ID)
	if err != nil || before.Reality == nil || after.Reality == nil ||
		before.Reality.PrivateKey != after.Reality.PrivateKey || before.Reality.PublicKey != after.Reality.PublicKey || before.Reality.ShortID != after.Reality.ShortID {
		t.Fatalf("REALITY secrets changed during ordinary edit: %v", err)
	}
}

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

func TestDesiredStateFiltersDisabledRecordsAndClientUDPDoesNotChangeIt(t *testing.T) {
	db, service, serverID := newTestService(t)
	first := createRealityProxy(t, service, serverID, 443, "First")
	second := createRealityProxy(t, service, serverID, 8443, "Second")
	client, _, err := service.CreateClient(t.Context(), first.ID, ClientCreateInput{Name: "enabled", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	falseValue := false
	if _, _, err := service.UpdateClient(t.Context(), first.Clients[0].ID, ClientUpdateInput{Enabled: &falseValue}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Update(t.Context(), second.ID, UpdateInput{Enabled: &falseValue}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := ListDesired(t.Context(), tx, serverID)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 1 || desired[0].ID != first.ID || len(desired[0].Clients) != 1 ||
		desired[0].Clients[0].ID != client.ID || desired[0].Clients[0].StatsID != clientStatsIdentifier(client.ID) {
		t.Fatalf("desired proxies = %+v", desired)
	}
	if desired[0].Reality == nil || desired[0].Reality.PrivateKey == "" {
		t.Fatal("desired REALITY state lacks private material")
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

func TestProxyDeleteCascadesClientsAndServerDeleteCascadesProxy(t *testing.T) {
	db, service, serverID := newTestService(t)
	first := createRealityProxy(t, service, serverID, 443, "First")
	second := createRealityProxy(t, service, serverID, 8443, "Second")
	if _, err := service.Delete(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, db, 1, 1)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := ListDesired(t.Context(), tx, serverID)
	_ = tx.Rollback()
	if err != nil || len(desired) != 1 || desired[0].ID != second.ID || desired[0].Port != 8443 {
		t.Fatalf("desired state after Proxy delete = %+v, %v", desired, err)
	}
	if _, err := service.Delete(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, db, 0, 0)
	_ = createRealityProxy(t, service, serverID, 443, "Again")
	if _, err := db.Exec(`DELETE FROM servers WHERE id = ?`, serverID); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, db, 0, 0)
}

func TestDesiredMutationsMarkExistingAgentPendingWithoutAdvancingAppliedVersion(t *testing.T) {
	db, service, serverID := newTestService(t)
	if _, err := db.Exec(`INSERT INTO agents
		(server_id, token_hash, version, registered_at, last_seen_at, applied_config_version,
		 config_sync_status, config_sync_error, config_synced_at, created_at, updated_at)
		VALUES (?, 'agent-token', 'test', 1, 1, 7, 'failed', 'old failure', 123, 1, 1)`, serverID); err != nil {
		t.Fatal(err)
	}
	assertPending := func(label string) {
		t.Helper()
		var applied int64
		var status, message string
		var syncedAt sql.NullInt64
		if err := db.QueryRow(`SELECT applied_config_version, config_sync_status, config_sync_error, config_synced_at
			FROM agents WHERE server_id = ?`, serverID).Scan(&applied, &status, &message, &syncedAt); err != nil {
			t.Fatal(err)
		}
		if applied != 7 || status != "pending" || message != "" || !syncedAt.Valid || syncedAt.Int64 != 123 {
			t.Fatalf("%s Agent config state = applied %d status %q error %q synced %v", label, applied, status, message, syncedAt)
		}
	}
	reset := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE agents SET config_sync_status = 'failed', config_sync_error = 'old failure' WHERE server_id = ?`, serverID); err != nil {
			t.Fatal(err)
		}
	}

	proxyValue := createRealityProxy(t, service, serverID, 443, "Proxy")
	assertPending("Proxy create")
	reset()
	proxyName := "Proxy updated"
	if _, _, err := service.Update(t.Context(), proxyValue.ID, UpdateInput{Name: &proxyName}); err != nil {
		t.Fatal(err)
	}
	assertPending("Proxy update")
	reset()
	client, _, err := service.CreateClient(t.Context(), proxyValue.ID, ClientCreateInput{Name: "second", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	assertPending("Client create")
	reset()
	clientName := "second updated"
	if _, _, err := service.UpdateClient(t.Context(), client.ID, ClientUpdateInput{Name: &clientName}); err != nil {
		t.Fatal(err)
	}
	assertPending("Client update")
	reset()
	if _, err := service.DeleteClient(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	assertPending("Client delete")
	reset()
	if _, err := service.Delete(t.Context(), proxyValue.ID); err != nil {
		t.Fatal(err)
	}
	assertPending("Proxy delete")
}

func TestShadowsocksCreatesMethodSizedSecretsAndDesiredState(t *testing.T) {
	for _, test := range []struct {
		method string
		length int
	}{
		{ShadowsocksMethodAES128GCM, 16},
		{ShadowsocksMethodAES256GCM, 32},
	} {
		t.Run(test.method, func(t *testing.T) {
			db, service, serverID := newTestService(t)
			value, mutation, err := service.Create(t.Context(), CreateInput{
				ServerID: serverID, Name: "SS 节点", Protocol: ProtocolShadowsocks,
				Method: test.method, ListenPort: 8388, EntryHostMode: EntryHostManual,
				EntryHost: "node.example.com", Enabled: true, FirstClientName: "手机",
			})
			if err != nil {
				t.Fatal(err)
			}
			if value.Protocol != ProtocolShadowsocks || value.Config.Method != test.method ||
				value.Config.Network != ShadowsocksNetwork || value.Config.Transport != "" ||
				value.Config.Security != "" || len(value.Clients) != 1 || value.Clients[0].UUIDSummary != "" ||
				mutation.Version != 2 {
				t.Fatalf("created Shadowsocks proxy = %+v, mutation = %+v", value, mutation)
			}
			client, err := service.GetClient(t.Context(), value.Clients[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if decoded, err := base64.StdEncoding.Strict().DecodeString(client.Password); err != nil || len(decoded) != test.length {
				t.Fatalf("client password length = %d, %v", len(decoded), err)
			}
			var configJSON string
			if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, value.ID).Scan(&configJSON); err != nil {
				t.Fatal(err)
			}
			config, err := decodeConfig(ProtocolShadowsocks, configJSON)
			if err != nil || config.Shadowsocks == nil {
				t.Fatalf("stored Shadowsocks config = %+v, %v", config, err)
			}
			if decoded, err := base64.StdEncoding.Strict().DecodeString(config.Shadowsocks.Password); err != nil || len(decoded) != test.length {
				t.Fatalf("master password length = %d, %v", len(decoded), err)
			}
			desired, err := ListDesired(t.Context(), db, serverID)
			if err != nil || len(desired) != 1 || desired[0].Shadowsocks == nil ||
				desired[0].Shadowsocks.Method != test.method || desired[0].Shadowsocks.Network != ShadowsocksNetwork ||
				len(desired[0].Clients) != 1 || desired[0].Clients[0].Password != client.Password || desired[0].Clients[0].UUID != "" ||
				desired[0].Clients[0].StatsID != clientStatsIdentifier(client.ID) {
				t.Fatalf("Shadowsocks desired state = %+v, %v", desired, err)
			}
		})
	}
}

func TestShadowsocksValidationAndClientLifecycle(t *testing.T) {
	_, service, serverID := newTestService(t)
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "invalid", Protocol: ProtocolShadowsocks, Method: "aes-256-gcm",
		ListenPort: 8388, EntryHostMode: EntryHostAuto, Enabled: true, FirstClientName: "first",
	}); !errors.Is(err, ErrInvalidShadowsocksMethod) {
		t.Fatalf("invalid method error = %v", err)
	}
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "invalid UDP443", Protocol: ProtocolShadowsocks,
		ListenPort: 8388, EntryHostMode: EntryHostAuto, Enabled: true,
		FirstClientName: "first", FirstClientUDP443: true,
	}); !errors.Is(err, ErrShadowsocksClientUDP443) {
		t.Fatalf("Shadowsocks first client UDP443 error = %v", err)
	}
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "SS", Protocol: ProtocolShadowsocks,
		ListenPort: 8388, EntryHostMode: EntryHostAuto, Enabled: true, FirstClientName: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.Config.Method != ShadowsocksMethodAES128GCM {
		t.Fatalf("default Shadowsocks method = %q", value.Config.Method)
	}
	vless := ProtocolVLESS
	if _, _, err := service.Update(t.Context(), value.ID, UpdateInput{Protocol: &vless}); !errors.Is(err, ErrImmutableProtocol) {
		t.Fatalf("protocol update error = %v", err)
	}
	method := ShadowsocksMethodAES256GCM
	if _, _, err := service.Update(t.Context(), value.ID, UpdateInput{Method: &method}); !errors.Is(err, ErrImmutableShadowsocksMethod) {
		t.Fatalf("method update error = %v", err)
	}
	if _, _, err := service.CreateClient(t.Context(), value.ID, ClientCreateInput{Name: "invalid", ClientUDP443: true, Enabled: true}); !errors.Is(err, ErrShadowsocksClientUDP443) {
		t.Fatalf("Shadowsocks client UDP443 create error = %v", err)
	}
	second, _, err := service.CreateClient(t.Context(), value.ID, ClientCreateInput{Name: "second", Enabled: true})
	if err != nil || second.Password == "" || second.UUID != "" {
		t.Fatalf("second Shadowsocks client = %+v, %v", second, err)
	}
	udp := true
	if _, _, err := service.UpdateClient(t.Context(), second.ID, ClientUpdateInput{ClientUDP443: &udp}); !errors.Is(err, ErrShadowsocksClientUDP443) {
		t.Fatalf("Shadowsocks client UDP443 update error = %v", err)
	}
	newName, disabled := "renamed", false
	updated, _, err := service.UpdateClient(t.Context(), second.ID, ClientUpdateInput{Name: &newName, Enabled: &disabled})
	if err != nil || updated.Name != newName || updated.Enabled || updated.Password != second.Password {
		t.Fatalf("updated Shadowsocks client = %+v, %v", updated, err)
	}
	if _, err := service.DeleteClient(t.Context(), value.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestShadowsocksZeroEnabledClientsOmitsInboundAndRejectsInvalidStoredKeys(t *testing.T) {
	db, service, serverID := newTestService(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "SS", Protocol: ProtocolShadowsocks, ListenPort: 8388,
		EntryHostMode: EntryHostAuto, Enabled: true, FirstClientName: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, _, err := service.UpdateClient(t.Context(), value.Clients[0].ID, ClientUpdateInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	desired, err := ListDesired(t.Context(), db, serverID)
	if err != nil || len(desired) != 0 {
		t.Fatalf("zero-client Shadowsocks desired state = %+v, %v", desired, err)
	}
	if _, err := db.Exec(`UPDATE clients SET credential_json = '{"password":"not-base64"}' WHERE id = ?`, value.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetClient(t.Context(), value.Clients[0].ID); !errors.Is(err, ErrInvalidShadowsocksCredential) {
		t.Fatalf("invalid stored client password error = %v", err)
	}
	wrongLength, _ := json.Marshal(storedCredential{Password: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if _, err := db.Exec(`UPDATE clients SET credential_json = ? WHERE id = ?`, string(wrongLength), value.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetClient(t.Context(), value.Clients[0].ID); !errors.Is(err, ErrInvalidShadowsocksCredential) {
		t.Fatalf("method-mismatched client password error = %v", err)
	}
	if _, err := db.Exec(`UPDATE proxies SET config_json = '{"shadowsocks":{"method":"2022-blake3-aes-128-gcm","network":"tcp,udp","password":"bad"}}' WHERE id = ?`, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(t.Context(), value.ID); err == nil || !strings.Contains(err.Error(), "invalid stored Shadowsocks") {
		t.Fatalf("invalid stored master password error = %v", err)
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

func newTestService(t *testing.T) (*sql.DB, *Service, int64) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	result, err := db.Exec(`INSERT INTO servers (name, status, created_at, updated_at) VALUES ('test', 'offline', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return db, NewService(db), id
}

func createRealityProxy(t *testing.T, service *Service, serverID int64, port int, name string) Proxy {
	t.Helper()
	value, _, err := service.Create(t.Context(), CreateInput{ServerID: serverID, Name: name, ListenPort: port, EntryHostMode: EntryHostAuto, Enabled: true, Security: SecurityReality, ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "默认客户端"})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(certificate), string(privateKey)
}

func getProxyForTest(service *Service, id int64) (Proxy, storedConfig, error) {
	row := service.db.QueryRow(`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		proxies.config_json, proxies.created_at, proxies.updated_at
		FROM proxies JOIN servers ON servers.id = proxies.server_id
		LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id WHERE proxies.id = ?`, id)
	return scanProxy(row)
}

func assertCounts(t *testing.T, db *sql.DB, proxies, clients int) {
	t.Helper()
	var proxyCount, clientCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&proxyCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&clientCount)
	if proxyCount != proxies || clientCount != clients {
		t.Fatalf("counts = proxies %d clients %d", proxyCount, clientCount)
	}
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
