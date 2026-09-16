package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderManagedXrayTLSWithMultipleClients(t *testing.T) {
	proxy := testDesiredTLSProxy()
	proxy.Clients = append(proxy.Clients, desiredClient{ID: 2, StatsID: "vp-client-2", UUID: "123e4567-e89b-42d3-a456-426614174001"})
	value, err := renderManagedXrayConfig([]desiredProxy{proxy})
	if err != nil {
		t.Fatal(err)
	}
	var config renderedXrayConfig
	if err := json.Unmarshal(value, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 1 || len(config.Inbounds[0].Settings.Clients) != 2 ||
		config.Inbounds[0].Settings.Clients[0].Flow != "xtls-rprx-vision" ||
		config.Inbounds[0].StreamSettings.Network != "tcp" ||
		config.Inbounds[0].StreamSettings.TLSSettings == nil ||
		config.Inbounds[0].StreamSettings.RealitySettings != nil {
		t.Fatalf("rendered TLS config = %+v", config)
	}
	if config.Inbounds[0].Settings.Clients[0].Email != "vp-client-1" ||
		config.Inbounds[0].Settings.Clients[1].Email != "vp-client-2" ||
		config.API.Listen != managedXrayStatsAPIAddress || len(config.API.Services) != 1 ||
		config.API.Services[0] != "StatsService" || !config.Policy.Levels["0"].StatsUserUplink ||
		!config.Policy.Levels["0"].StatsUserDownlink || !strings.Contains(string(value), `"stats": {}`) {
		t.Fatalf("rendered client stats config = %+v", config)
	}
	if strings.Contains(string(value), "udp443") {
		t.Fatal("server-side config contains client UDP/443 flow")
	}
}

func TestRenderManagedXrayACMEUsesCertificateFilesOnly(t *testing.T) {
	proxy := testDesiredTLSProxy()
	proxy.ServerName = "jp.example.com"
	proxy.TLS = &desiredTLS{Mode: "acme"}
	value, err := renderManagedXrayConfig([]desiredProxy{proxy})
	if err != nil {
		t.Fatal(err)
	}
	var config renderedXrayConfig
	if err := json.Unmarshal(value, &config); err != nil {
		t.Fatal(err)
	}
	cert := config.Inbounds[0].StreamSettings.TLSSettings.Certificates[0]
	if cert.CertificateFile != "/etc/vps-panel/xray/certs/jp.example.com/fullchain.pem" ||
		cert.KeyFile != "/etc/vps-panel/xray/certs/jp.example.com/private.key" ||
		len(cert.Certificate) != 0 || len(cert.Key) != 0 ||
		strings.Contains(string(value), `"certificate":`) || strings.Contains(string(value), `"key":`) {
		t.Fatalf("ACME Xray config contains inline PEM or wrong files: %s", value)
	}
	proxy.ServerName = "../unsafe.example.com"
	if _, err := renderManagedXrayConfig([]desiredProxy{proxy}); !errors.Is(err, errUnsupportedManagedConfig) {
		t.Fatalf("unsafe ACME desired domain error = %v", err)
	}
}

func TestRenderManagedXrayRealityAndMultipleInbounds(t *testing.T) {
	reality := desiredProxy{ID: 2, Listen: "0.0.0.0", Port: 8443, Protocol: "vless", Transport: "tcp", Security: "reality", ServerFlow: "xtls-rprx-vision", ServerName: "www.example.com", Reality: &desiredReality{Target: "www.example.com:443", PrivateKey: "private", ShortID: "0123456789abcdef"}, Clients: []desiredClient{{ID: 3, StatsID: "vp-client-3", UUID: "123e4567-e89b-42d3-a456-426614174002"}}}
	value, err := renderManagedXrayConfig([]desiredProxy{testDesiredTLSProxy(), reality})
	if err != nil {
		t.Fatal(err)
	}
	var config renderedXrayConfig
	if err := json.Unmarshal(value, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 2 || config.Inbounds[1].StreamSettings.RealitySettings == nil ||
		config.Inbounds[1].StreamSettings.Network != "raw" ||
		config.Inbounds[1].Settings.Clients[0].Flow != "xtls-rprx-vision" ||
		config.Inbounds[1].StreamSettings.RealitySettings.Target != "www.example.com:443" ||
		config.Inbounds[1].StreamSettings.TLSSettings != nil {
		t.Fatalf("rendered REALITY config = %+v", config)
	}
}

func TestRenderManagedXrayRejectsInvalidSemantics(t *testing.T) {
	tests := []desiredProxy{
		{Protocol: "trojan"},
		func() desiredProxy { value := testDesiredTLSProxy(); value.Transport = "ws"; return value }(),
		func() desiredProxy { value := testDesiredTLSProxy(); value.Security = "none"; return value }(),
		func() desiredProxy {
			value := testDesiredTLSProxy()
			value.Clients[0].UUID = "not-a-uuid"
			return value
		}(),
		func() desiredProxy {
			value := testDesiredTLSProxy()
			value.Clients[0].StatsID = "client-secret"
			return value
		}(),
		func() desiredProxy { value := testDesiredTLSProxy(); value.TLS = nil; return value }(),
	}
	for _, value := range tests {
		if _, err := renderManagedXrayConfig([]desiredProxy{value}); !errors.Is(err, errUnsupportedManagedConfig) {
			t.Fatalf("invalid proxy error = %v for %+v", err, value)
		}
	}
}

func TestRenderManagedXrayShadowsocks2022AndZeroClients(t *testing.T) {
	ss128 := testDesiredShadowsocksProxy(3, 8388, "2022-blake3-aes-128-gcm", 16)
	ss128.Clients = append(ss128.Clients, desiredClient{
		ID: 2, StatsID: "vp-client-2", Password: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16)),
	})
	ss256 := testDesiredShadowsocksProxy(4, 8389, "2022-blake3-aes-256-gcm", 32)
	empty := testDesiredShadowsocksProxy(5, 8390, "2022-blake3-aes-128-gcm", 16)
	empty.Clients = nil
	value, err := renderManagedXrayConfig([]desiredProxy{testDesiredTLSProxy(), ss128, ss256, empty})
	if err != nil {
		t.Fatal(err)
	}
	var config renderedXrayConfig
	if err := json.Unmarshal(value, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 3 {
		t.Fatalf("rendered inbound count = %d, config = %s", len(config.Inbounds), value)
	}
	for index, expected := range []desiredProxy{ss128, ss256} {
		inbound := config.Inbounds[index+1]
		if inbound.Protocol != "shadowsocks" || inbound.StreamSettings != nil ||
			inbound.Settings.Method != expected.Shadowsocks.Method || inbound.Settings.Password != expected.Shadowsocks.Password ||
			inbound.Settings.Network != "tcp,udp" || inbound.Settings.Decryption != "" || len(inbound.Settings.Clients) != len(expected.Clients) ||
			inbound.Settings.Clients[0].Password != expected.Clients[0].Password || inbound.Settings.Clients[0].Email != "vp-client-1" ||
			inbound.Settings.Clients[0].ID != "" || inbound.Settings.Clients[0].Flow != "" {
			t.Fatalf("rendered Shadowsocks inbound = %+v", inbound)
		}
		if len(expected.Clients) == 2 && (inbound.Settings.Clients[1].Email != "vp-client-2" ||
			inbound.Settings.Clients[1].Email == inbound.Settings.Clients[1].Password) {
			t.Fatalf("second Shadowsocks client stats identifier = %+v", inbound.Settings.Clients[1])
		}
	}
	if strings.Contains(string(value), "8390") {
		t.Fatal("zero-client Shadowsocks inbound was rendered")
	}
}

func TestRenderManagedXrayRejectsInvalidShadowsocksSecrets(t *testing.T) {
	tests := []desiredProxy{
		func() desiredProxy {
			value := testDesiredShadowsocksProxy(1, 8388, "2022-blake3-aes-128-gcm", 16)
			value.Shadowsocks.Method = "aes-128-gcm"
			return value
		}(),
		func() desiredProxy {
			value := testDesiredShadowsocksProxy(1, 8388, "2022-blake3-aes-128-gcm", 16)
			value.Shadowsocks.Network = "tcp"
			return value
		}(),
		func() desiredProxy {
			value := testDesiredShadowsocksProxy(1, 8388, "2022-blake3-aes-128-gcm", 16)
			value.Shadowsocks.Password = "bad"
			return value
		}(),
		func() desiredProxy {
			value := testDesiredShadowsocksProxy(1, 8388, "2022-blake3-aes-128-gcm", 16)
			value.Clients[0].Password = base64.StdEncoding.EncodeToString(make([]byte, 32))
			return value
		}(),
	}
	for _, value := range tests {
		if _, err := renderManagedXrayConfig([]desiredProxy{value}); !errors.Is(err, errUnsupportedManagedConfig) {
			t.Fatalf("invalid Shadowsocks desired state error = %v for %+v", err, value)
		}
	}
}

func TestRenderedConfigAcceptedByPinnedXrayWhenAvailable(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Skip("XRAY_TEST_BINARY is not set")
	}
	certificate, privateKey := renderTestCertificate(t)
	tlsProxy := testDesiredTLSProxy()
	tlsProxy.TLS = &desiredTLS{Certificate: certificate, PrivateKey: privateKey}
	realityPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	reality := desiredProxy{
		ID: 2, Listen: "0.0.0.0", Port: 8443, Protocol: "vless", Transport: "tcp",
		Security: "reality", ServerFlow: "xtls-rprx-vision", ServerName: "www.example.com",
		Reality: &desiredReality{
			Target:     "www.example.com:443",
			PrivateKey: base64.RawURLEncoding.EncodeToString(realityPrivate.Bytes()),
			ShortID:    "0123456789abcdef",
		},
		Clients: []desiredClient{{ID: 2, StatsID: "vp-client-2", UUID: "123e4567-e89b-42d3-a456-426614174002"}},
	}
	ss128 := testDesiredShadowsocksProxy(3, 8388, "2022-blake3-aes-128-gcm", 16)
	ss128.Clients = append(ss128.Clients, desiredClient{
		ID: 5, StatsID: "vp-client-5", Password: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{10}, 16)),
	})
	ss256 := testDesiredShadowsocksProxy(4, 8389, "2022-blake3-aes-256-gcm", 32)
	for _, candidate := range []struct {
		name    string
		proxies []desiredProxy
	}{
		{"vless-regression", []desiredProxy{tlsProxy, reality}},
		{"shadowsocks-128", []desiredProxy{ss128}},
		{"shadowsocks-256", []desiredProxy{ss256}},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			value, err := renderManagedXrayConfig(candidate.proxies)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, value, 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(binary, "run", "-test", "-config", path).CombinedOutput()
			if err != nil {
				t.Fatalf("pinned Xray rejected rendered config: %v: %s", err, output)
			}
		})
	}
}

func testDesiredTLSProxy() desiredProxy {
	return desiredProxy{ID: 1, Listen: "0.0.0.0", Port: 443, Protocol: "vless", Transport: "tcp", Security: "tls", ServerFlow: "xtls-rprx-vision", ServerName: "example.com", TLS: &desiredTLS{Certificate: "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", PrivateKey: "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----"}, Clients: []desiredClient{{ID: 1, StatsID: "vp-client-1", UUID: "123e4567-e89b-42d3-a456-426614174000"}}}
}

func testDesiredShadowsocksProxy(id int64, port int, method string, keyLength int) desiredProxy {
	return desiredProxy{
		ID: id, Listen: "0.0.0.0", Port: port, Protocol: "shadowsocks",
		Shadowsocks: &desiredShadowsocks{
			Method: method, Network: "tcp,udp",
			Password: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(id)}, keyLength)),
		},
		Clients: []desiredClient{{
			ID: 1, StatsID: "vp-client-1", Password: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(id + 1)}, keyLength)),
		}},
	}
}

func renderTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}
