package landing

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/noderole"
)

func TestLandingNodeRolesAndOwnership(t *testing.T) {
	service, owner, other := newLandingTestService(t)
	for _, uri := range []string{"vless://uuid@example.com:443?type=tcp", "ss://aes-256-gcm:password@example.com:8388"} {
		for _, role := range []string{"", noderole.Direct, noderole.Landing} {
			value, err := service.Create(t.Context(), owner, CreateInput{URI: uri, NodeRole: role, Visibility: VisibilityPublic})
			if err != nil {
				t.Fatal(err)
			}
			want := role
			if want == "" {
				want = noderole.Direct
			}
			if value.NodeRole != want {
				t.Fatalf("create role = %q", value.NodeRole)
			}
			for _, updatedRole := range []string{noderole.Landing, noderole.Direct} {
				updated, endpointChanged, err := service.Update(t.Context(), value.ID, owner, UpdateInput{NodeRole: &updatedRole})
				if err != nil || endpointChanged || updated.NodeRole != updatedRole || updated.Host != value.Host || updated.Port != value.Port || updated.Protocol != value.Protocol {
					t.Fatalf("role update changed endpoint: %v", err)
				}
				raw, err := service.GetURI(t.Context(), value.ID, owner)
				if err != nil || raw != uri {
					t.Fatalf("role update changed URI: %v", err)
				}
			}
			landingRole := noderole.Landing
			if _, _, err := service.Update(t.Context(), value.ID, other, UpdateInput{NodeRole: &landingRole}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("non-owner role update: %v", err)
			}
			for _, invalid := range []string{"", "invalid", "DIRECT"} {
				if _, _, err := service.Update(t.Context(), value.ID, owner, UpdateInput{NodeRole: &invalid}); !errors.Is(err, noderole.ErrInvalid) {
					t.Fatalf("invalid update role: %v", err)
				}
			}
		}
		if _, err := service.Create(t.Context(), owner, CreateInput{URI: uri, NodeRole: "invalid"}); !errors.Is(err, noderole.ErrInvalid) {
			t.Fatalf("invalid import role: %v", err)
		}
	}
	values, err := service.List(t.Context(), other)
	if err != nil || len(values) != 6 {
		t.Fatalf("List = %d, %v", len(values), err)
	}
	for _, value := range values {
		if value.NodeRole != noderole.Direct || value.OwnedByMe {
			t.Fatal("List did not preserve role/ownership")
		}
	}
}
