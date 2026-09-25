package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

type capabilityAPIFixture struct {
	db       *sql.DB
	handler  http.Handler
	cookie   *http.Cookie
	serverID int64
}

func newCapabilityAPIFixture(t *testing.T, implementation string, capabilities []string) capabilityAPIFixture {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]
	createdResponse := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Capability Server"}, cookie)
	var created createdServerResponse
	if createdResponse.Code != http.StatusCreated || json.Unmarshal(createdResponse.Body.Bytes(), &created) != nil {
		t.Fatalf("create server = %d, %s", createdResponse.Code, createdResponse.Body.String())
	}
	registration := agentRegistrationRequest{
		EnrollmentToken: created.EnrollmentToken, AgentVersion: "v1.0.0", ExistingConfig: false,
	}
	if implementation != "" {
		registration.AgentImplementation = implementation
		registration.AgentAPIVersion = agentcontrol.CurrentAPIVersion
		registration.AgentCapabilities = capabilities
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/agent/register", registration, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register Agent = %d, %s", registered.Code, registered.Body.String())
	}
	return capabilityAPIFixture{db: db, handler: handler, cookie: cookie, serverID: created.Server.ID}
}

func (fixture capabilityAPIFixture) createProxy(t *testing.T, body createProxyRequest) *httptest.ResponseRecorder {
	t.Helper()
	body.ServerID = fixture.serverID
	return performRequest(t, fixture.handler, http.MethodPost, "/api/proxies", body, fixture.cookie)
}

func realityProxyRequest(port int) createProxyRequest {
	return createProxyRequest{Name: "REALITY", ListenPort: port, Security: "reality", ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "default"}
}

func acmeProxyRequest(port int) createProxyRequest {
	return createProxyRequest{Name: "ACME", ListenPort: port, Security: "tls", TLSMode: "acme", ServerName: "node.example.com", FirstClientName: "default"}
}

func shadowsocksProxyRequest(port int) createProxyRequest {
	return createProxyRequest{Name: "Shadowsocks", Protocol: "shadowsocks", Method: "2022-blake3-aes-128-gcm", ListenPort: port, FirstClientName: "default"}
}

func manualTLSProxyRequest(t *testing.T, port int) createProxyRequest {
	t.Helper()
	certificate, privateKey := apiTestCertificate(t)
	return createProxyRequest{Name: "Manual TLS", ListenPort: port, Security: "tls", TLSMode: "manual", ServerName: "manual.example.com", Certificate: certificate, PrivateKey: privateKey, FirstClientName: "default"}
}

func TestProxyCapabilityEnforcement(t *testing.T) {
	t.Run("legacy remains compatible", func(t *testing.T) {
		fixture := newCapabilityAPIFixture(t, "", nil)
		if response := fixture.createProxy(t, realityProxyRequest(443)); response.Code != http.StatusCreated {
			t.Fatalf("legacy create = %d, %s", response.Code, response.Body.String())
		}
	})

	t.Run("reality only", func(t *testing.T) {
		fixture := newCapabilityAPIFixture(t, "third-party-agent", []string{agentcontrol.CapabilityProxyVLESSReality})
		for _, test := range []struct {
			name string
			body createProxyRequest
			want int
		}{
			{"reality", realityProxyRequest(443), http.StatusCreated},
			{"acme", acmeProxyRequest(8443), http.StatusConflict},
			{"manual", manualTLSProxyRequest(t, 9443), http.StatusConflict},
			{"shadowsocks", shadowsocksProxyRequest(8388), http.StatusConflict},
		} {
			response := fixture.createProxy(t, test.body)
			if response.Code != test.want {
				t.Fatalf("%s create = %d, %s", test.name, response.Code, response.Body.String())
			}
		}
		invalidManual := manualTLSProxyRequest(t, 10443)
		invalidManual.Certificate = ""
		invalidManual.PrivateKey = ""
		if response := fixture.createProxy(t, invalidManual); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid manual TLS = %d, %s", response.Code, response.Body.String())
		}
		for _, invalid := range []createProxyRequest{
			{Name: "invalid protocol", Protocol: "invalid", ListenPort: 10444, FirstClientName: "default"},
			{Name: "invalid security", ListenPort: 10445, Security: "invalid", ServerName: "example.com", FirstClientName: "default"},
			{Name: "invalid TLS mode", ListenPort: 10446, Security: "tls", TLSMode: "invalid", ServerName: "example.com", FirstClientName: "default"},
		} {
			if response := fixture.createProxy(t, invalid); response.Code != http.StatusBadRequest {
				t.Fatalf("invalid feature combination = %d, %s", response.Code, response.Body.String())
			}
		}
	})

	t.Run("acme only", func(t *testing.T) {
		fixture := newCapabilityAPIFixture(t, "third-party-agent", []string{agentcontrol.CapabilityProxyVLESSACME})
		if response := fixture.createProxy(t, acmeProxyRequest(443)); response.Code != http.StatusCreated {
			t.Fatalf("ACME create = %d, %s", response.Code, response.Body.String())
		}
		if response := fixture.createProxy(t, realityProxyRequest(8443)); response.Code != http.StatusConflict {
			t.Fatalf("REALITY create = %d, %s", response.Code, response.Body.String())
		}
	})
}

func TestUnsupportedHistoricalProxyCanBeDisabledAndDeletedButNotReenabled(t *testing.T) {
	fixture := newCapabilityAPIFixture(t, "", nil)
	create := func(body createProxyRequest) proxyResponse {
		response := fixture.createProxy(t, body)
		var result struct {
			Proxy proxyResponse `json:"proxy"`
		}
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("create historical proxy = %d, %s", response.Code, response.Body.String())
		}
		return result.Proxy
	}
	enabledProxy := create(shadowsocksProxyRequest(8388))
	disabledRequest := shadowsocksProxyRequest(8389)
	disabledRequest.Enabled = boolPointer(false)
	disabledProxy := create(disabledRequest)
	setAgentCapabilities(t, fixture.db, fixture.serverID, "third-party-agent", []string{agentcontrol.CapabilityMetrics})

	disabled := false
	path := "/api/proxies/" + strconv.FormatInt(enabledProxy.ID, 10)
	if response := performRequest(t, fixture.handler, http.MethodPatch, path, updateProxyRequest{Enabled: &disabled}, fixture.cookie); response.Code != http.StatusOK {
		t.Fatalf("disable unsupported proxy = %d, %s", response.Code, response.Body.String())
	}
	keepDisabledPath := "/api/proxies/" + strconv.FormatInt(disabledProxy.ID, 10)
	if response := performRequest(t, fixture.handler, http.MethodPatch, keepDisabledPath, updateProxyRequest{Enabled: &disabled}, fixture.cookie); response.Code != http.StatusOK {
		t.Fatalf("keep unsupported proxy disabled = %d, %s", response.Code, response.Body.String())
	}
	enabled := true
	if response := performRequest(t, fixture.handler, http.MethodPatch, keepDisabledPath, updateProxyRequest{Enabled: &enabled}, fixture.cookie); response.Code != http.StatusConflict {
		t.Fatalf("reenable unsupported proxy = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, fixture.handler, http.MethodDelete, keepDisabledPath, nil, fixture.cookie); response.Code != http.StatusNoContent {
		t.Fatalf("delete unsupported proxy = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, fixture.handler, http.MethodDelete, path, nil, fixture.cookie); response.Code != http.StatusConflict {
		t.Fatalf("delete last unsupported proxy = %d, %s", response.Code, response.Body.String())
	}
	setAgentCapabilities(t, fixture.db, fixture.serverID, "third-party-agent", []string{agentcontrol.CapabilityManagedRuntimePurge})
	if response := performRequest(t, fixture.handler, http.MethodDelete, path, nil, fixture.cookie); response.Code != http.StatusNoContent {
		t.Fatalf("delete last purge-capable proxy = %d, %s", response.Code, response.Body.String())
	}
}

func TestRelayCapabilityEnforcement(t *testing.T) {
	fixture := newCapabilityAPIFixture(t, "third-party-agent", []string{agentcontrol.CapabilityMetrics})
	body := createRelayRequest{ServerID: fixture.serverID, Name: "Relay", ListenPort: 9502, TargetType: "manual", TargetHost: "example.com", TargetPort: 443, Network: "tcp"}
	if response := performRequest(t, fixture.handler, http.MethodPost, "/api/relays", body, fixture.cookie); response.Code != http.StatusConflict {
		t.Fatalf("create unsupported Relay = %d, %s", response.Code, response.Body.String())
	}

	legacy := newCapabilityAPIFixture(t, "", nil)
	body.ServerID = legacy.serverID
	createdResponse := performRequest(t, legacy.handler, http.MethodPost, "/api/relays", body, legacy.cookie)
	var created struct {
		Relay relayResponse `json:"relay"`
	}
	if createdResponse.Code != http.StatusCreated || json.Unmarshal(createdResponse.Body.Bytes(), &created) != nil {
		t.Fatalf("create historical Relay = %d, %s", createdResponse.Code, createdResponse.Body.String())
	}
	setAgentCapabilities(t, legacy.db, legacy.serverID, "third-party-agent", []string{agentcontrol.CapabilityMetrics})
	path := "/api/relays/" + strconv.FormatInt(created.Relay.ID, 10)
	disabled := false
	if response := performRequest(t, legacy.handler, http.MethodPatch, path, updateRelayRequest{Enabled: &disabled}, legacy.cookie); response.Code != http.StatusOK {
		t.Fatalf("disable unsupported Relay = %d, %s", response.Code, response.Body.String())
	}
	enabled := true
	if response := performRequest(t, legacy.handler, http.MethodPatch, path, updateRelayRequest{Enabled: &enabled}, legacy.cookie); response.Code != http.StatusConflict {
		t.Fatalf("reenable unsupported Relay = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, legacy.handler, http.MethodDelete, path, nil, legacy.cookie); response.Code != http.StatusConflict {
		t.Fatalf("delete last unsupported Relay = %d, %s", response.Code, response.Body.String())
	}
	setAgentCapabilities(t, legacy.db, legacy.serverID, "third-party-agent", []string{agentcontrol.CapabilityManagedRuntimePurge})
	if response := performRequest(t, legacy.handler, http.MethodDelete, path, nil, legacy.cookie); response.Code != http.StatusNoContent {
		t.Fatalf("delete last purge-capable Relay = %d, %s", response.Code, response.Body.String())
	}
}

func TestOutboundPreferenceCapabilityEnforcement(t *testing.T) {
	fixture := newCapabilityAPIFixture(t, "third-party-agent", []string{agentcontrol.CapabilityMetrics})
	path := "/api/servers/" + strconv.FormatInt(fixture.serverID, 10)
	for _, preference := range []string{"prefer_ipv4", "prefer_ipv6"} {
		response := performRequest(t, fixture.handler, http.MethodPatch, path, map[string]string{"outbound_preference": preference}, fixture.cookie)
		if response.Code != http.StatusConflict {
			t.Fatalf("unsupported %s = %d, %s", preference, response.Code, response.Body.String())
		}
	}
	if response := performRequest(t, fixture.handler, http.MethodPatch, path, map[string]string{"outbound_preference": "auto"}, fixture.cookie); response.Code != http.StatusOK {
		t.Fatalf("restore auto = %d, %s", response.Code, response.Body.String())
	}

	supported := newCapabilityAPIFixture(t, "third-party-agent", []string{agentcontrol.CapabilityOutboundPreference})
	supportedPath := "/api/servers/" + strconv.FormatInt(supported.serverID, 10)
	for _, preference := range []string{"prefer_ipv4", "prefer_ipv6", "auto"} {
		response := performRequest(t, supported.handler, http.MethodPatch, supportedPath, map[string]string{"outbound_preference": preference}, supported.cookie)
		if response.Code != http.StatusOK {
			t.Fatalf("supported %s = %d, %s", preference, response.Code, response.Body.String())
		}
	}
}

func TestOfficialAgentCapabilitiesRemainAvailable(t *testing.T) {
	fixture := newCapabilityAPIFixture(t, agentcontrol.OfficialImplementation, []string{
		agentcontrol.CapabilityProxyVLESSACME,
		agentcontrol.CapabilityProxyVLESSManual,
		agentcontrol.CapabilityProxyVLESSReality,
		agentcontrol.CapabilityProxyShadowsocks,
		agentcontrol.CapabilityRelayRealm,
		agentcontrol.CapabilityOutboundPreference,
	})
	for index, body := range []createProxyRequest{
		realityProxyRequest(443), acmeProxyRequest(8443), manualTLSProxyRequest(t, 9443), shadowsocksProxyRequest(8388),
	} {
		if response := fixture.createProxy(t, body); response.Code != http.StatusCreated {
			t.Fatalf("official proxy %d = %d, %s", index, response.Code, response.Body.String())
		}
	}
	relay := createRelayRequest{ServerID: fixture.serverID, Name: "Relay", ListenPort: 9502, TargetType: "manual", TargetHost: "example.com", TargetPort: 443, Network: "tcp"}
	if response := performRequest(t, fixture.handler, http.MethodPost, "/api/relays", relay, fixture.cookie); response.Code != http.StatusCreated {
		t.Fatalf("official Relay = %d, %s", response.Code, response.Body.String())
	}
	path := "/api/servers/" + strconv.FormatInt(fixture.serverID, 10)
	if response := performRequest(t, fixture.handler, http.MethodPatch, path, map[string]string{"outbound_preference": "prefer_ipv4"}, fixture.cookie); response.Code != http.StatusOK {
		t.Fatalf("official outbound preference = %d, %s", response.Code, response.Body.String())
	}
}

func setAgentCapabilities(t *testing.T, db *sql.DB, serverID int64, implementation string, capabilities []string) {
	t.Helper()
	encoded, err := agentcontrol.EncodeCapabilities(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET implementation = ?, api_version = ?, capabilities_json = ? WHERE server_id = ?`, implementation, agentcontrol.CurrentAPIVersion, encoded, serverID); err != nil {
		t.Fatal(err)
	}
}

func apiTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "manual.example.com"}, DNSNames: []string{"manual.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(certificate), string(privateKey)
}
