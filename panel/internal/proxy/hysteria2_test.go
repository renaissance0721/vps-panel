package proxy

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/listener"
)

func TestManagedKernelProtocolCompatibility(t *testing.T) {
	for _, tc := range []struct {
		protocol, kernel string
		want             bool
	}{
		{ProtocolVLESS, KernelXray, true},
		{ProtocolShadowsocks, KernelXray, true},
		{ProtocolHysteria2, KernelSingbox, true},
		{ProtocolVLESS, KernelSingbox, false},
		{ProtocolShadowsocks, KernelSingbox, false},
		{ProtocolHysteria2, KernelXray, false},
		{"unknown", KernelXray, false},
	} {
		if got := supportsKernel(tc.protocol, tc.kernel); got != tc.want {
			t.Errorf("supportsKernel(%q, %q) = %v, want %v", tc.protocol, tc.kernel, got, tc.want)
		}
	}
	_, service, serverID := newTestService(t)
	input := CreateInput{ServerID: serverID, Name: "HY2", Protocol: ProtocolHysteria2, Kernel: KernelXray, ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "node.example.com", ServerName: "tls.example.com", TLSMode: TLSModeACME, FirstClientName: "first", Enabled: true}
	if _, _, err := service.Create(t.Context(), input); !errors.Is(err, ErrInvalidKernel) {
		t.Fatalf("unsupported kernel create error = %v", err)
	}
	input.Kernel = KernelSingbox
	created, _, err := service.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	wrongKernel := KernelXray
	if _, _, err := service.Update(t.Context(), created.ID, UpdateInput{Kernel: &wrongKernel}); !errors.Is(err, ErrInvalidKernel) {
		t.Fatalf("unsupported kernel migration error = %v", err)
	}
}

func TestHysteria2PortHoppingReservationsAndShare(t *testing.T) {
	db, service, serverID := newTestService(t)
	input := CreateInput{ServerID: serverID, Name: "HY2", Protocol: ProtocolHysteria2, Kernel: KernelSingbox, ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "node.example.com", ServerName: "tls.example.com", TLSMode: TLSModeACME, FirstClientName: "first", Enabled: true, PortHoppingEnabled: true, PortHoppingStart: 20000, PortHoppingEnd: 20100}
	first, _, err := service.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	share, err := service.GetClientShare(t.Context(), first.Clients[0].ID)
	if err != nil || !strings.Contains(share.URI, "@node.example.com:20000-20100/") || share.Hysteria2Ports != "20000-20100" {
		t.Fatalf("invalid hopping share: %v", err)
	}
	input.ListenPort = 8443
	input.PortHoppingStart, input.PortHoppingEnd = 20050, 20200
	if _, _, err := service.Create(t.Context(), input); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("overlapping hopping range error = %v", err)
	}
	input.PortHoppingEnabled = false
	input.ListenPort = 20005
	if _, _, err := service.Create(t.Context(), input); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("listener inside hopping range error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO relays(server_id,name,listen_address,listen_port,target_type,target_host,target_port,network,enabled,created_at,updated_at) VALUES (?, 'conflict','0.0.0.0',20020,'manual','example.com',443,'udp',1,1,1)`, serverID); err == nil {
		t.Fatal("relay inside hopping range accepted")
	}
	enabled := false
	if _, _, err := service.Update(t.Context(), first.ID, UpdateInput{PortHoppingEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	input.ListenPort = 20005
	if _, _, err := service.Create(t.Context(), input); err != nil {
		t.Fatalf("released hopping port unavailable: %v", err)
	}
}

func TestHysteria2HoppingURIEscapesCredentialsAndIPv6(t *testing.T) {
	share := ClientShare{Client: Client{Password: "p@ss:/?"}, Protocol: ProtocolHysteria2, Address: "2001:db8::1", Port: 443, Hysteria2Ports: "20000-30000", ServerName: "tls.example.com", ObfsPassword: "mix &/secret", DisplayName: "IPv6 node"}
	uri := buildHysteria2URI(share)
	if !strings.HasPrefix(uri, "hysteria2://p%40ss%3A%2F%3F@[2001:db8::1]:20000-30000/") || !strings.Contains(uri, "sni=tls.example.com") || !strings.Contains(uri, "obfs=salamander") || !strings.Contains(uri, "obfs-password=mix+%26%2Fsecret") {
		t.Fatal("hopping URI failed to encode IPv6 or credentials")
	}
}

func TestHysteria2ProxyClientsDesiredAndShare(t *testing.T) {
	db, service, serverID := newTestService(t)
	input := CreateInput{ServerID: serverID, Name: "HY2", Protocol: ProtocolHysteria2, Kernel: KernelSingbox, ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "node.example.com", ServerName: "tls.example.com", TLSMode: TLSModeACME, FirstClientName: "first", Enabled: true, UpMbps: 100, ObfsPassword: "mix &/密码"}
	value, _, err := service.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if value.Kernel != KernelSingbox || value.Config.UpMbps != 100 || !value.Config.ObfsEnabled {
		t.Fatalf("unexpected Hysteria2 proxy: %+v", value)
	}
	client, _, err := service.CreateClient(t.Context(), value.ID, ClientCreateInput{Name: "second", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.GetClient(t.Context(), value.Clients[0].ID)
	if err != nil || first.Password == "" || first.Password == client.Password {
		t.Fatal("client passwords were not generated independently")
	}
	limit := int64(1000)
	if _, _, err := service.CreateClient(t.Context(), value.ID, ClientCreateInput{Name: "quota", Enabled: true, Traffic: ClientTrafficConfig{LimitBytes: &limit}}); !errors.Is(err, ErrInvalidHysteria2) {
		t.Fatalf("quota accepted: %v", err)
	}
	if _, err := service.RecordClientTrafficWithMutation(t.Context(), serverID, []ClientTrafficReport{{ClientID: client.ID, UplinkBytes: 10}}); !errors.Is(err, ErrInvalidClientTraffic) {
		t.Fatalf("Hysteria2 traffic report accepted: %v", err)
	}
	if _, _, err := service.ResetClientTrafficWithMutation(t.Context(), client.ID); !errors.Is(err, ErrInvalidHysteria2) {
		t.Fatalf("Hysteria2 traffic reset accepted: %v", err)
	}
	duplicate := first.Password
	if _, _, err := service.UpdateClient(t.Context(), client.ID, ClientUpdateInput{Password: &duplicate}); err == nil {
		t.Fatal("duplicate password accepted")
	}
	password := "p@ss:/? 测试"
	client, _, err = service.UpdateClient(t.Context(), client.ID, ClientUpdateInput{Password: &password})
	if err != nil || client.Password != password {
		t.Fatalf("update client password: %v", err)
	}
	share, err := service.GetClientShare(t.Context(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(share.URI)
	if err != nil || u.Scheme != "hysteria2" || u.User.Username() != client.Password || u.Query().Get("sni") != input.ServerName || u.Query().Get("obfs-password") != input.ObfsPassword || u.Query().Get("obfs") != "salamander" || strings.Contains(share.URI, "insecure=") {
		t.Fatal("invalid Hysteria2 share URI")
	}
	up := 250
	updated, _, err := service.Update(t.Context(), value.ID, UpdateInput{UpMbps: &up})
	if err != nil || updated.Config.UpMbps != up {
		t.Fatalf("update Hysteria2 proxy: %v", err)
	}
	desired, err := ListDesired(t.Context(), db, serverID)
	if err != nil || len(desired) != 1 || desired[0].Kernel != KernelSingbox || desired[0].Hysteria2.UpMbps != up || len(desired[0].Clients) != 2 {
		t.Fatalf("Hysteria2 desired state: %v", err)
	}
	if _, err := service.DeleteClient(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	desired, err = ListDesired(t.Context(), db, serverID)
	if err != nil || len(desired) != 1 || len(desired[0].Clients) != 1 || desired[0].Clients[0].Password != first.Password {
		t.Fatalf("desired clients after deletion: %v", err)
	}
}

func TestHoppingUDPReservations(t *testing.T) {
	for _, tc := range []struct {
		name, network string
		conflict      bool
	}{{"tcp", "tcp", false}, {"udp", "udp", true}, {"both", "both", true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, service, serverID := newTestService(t)
			input := CreateInput{ServerID: serverID, Name: "HY2", Protocol: ProtocolHysteria2, Kernel: KernelSingbox, ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "node.example.com", ServerName: "tls.example.com", TLSMode: TLSModeACME, FirstClientName: "first", Enabled: true, PortHoppingEnabled: true, PortHoppingStart: 20000, PortHoppingEnd: 20100}
			if _, _, err := service.Create(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			err := listener.ListenerAvailable(t.Context(), db, serverID, 20001, tc.network, listener.AvailabilityOptions{})
			if errors.Is(err, listener.ErrConflict) != tc.conflict {
				t.Fatalf("availability = %v", err)
			}
			_, err = db.Exec(`INSERT INTO server_listener_reservations(server_id,resource_type,resource_id,port,network,created_at) VALUES (?, 'relay', 999, 20001, ?, 1)`, serverID, tc.network)
			if (err != nil) != tc.conflict {
				t.Fatalf("reservation = %v", err)
			}
		})
	}
	for _, bounds := range [][2]int{{1, 1024}, {1023, 1023}, {20000, 30000}, {30001, 30000}} {
		if err := validatePortHopping(&storedHysteria2{PortHoppingEnabled: true, PortHoppingStart: bounds[0], PortHoppingEnd: bounds[1]}, 443); !errors.Is(err, ErrInvalidPortHopping) {
			t.Fatalf("bounds %v = %v", bounds, err)
		}
	}
}

func TestHoppingWithExistingRealmNetwork(t *testing.T) {
	for _, tc := range []struct {
		network  string
		conflict bool
	}{{"tcp", false}, {"udp", true}, {"tcp,udp", true}} {
		t.Run(tc.network, func(t *testing.T) {
			db, service, serverID := newTestService(t)
			_, err := db.Exec(`INSERT INTO relays(server_id,name,listen_address,listen_port,target_type,target_host,target_port,network,enabled,created_at,updated_at) VALUES (?, 'realm','0.0.0.0',20001,'manual','example.com',443,?,1,1,1)`, serverID, tc.network)
			if err != nil {
				t.Fatal(err)
			}
			input := CreateInput{ServerID: serverID, Name: "HY2", Protocol: ProtocolHysteria2, Kernel: KernelSingbox, ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "node.example.com", ServerName: "tls.example.com", TLSMode: TLSModeACME, FirstClientName: "first", Enabled: true, PortHoppingEnabled: true, PortHoppingStart: 20000, PortHoppingEnd: 20100}
			_, _, err = service.Create(t.Context(), input)
			if errors.Is(err, ErrPortConflict) != tc.conflict {
				t.Fatalf("create with realm %s: %v", tc.network, err)
			}
		})
	}
}
