package proxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

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
				value.Config.Security != "" || len(value.Clients) != 1 ||
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
