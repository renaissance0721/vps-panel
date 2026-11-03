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

func TestAssignClientSynchronizesCarpoolNodes(t *testing.T) {
	db, service, serverID := newTestService(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'alice','hash','carpool',1,1),(2,'bob','hash','carpool',1,1)`); err != nil {
		t.Fatal(err)
	}
	p := createRealityProxy(t, service, serverID, 443, "proxy")
	id := p.Clients[0].ID
	alice, bob := int64(1), int64(2)
	if _, err := service.AssignClient(t.Context(), id, &alice, nil); err != nil {
		t.Fatal(err)
	}
	var assignmentID int64
	if err := db.QueryRow(`SELECT id FROM carpool_node_assignments WHERE user_id=? AND client_id=? AND source_type='proxy' AND source_id=?`, alice, id, p.ID).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO carpool_node_assignments(user_id,client_id,source_type,source_id,created_at) VALUES(?,?,'relay',123,1)`, alice, id); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignClient(t.Context(), id, &bob, nil); err != nil {
		t.Fatal(err)
	}
	var oldCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM carpool_node_assignments WHERE user_id=? AND client_id=?`, alice, id).Scan(&oldCount); err != nil || oldCount != 0 {
		t.Fatalf("old assignments=%d err=%v", oldCount, err)
	}
	if err := db.QueryRow(`SELECT id FROM carpool_node_assignments WHERE user_id=? AND client_id=? AND source_type='proxy' AND source_id=?`, bob, id, p.ID).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM carpool_node_assignments WHERE id=?`, assignmentID); err != nil {
		t.Fatal(err)
	}
	billing := 1
	if _, err := service.AssignClient(t.Context(), id, &bob, &billing); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM carpool_node_assignments WHERE user_id=?`, bob).Scan(&count); err != nil || count != 0 {
		t.Fatalf("same-user edit resurrected deleted node: %d %v", count, err)
	}
}
