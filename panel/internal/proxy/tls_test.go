package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
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

func TestACMETLSStoresOnlyModeAndDomain(t *testing.T) {
	db, service, serverID := newTestService(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "ACME", ListenPort: 443, EntryHostMode: EntryHostAuto,
		Enabled: true, Security: SecurityTLS, TLSMode: TLSModeACME,
		ServerName: "jp.example.com", FirstClientName: "default",
	})
	if err != nil || value.Config.TLSMode != TLSModeACME || value.Config.TLSCertificateConfigured {
		t.Fatalf("create ACME TLS proxy = %+v, %v", value, err)
	}
	var configJSON string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, value.ID).Scan(&configJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(configJSON, "certificate") || strings.Contains(configJSON, "private_key") {
		t.Fatalf("ACME config stored PEM fields: %s", configJSON)
	}
	desired, err := ListDesired(t.Context(), db, serverID)
	if err != nil || len(desired) != 1 || desired[0].TLS.Mode != TLSModeACME ||
		desired[0].TLS.Certificate != "" || desired[0].TLS.PrivateKey != "" {
		t.Fatalf("ACME desired state = %+v, %v", desired, err)
	}
	for _, domain := range []string{"1.2.3.4", "2001:db8::1", "localhost", "../../etc/passwd", "example.local"} {
		_, _, err := service.Create(t.Context(), CreateInput{
			ServerID: serverID, Name: "invalid", ListenPort: 8443, EntryHostMode: EntryHostAuto,
			Security: SecurityTLS, TLSMode: TLSModeACME, ServerName: domain, FirstClientName: "default",
		})
		if !errors.Is(err, ErrInvalidACMEDomain) && !errors.Is(err, ErrInvalidServerName) {
			t.Fatalf("ACME domain %q error = %v", domain, err)
		}
	}
	_, _, err = service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "manual", ListenPort: 8444, EntryHostMode: EntryHostAuto,
		Security: SecurityTLS, TLSMode: TLSModeManual, ServerName: "manual.example.com", FirstClientName: "default",
	})
	if !errors.Is(err, ErrInvalidTLS) {
		t.Fatalf("manual TLS without PEM error = %v", err)
	}
}

func TestLegacyTLSWithoutModeRemainsManual(t *testing.T) {
	db, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "legacy", ListenPort: 443, EntryHostMode: EntryHostAuto,
		Enabled: true, Security: SecurityTLS, TLSMode: TLSModeManual, ServerName: "legacy.example.com",
		Certificate: certificate, PrivateKey: privateKey, FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE proxies SET config_json = REPLACE(config_json, '"mode":"manual",', '') WHERE id = ?`, value.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := service.Get(t.Context(), value.ID)
	if err != nil || legacy.Config.TLSMode != TLSModeManual || !legacy.Config.TLSCertificateConfigured {
		t.Fatalf("legacy TLS = %+v, %v", legacy, err)
	}
	desired, err := ListDesired(t.Context(), db, serverID)
	if err != nil || desired[0].TLS.Mode != TLSModeManual || desired[0].TLS.PrivateKey != strings.TrimSpace(privateKey) {
		t.Fatalf("legacy desired state = %+v, %v", desired, err)
	}
}

func TestSwitchManualTLSToACMERemovesStoredPEM(t *testing.T) {
	db, service, serverID := newTestService(t)
	certificate, privateKey := testCertificate(t)
	value, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "switch", ListenPort: 443, EntryHostMode: EntryHostAuto,
		Security: SecurityTLS, TLSMode: TLSModeManual, ServerName: "jp.example.com",
		Certificate: certificate, PrivateKey: privateKey, FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	mode := TLSModeACME
	updated, _, err := service.Update(t.Context(), value.ID, UpdateInput{TLSMode: &mode})
	if err != nil || updated.Config.TLSMode != TLSModeACME || updated.Config.TLSCertificateConfigured {
		t.Fatalf("switch to ACME = %+v, %v", updated, err)
	}
	var stored string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, value.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "certificate") || strings.Contains(stored, "private_key") || strings.Contains(stored, privateKey) {
		t.Fatalf("ACME mode retained manual PEM: %s", stored)
	}
	mode = TLSModeManual
	if _, _, err := service.Update(t.Context(), value.ID, UpdateInput{TLSMode: &mode}); !errors.Is(err, ErrInvalidTLS) {
		t.Fatalf("manual mode without PEM error = %v", err)
	}
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
