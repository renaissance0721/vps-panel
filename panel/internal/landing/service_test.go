package landing

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func newLandingTestService(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, username := range []string{"owner", "other"} {
		if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, 'hash', 'vip', 1, 1)`, username); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(db), 1, 2
}

func TestLandingServiceCreateDefaultsAndAccess(t *testing.T) {
	service, ownerID, otherID := newLandingTestService(t)
	private, err := service.Create(t.Context(), ownerID, CreateInput{
		URI: "vless://uuid@example.com:443?type=tcp#US%20Home",
	})
	if err != nil {
		t.Fatal(err)
	}
	if private.Name != "US Home" || private.Visibility != VisibilityPrivate || private.Protocol != ProtocolVLESS ||
		private.Host != "example.com" || private.Port != 443 || !private.OwnedByMe {
		t.Fatalf("created private landing = %+v", private)
	}
	if _, err := service.Get(t.Context(), private.ID, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user private Get error = %v", err)
	}
	public, err := service.Create(t.Context(), ownerID, CreateInput{
		Visibility: VisibilityPublic,
		URI:        "ss://aes-256-gcm:password@1.2.3.4:8388",
	})
	if err != nil {
		t.Fatal(err)
	}
	if public.Name != "Shadowsocks 外部节点" || public.Visibility != VisibilityPublic || public.Protocol != ProtocolSS {
		t.Fatalf("created public landing = %+v", public)
	}
	visible, err := service.Get(t.Context(), public.ID, otherID)
	if err != nil || visible.OwnedByMe {
		t.Fatalf("other user public Get = %+v, %v", visible, err)
	}
	list, err := service.List(t.Context(), otherID)
	if err != nil || len(list) != 1 || list[0].ID != public.ID {
		t.Fatalf("other user landing list = %+v, %v", list, err)
	}
	name := "Nope"
	if _, _, err := service.Update(t.Context(), public.ID, otherID, UpdateInput{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner update error = %v", err)
	}
	if err := service.Delete(t.Context(), public.ID, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner delete error = %v", err)
	}
}

func TestLandingServiceCreateNameSelection(t *testing.T) {
	service, ownerID, _ := newLandingTestService(t)
	tests := []struct {
		name     string
		input    CreateInput
		expected string
	}{
		{"plain fragment", CreateInput{URI: "vless://uuid@example.com:443#US-LAX"}, "US-LAX"},
		{"escaped fragment", CreateInput{URI: "vless://uuid@example.com:443#US%20Los%20Angeles"}, "US Los Angeles"},
		{"unicode Shadowsocks fragment", CreateInput{URI: "ss://aes-256-gcm:password@example.com:8388#%E6%96%B0%E5%8A%A0%E5%9D%A1%20%E5%85%B1%E4%BA%AB"}, "新加坡 共享"},
		{"VLESS fallback", CreateInput{URI: "vless://uuid@example.com:443"}, "VLESS 外部节点"},
		{"Shadowsocks fallback", CreateInput{URI: "ss://aes-256-gcm:password@example.com:8388"}, "Shadowsocks 外部节点"},
		{"explicit name wins", CreateInput{Name: "我的节点", URI: "vless://uuid@example.com:443#Ignored"}, "我的节点"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := service.Create(t.Context(), ownerID, test.input)
			if err != nil {
				t.Fatal(err)
			}
			if value.Name != test.expected {
				t.Fatalf("name = %q, want %q", value.Name, test.expected)
			}
		})
	}
}

func TestLandingServiceUpdateProtocolReferencesAndEndpointChanges(t *testing.T) {
	service, ownerID, _ := newLandingTestService(t)
	value, err := service.Create(t.Context(), ownerID, CreateInput{
		Visibility: VisibilityPublic,
		URI:        "vless://uuid-a@old.example.com:443?type=tcp&pbk=a#US",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialOnly := "vless://uuid-b@old.example.com:443?type=tcp&pbk=b#US"
	updated, endpointChanged, err := service.Update(t.Context(), value.ID, ownerID, UpdateInput{URI: &credentialOnly})
	if err != nil || endpointChanged || updated.Host != "old.example.com" || updated.Port != 443 {
		t.Fatalf("credential-only update = %+v, changed %v, error %v", updated, endpointChanged, err)
	}
	changedURI := "vless://uuid-b@new.example.com:8443?type=tcp&pbk=b#US"
	updated, endpointChanged, err = service.Update(t.Context(), value.ID, ownerID, UpdateInput{URI: &changedURI})
	if err != nil || !endpointChanged || updated.Host != "new.example.com" || updated.Port != 8443 {
		t.Fatalf("endpoint update = %+v, changed %v, error %v", updated, endpointChanged, err)
	}
	ssURI := "ss://aes-256-gcm:password@example.com:8388"
	if _, _, err := service.Update(t.Context(), value.ID, ownerID, UpdateInput{URI: &ssURI}); !errors.Is(err, ErrImmutableProtocol) {
		t.Fatalf("protocol change error = %v", err)
	}

	db := service.db
	if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'Source', 'offline', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_landing_id, network, enabled, created_at, updated_at)
		VALUES (1, 'Relay', '0.0.0.0', 9502, 'landing', ?, 'tcp', 1, 1, 1)`, value.ID); err != nil {
		t.Fatal(err)
	}
	private := VisibilityPrivate
	if _, _, err := service.Update(t.Context(), value.ID, ownerID, UpdateInput{Visibility: &private}); !errors.Is(err, ErrReferencedByRelay) {
		t.Fatalf("referenced public to private error = %v", err)
	}
	if err := service.Delete(t.Context(), value.ID, ownerID); !errors.Is(err, ErrReferencedByRelay) {
		t.Fatalf("referenced delete error = %v", err)
	}
	if _, err := db.Exec(`DELETE FROM relays WHERE target_landing_id = ?`, value.ID); err != nil {
		t.Fatal(err)
	}
	updated, _, err = service.Update(t.Context(), value.ID, ownerID, UpdateInput{Visibility: &private})
	if err != nil || updated.Visibility != VisibilityPrivate {
		t.Fatalf("unreferenced public to private = %+v, %v", updated, err)
	}
	if err := service.Delete(t.Context(), value.ID, ownerID); err != nil {
		t.Fatal(err)
	}
}
