package proxy

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

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
