package main

import (
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
	proxy.Clients = append(proxy.Clients, desiredClient{ID: 2, UUID: "123e4567-e89b-42d3-a456-426614174001"})
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
		config.Inbounds[0].StreamSettings.TLSSettings == nil ||
		config.Inbounds[0].StreamSettings.RealitySettings != nil {
		t.Fatalf("rendered TLS config = %+v", config)
	}
	if strings.Contains(string(value), "udp443") {
		t.Fatal("server-side config contains client UDP/443 flow")
	}
}

func TestRenderManagedXrayRealityAndMultipleInbounds(t *testing.T) {
	reality := desiredProxy{ID: 2, Listen: "0.0.0.0", Port: 8443, Protocol: "vless", Transport: "tcp", Security: "reality", ServerFlow: "xtls-rprx-vision", ServerName: "www.example.com", Reality: &desiredReality{Target: "www.example.com:443", PrivateKey: "private", ShortID: "0123456789abcdef"}, Clients: []desiredClient{{ID: 3, UUID: "123e4567-e89b-42d3-a456-426614174002"}}}
	value, err := renderManagedXrayConfig([]desiredProxy{testDesiredTLSProxy(), reality})
	if err != nil {
		t.Fatal(err)
	}
	var config renderedXrayConfig
	if err := json.Unmarshal(value, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 2 || config.Inbounds[1].StreamSettings.RealitySettings == nil ||
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
		func() desiredProxy { value := testDesiredTLSProxy(); value.TLS = nil; return value }(),
	}
	for _, value := range tests {
		if _, err := renderManagedXrayConfig([]desiredProxy{value}); !errors.Is(err, errUnsupportedManagedConfig) {
			t.Fatalf("invalid proxy error = %v for %+v", err, value)
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
		Clients: []desiredClient{{ID: 2, UUID: "123e4567-e89b-42d3-a456-426614174002"}},
	}
	value, err := renderManagedXrayConfig([]desiredProxy{tlsProxy, reality})
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
}

func testDesiredTLSProxy() desiredProxy {
	return desiredProxy{ID: 1, Listen: "0.0.0.0", Port: 443, Protocol: "vless", Transport: "tcp", Security: "tls", ServerFlow: "xtls-rprx-vision", ServerName: "example.com", TLS: &desiredTLS{Certificate: "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", PrivateKey: "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----"}, Clients: []desiredClient{{ID: 1, UUID: "123e4567-e89b-42d3-a456-426614174000"}}}
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
