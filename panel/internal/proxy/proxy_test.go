package proxy

import (
	"errors"
	"strings"
	"testing"
)

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
