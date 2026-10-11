package proxy

import (
	"errors"
	"reflect"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/noderole"
)

func TestProxyNodeRolesAndRuntimeIsolation(t *testing.T) {
	for _, protocol := range []string{ProtocolVLESS, ProtocolShadowsocks} {
		t.Run(protocol, func(t *testing.T) {
			db, service, serverID := newTestService(t)
			input := CreateInput{ServerID: serverID, Name: "node", Protocol: protocol, ListenPort: 443,
				EntryHostMode: EntryHostManual, EntryHost: "node.example.com", Enabled: true,
				Security: SecurityReality, ServerName: "example.com", RealityTarget: "example.com:443", FirstClientName: "default"}
			for index, role := range []string{"", noderole.Direct, noderole.Landing} {
				input.NodeRole, input.ListenPort = role, 443+index
				value, _, err := service.Create(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				want := role
				if want == "" {
					want = noderole.Direct
				}
				got, err := service.Get(t.Context(), value.ID)
				if err != nil || got.NodeRole != want {
					t.Fatalf("Get role = %q, %v", got.NodeRole, err)
				}
			}
			values, err := service.List(t.Context())
			if err != nil || len(values) != 3 || values[0].NodeRole != noderole.Landing {
				t.Fatalf("List roles: %v", err)
			}
			id := values[0].ID
			before, err := ListDesired(t.Context(), db, serverID)
			if err != nil {
				t.Fatal(err)
			}
			var version, operations int64
			if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM configuration_operations`).Scan(&operations); err != nil {
				t.Fatal(err)
			}
			var originalConfig, originalCredential string
			if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, id).Scan(&originalConfig); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT credential_json FROM clients WHERE proxy_id = ?`, id).Scan(&originalCredential); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{noderole.Direct, noderole.Landing, noderole.Direct} {
				updated, mutation, err := service.Update(t.Context(), id, UpdateInput{NodeRole: &role})
				if err != nil || updated.NodeRole != role || mutation.Version != 0 {
					t.Fatalf("role update = %q, version %d, %v", updated.NodeRole, mutation.Version, err)
				}
			}
			// The edit form submits unchanged runtime fields alongside node_role.
			current, err := service.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			role := noderole.Landing
			_, mutation, err := service.Update(t.Context(), id, UpdateInput{NodeRole: &role, Name: &current.Name,
				ListenPort: &current.ListenPort, ListenFamily: &current.ListenFamily, EntryHostMode: &current.EntryHostMode,
				EntryHost: &current.EntryHost, Enabled: &current.Enabled})
			if err != nil || mutation.Version != 0 {
				t.Fatalf("full form role update version = %d, %v", mutation.Version, err)
			}
			after, err := ListDesired(t.Context(), db, serverID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("role changed desired config: %v", err)
			}
			var gotVersion, gotOperations int64
			if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&gotVersion); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM configuration_operations`).Scan(&gotOperations); err != nil {
				t.Fatal(err)
			}
			var config, credential string
			if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, id).Scan(&config); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT credential_json FROM clients WHERE proxy_id = ?`, id).Scan(&credential); err != nil {
				t.Fatal(err)
			}
			if gotVersion != version || gotOperations != operations || config != originalConfig || credential != originalCredential {
				t.Fatal("role update changed runtime state")
			}
			for _, invalid := range []string{"", "invalid", "DIRECT"} {
				if _, _, err := service.Update(t.Context(), id, UpdateInput{NodeRole: &invalid}); !errors.Is(err, noderole.ErrInvalid) {
					t.Fatalf("invalid update accepted: %q, %v", invalid, err)
				}
			}
			input.NodeRole, input.ListenPort = "invalid", 500
			if _, _, err := service.Create(t.Context(), input); !errors.Is(err, noderole.ErrInvalid) {
				t.Fatalf("invalid create: %v", err)
			}
			port := 8443
			if _, mutation, err := service.Update(t.Context(), id, UpdateInput{ListenPort: &port}); err != nil || mutation.Version != version+1 {
				t.Fatalf("runtime update version = %d, %v", mutation.Version, err)
			}
		})
	}
}

func TestProxyRoleUpdateWithUnavailableIPv6(t *testing.T) {
	db, service, serverID := newTestService(t)
	value, created, err := service.Create(t.Context(), CreateInput{ServerID: serverID, Name: "IPv6", ListenFamily: ListenFamilyIPv6,
		ListenPort: 443, EntryHostMode: EntryHostManual, EntryHost: "v6.example.com", Enabled: true,
		Security: SecurityReality, ServerName: "example.com", RealityTarget: "example.com:443", FirstClientName: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, public_ipv6, agent_version, reported_at)
		VALUES (?, '', '', '', '', '', '[]', '[]', '', '', '', 1)`, serverID); err != nil {
		t.Fatal(err)
	}
	role := noderole.Landing
	_, mutation, err := service.Update(t.Context(), value.ID, UpdateInput{NodeRole: &role, Name: &value.Name,
		ListenFamily: &value.ListenFamily, ListenPort: &value.ListenPort, EntryHostMode: &value.EntryHostMode,
		EntryHost: &value.EntryHost, Enabled: &value.Enabled})
	if err != nil || mutation.Version != 0 {
		t.Fatalf("metadata update with unavailable IPv6: version %d, %v", mutation.Version, err)
	}
	var version int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil || version != created.Version {
		t.Fatalf("role update changed desired version: %d, %v", version, err)
	}
	port := 8443
	if _, _, err := service.Update(t.Context(), value.ID, UpdateInput{ListenPort: &port}); !errors.Is(err, ErrIPv6Unavailable) {
		t.Fatalf("runtime change bypassed IPv6 validation: %v", err)
	}
}
