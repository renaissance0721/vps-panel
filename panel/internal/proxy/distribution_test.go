package proxy

import (
	"errors"
	"testing"
)

func TestDistributableProxiesUseImmutableServerCreatorRole(t *testing.T) {
	db, service, adminServerID := newTestService(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES
			(1, 'admin', 'hash', 'admin', 1, 1),
			(2, 'vip', 'hash', 'vip', 1, 1),
			(3, 'member', 'hash', 'carpool', 1, 1)`,
		`UPDATE servers SET visibility = 'private', owner_user_id = 2 WHERE id = 1`,
		`INSERT INTO servers (id, name, owner_user_id, created_by_user_id, created_by_role, status, visibility, created_at, updated_at)
			VALUES (2, 'VIP Public', 1, 2, 'vip', 'offline', 'public', 1, 1)`,
		`INSERT INTO servers (id, name, owner_user_id, created_by_role, status, visibility, created_at, updated_at)
			VALUES (3, 'Legacy Unknown', 1, 'unknown', 'offline', 'public', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	adminProxy := createRealityProxy(t, service, adminServerID, 8443, "Admin Private")
	vipProxy := createRealityProxy(t, service, 2, 8444, "VIP Public")
	unknownProxy := createRealityProxy(t, service, 3, 8445, "Unknown")

	values, err := service.ListDistributable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].ID != adminProxy.ID {
		t.Fatalf("distributable proxies = %+v, want only admin private proxy %d", values, adminProxy.ID)
	}
	if _, _, err := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
		UserID: 3, ProxyID: adminProxy.ID, Name: "allowed", Enabled: true,
	}); err != nil {
		t.Fatalf("admin-created private proxy assignment error = %v", err)
	}
	for _, proxyID := range []int64{vipProxy.ID, unknownProxy.ID} {
		if _, _, err := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
			UserID: 3, ProxyID: proxyID, Name: "blocked", Enabled: true,
		}); !errors.Is(err, ErrNotDistributable) {
			t.Fatalf("proxy %d assignment error = %v, want ErrNotDistributable", proxyID, err)
		}
	}
}
