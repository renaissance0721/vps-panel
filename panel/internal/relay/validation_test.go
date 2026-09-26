package relay

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestRelayProtocolAwarePortConflicts(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "203.0.113.1")
	if _, err := db.Exec(`INSERT INTO proxies
		(server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		VALUES (1, 'TCP Proxy', 'vless', 443, '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	udp, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "UDP same port", ListenPort: 443, TargetType: TargetManual,
		TargetHost: "1.1.1.1", TargetPort: 53, Network: NetworkUDP, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UDP should coexist with TCP: %v", err)
	}
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: 1, Name: "TCP conflict", ListenPort: 443, TargetType: TargetManual,
		TargetHost: "example.com", TargetPort: 443, Network: NetworkTCP, Enabled: true,
	}); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("TCP conflict error = %v", err)
	}
	if err := ProxyPortAvailable(t.Context(), db, 1, 443, "vless"); err != nil {
		t.Fatalf("TCP Proxy should coexist with UDP Relay: %v", err)
	}
	if err := ProxyPortAvailable(t.Context(), db, 1, 443, "shadowsocks"); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("TCP+UDP Proxy conflict error = %v", err)
	}
	if udp.Network != NetworkUDP {
		t.Fatalf("created UDP relay = %+v", udp)
	}
}

func TestRelayValidationRejectsUnsafeValues(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "Source", "")
	service := NewService(db)
	tests := []struct {
		input CreateInput
		want  error
	}{
		{CreateInput{ServerID: 1, Name: "", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidName},
		{CreateInput{ServerID: 1, Name: "x", ListenAddress: "$(bad)", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidListenIP},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 0, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidPort},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, EntryHostMode: "guess", TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidEntryHostMode},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, EntryHostMode: EntryHostManual, EntryHost: "https://bad", TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: NetworkTCP}, ErrInvalidEntryHost},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, TargetType: TargetManual, TargetHost: "https://bad", TargetPort: 1, Network: NetworkTCP}, ErrInvalidTarget},
		{CreateInput{ServerID: 1, Name: "x", ListenPort: 1, TargetType: TargetManual, TargetHost: "a.com", TargetPort: 1, Network: "icmp"}, ErrInvalidNetwork},
	}
	for _, test := range tests {
		if _, _, err := service.Create(t.Context(), test.input); !errors.Is(err, test.want) {
			t.Fatalf("Create(%+v) error = %v, want %v", test.input, err, test.want)
		}
	}
}

func TestRelayValidationAcceptsIPv4AndIPv6ListenAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0", "::"} {
		value, err := normalizeCreate(CreateInput{
			ServerID: 1, Name: "Relay", ListenAddress: address, ListenPort: 31821,
			TargetType: TargetManual, TargetHost: "example.com", TargetPort: 443,
			Network: NetworkTCP, Enabled: true,
		})
		if err != nil || value.ListenAddress != address {
			t.Fatalf("normalize listen address %q = %+v, %v", address, value, err)
		}
	}
}
