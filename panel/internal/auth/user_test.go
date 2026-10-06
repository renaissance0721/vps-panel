package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestInitializeCreatesOnlyFirstUserWithHashedPassword(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	needsInitialization, err := service.NeedsInitialization(ctx)
	if err != nil || !needsInitialization {
		t.Fatalf("NeedsInitialization() = (%v, %v), want (true, nil)", needsInitialization, err)
	}

	user, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if user.Username != "admin" || user.Role != RoleAdmin {
		t.Fatalf("Initialize() user = %+v, want admin role", user)
	}

	var passwordHash, role string
	if err := db.QueryRow(`SELECT password_hash, role FROM users WHERE id = ?`, user.ID).Scan(&passwordHash, &role); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if passwordHash == testPassword || passwordHash == "" {
		t.Fatalf("password was not safely hashed")
	}
	if role != RoleAdmin {
		t.Fatalf("stored first user role = %q, want %q", role, RoleAdmin)
	}

	if _, err := service.Initialize(ctx, "second", testPassword); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Initialize() error = %v, want ErrAlreadyInitialized", err)
	}
	needsInitialization, err = service.NeedsInitialization(ctx)
	if err != nil || needsInitialization {
		t.Fatalf("NeedsInitialization() = (%v, %v), want (false, nil)", needsInitialization, err)
	}
}

func TestListUsersReturnsAccountsWithoutCredentials(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	admin, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterWithInvitation(ctx, invitation.Token, "member", testPassword); err != nil {
		t.Fatal(err)
	}
	users, err := service.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" || users[0].Role != RoleAdmin ||
		users[1].Username != "member" || users[1].Role != RoleVIP {
		t.Fatalf("ListUsers() = %+v", users)
	}
}

func TestDeleteUserResourceScenarios(t *testing.T) {
	t.Run("empty VIP and login", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleVIP)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		if err != nil || len(mutations) != 0 {
			t.Fatalf("DeleteUser() = (%+v, %v), want no mutations", mutations, err)
		}
		if _, err := service.Login(t.Context(), user.Username, testPassword); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login() after deletion error = %v, want ErrInvalidCredentials", err)
		}
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("VIP-owned server is preserved and unowned", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleVIP)
		insertDeletionServer(t, db, 10, user.ID)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		if err != nil || len(mutations) != 0 {
			t.Fatalf("DeleteUser() = (%+v, %v), want no mutations", mutations, err)
		}
		var owner sql.NullInt64
		var version int64
		if err := db.QueryRow(`SELECT owner_user_id, desired_state_version FROM servers WHERE id = 10`).Scan(&owner, &version); err != nil {
			t.Fatal(err)
		}
		if owner.Valid || version != 1 {
			t.Fatalf("preserved server = owner %+v version %d, want NULL/1", owner, version)
		}
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("assigned client and cascaded client data", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
		insertDeletionServer(t, db, 10, nil)
		insertDeletionProxy(t, db, 20, 10, 8443)
		insertDeletionClient(t, db, 30, 20, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO client_metrics
			(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_started_at, updated_at)
			VALUES (30, 1, 2, 1, 1)`)
		mustExecDeleteTest(t, db, `INSERT INTO client_relay_ports (client_id, server_id, port, created_at)
			VALUES (30, 10, 20000, 1)`)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		assertDeletionMutations(t, mutations, err, 10)
		assertTableCount(t, db, "clients", "id = ?", 0, int64(30))
		assertTableCount(t, db, "client_metrics", "client_id = ?", 0, int64(30))
		assertTableCount(t, db, "client_relay_ports", "client_id = ?", 0, int64(30))
		assertServerVersion(t, db, 10, 2)
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("owned relay", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
		insertDeletionServer(t, db, 10, nil)
		insertManualDeletionRelay(t, db, 40, 10, user.ID, nil)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		assertDeletionMutations(t, mutations, err, 10)
		assertTableCount(t, db, "relays", "id = ?", 0, int64(40))
		assertServerVersion(t, db, 10, 2)
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("landing referenced by another user's relay", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
		insertDeletionServer(t, db, 10, nil)
		insertDeletionLanding(t, db, 50, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO relays
			(id, server_id, name, listen_port, target_type, target_landing_id, network, created_at, updated_at)
			VALUES (40, 10, 'foreign landing relay', 9500, 'landing', 50, 'tcp', 1, 1)`)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		assertDeletionMutations(t, mutations, err, 10)
		assertTableCount(t, db, "relays", "id = ?", 0, int64(40))
		assertTableCount(t, db, "landing_nodes", "id = ?", 0, int64(50))
		assertNoForeignKeyViolations(t, db)
	})

	for _, reference := range []string{"source", "target"} {
		t.Run(reference+" client referenced by another user's relay", func(t *testing.T) {
			service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
			insertDeletionServer(t, db, 10, nil)
			insertDeletionServer(t, db, 11, nil)
			insertDeletionProxy(t, db, 20, 10, 8443)
			insertDeletionProxy(t, db, 21, 11, 8444)
			insertDeletionClient(t, db, 30, 20, user.ID)
			if reference == "source" {
				insertManualDeletionRelay(t, db, 40, 11, nil, int64(30))
			} else {
				mustExecDeleteTest(t, db, `INSERT INTO relays
					(id, server_id, target_client_id, name, listen_port, target_type,
					 target_proxy_id, network, created_at, updated_at)
					VALUES (40, 11, 30, 'target client relay', 9500, 'proxy', 21, 'tcp', 1, 1)`)
			}
			mutations, err := service.DeleteUser(t.Context(), user.ID)
			assertDeletionMutations(t, mutations, err, 10, 11)
			assertTableCount(t, db, "relays", "id = ?", 0, int64(40))
			assertTableCount(t, db, "clients", "id = ?", 0, int64(30))
			assertNoForeignKeyViolations(t, db)
		})
	}

	t.Run("subscriber profile usage and managed client", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleSubscriber)
		insertDeletionServer(t, db, 10, nil)
		insertDeletionProxy(t, db, 20, 10, 8443)
		insertDeletionClient(t, db, 30, 20, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO subscriber_clients
			(user_id, proxy_id, client_id, created_at) VALUES (?, 20, 30, 1)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO client_metrics
			(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_started_at, updated_at)
			VALUES (30, 5, 6, 1, 1)`)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		assertDeletionMutations(t, mutations, err, 10)
		for _, table := range []string{"subscriber_profiles", "subscriber_clients", "subscriber_usage"} {
			assertTableCount(t, db, table, "user_id = ?", 0, user.ID)
		}
		assertTableCount(t, db, "clients", "id = ?", 0, int64(30))
		assertTableCount(t, db, "client_metrics", "client_id = ?", 0, int64(30))
		assertServerVersion(t, db, 10, 2)
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("session password request invitation access and orders", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
		insertDeletionServer(t, db, 10, nil)
		insertDeletionProxy(t, db, 20, 10, 8443)
		insertManualDeletionRelay(t, db, 40, 10, nil, nil)
		mustExecDeleteTest(t, db, `INSERT INTO sessions
			(user_id, token_hash, expires_at, created_at) VALUES (?, 'delete-session', 9999999999, 1)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO password_change_requests
			(user_id, proposed_password_hash, status, created_at) VALUES (?, 'hash', 'pending', 1)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO admin_invitations
			(token_hash, created_by, expires_at, role, created_at) VALUES ('delete-invite', ?, 9999999999, 'vip', 1)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO server_access (server_id, user_id) VALUES (10, ?)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO user_server_order (user_id, server_id, position) VALUES (?, 10, 0)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO user_proxy_order (user_id, proxy_id, position) VALUES (?, 20, 0)`, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO user_relay_order (user_id, relay_id, position) VALUES (?, 40, 0)`, user.ID)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		if err != nil || len(mutations) != 0 {
			t.Fatalf("DeleteUser() = (%+v, %v), want no mutations", mutations, err)
		}
		for _, table := range []string{
			"sessions", "password_change_requests", "server_access", "user_server_order",
			"user_proxy_order", "user_relay_order",
		} {
			assertTableCount(t, db, table, "user_id = ?", 0, user.ID)
		}
		assertTableCount(t, db, "admin_invitations", "created_by = ?", 0, user.ID)
		assertTableCount(t, db, "relays", "id = ?", 1, int64(40))
		assertNoForeignKeyViolations(t, db)
	})

	t.Run("mixed resources on multiple servers bump once each", func(t *testing.T) {
		service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
		insertDeletionServer(t, db, 10, user.ID)
		insertDeletionServer(t, db, 11, nil)
		insertDeletionProxy(t, db, 20, 10, 8443)
		insertDeletionProxy(t, db, 21, 11, 8444)
		insertDeletionClient(t, db, 30, 20, user.ID)
		insertDeletionClient(t, db, 31, 21, user.ID)
		insertManualDeletionRelay(t, db, 40, 10, user.ID, int64(30))
		insertDeletionLanding(t, db, 50, user.ID)
		mustExecDeleteTest(t, db, `INSERT INTO relays
			(id, server_id, name, listen_port, target_type, target_landing_id, network, created_at, updated_at)
			VALUES (41, 11, 'mixed landing relay', 9501, 'landing', 50, 'tcp', 1, 1)`)
		mutations, err := service.DeleteUser(t.Context(), user.ID)
		assertDeletionMutations(t, mutations, err, 10, 11)
		assertServerVersion(t, db, 10, 2)
		assertServerVersion(t, db, 11, 2)
		var owner sql.NullInt64
		if err := db.QueryRow(`SELECT owner_user_id FROM servers WHERE id = 10`).Scan(&owner); err != nil || owner.Valid {
			t.Fatalf("server owner after mixed deletion = %+v, %v", owner, err)
		}
		assertNoForeignKeyViolations(t, db)
	})

	for _, unavailableServer := range []struct {
		name   string
		update string
	}{
		{"archived", `UPDATE servers SET archived_at = 2 WHERE id = 10`},
		{"decommissioning", `UPDATE servers SET decommission_status = 'pending' WHERE id = 10`},
	} {
		t.Run("resources on "+unavailableServer.name+" server do not require mutation", func(t *testing.T) {
			service, db, _, user := newDeleteUserFixture(t, RoleCarpool)
			insertDeletionServer(t, db, 10, user.ID)
			mustExecDeleteTest(t, db, unavailableServer.update)
			insertDeletionProxy(t, db, 20, 10, 8443)
			insertDeletionClient(t, db, 30, 20, user.ID)
			insertManualDeletionRelay(t, db, 40, 10, user.ID, int64(30))
			mutations, err := service.DeleteUser(t.Context(), user.ID)
			if err != nil || len(mutations) != 0 {
				t.Fatalf("DeleteUser() unavailable resources = (%+v, %v), want no mutations", mutations, err)
			}
			assertTableCount(t, db, "clients", "id = ?", 0, int64(30))
			assertTableCount(t, db, "relays", "id = ?", 0, int64(40))
			assertServerVersion(t, db, 10, 1)
			var owner sql.NullInt64
			if err := db.QueryRow(`SELECT owner_user_id FROM servers WHERE id = 10`).Scan(&owner); err != nil || owner.Valid {
				t.Fatalf("unavailable server owner after deletion = %+v, %v", owner, err)
			}
			assertNoForeignKeyViolations(t, db)
		})
	}

	t.Run("admin cannot be deleted", func(t *testing.T) {
		service, db, admin, _ := newDeleteUserFixture(t, RoleVIP)
		if _, err := service.DeleteUser(t.Context(), admin.ID); !errors.Is(err, ErrCannotDeleteAdmin) {
			t.Fatalf("DeleteUser(admin) error = %v, want ErrCannotDeleteAdmin", err)
		}
		assertTableCount(t, db, "users", "id = ?", 1, admin.ID)
	})
}

func newDeleteUserFixture(t *testing.T, role string) (*Service, *sql.DB, User, User) {
	t.Helper()
	service, db := newTestService(t)
	admin, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(t.Context(), admin.ID, role)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.RegisterWithInvitation(t.Context(), invitation.Token, "delete-user", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return service, db, admin, user
}

func insertDeletionServer(t *testing.T, db *sql.DB, id int64, owner any) {
	t.Helper()
	mustExecDeleteTest(t, db, `INSERT INTO servers
		(id, name, owner_user_id, status, created_at, updated_at)
		VALUES (?, ?, ?, 'offline', 1, 1)`, id, "server", owner)
}

func insertDeletionProxy(t *testing.T, db *sql.DB, id, serverID int64, port int) {
	t.Helper()
	mustExecDeleteTest(t, db, `INSERT INTO proxies
		(id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		VALUES (?, ?, ?, 'vless', ?, '{}', 1, 1)`, id, serverID, "proxy", port)
}

func insertDeletionClient(t *testing.T, db *sql.DB, id, proxyID, userID int64) {
	t.Helper()
	mustExecDeleteTest(t, db, `INSERT INTO clients
		(id, proxy_id, assigned_user_id, name, credential_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, '{}', 1, 1)`, id, proxyID, userID, "client")
}

func insertManualDeletionRelay(t *testing.T, db *sql.DB, id, serverID int64, owner, sourceClient any) {
	t.Helper()
	mustExecDeleteTest(t, db, `INSERT INTO relays
		(id, server_id, owner_user_id, source_client_id, name, listen_port,
		 target_type, target_host, target_port, network, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 9500, 'manual', 'example.com', 443, 'tcp', 1, 1)`,
		id, serverID, owner, sourceClient, "manual relay")
}

func insertDeletionLanding(t *testing.T, db *sql.DB, id, userID int64) {
	t.Helper()
	mustExecDeleteTest(t, db, `INSERT INTO landing_nodes
		(id, owner_user_id, name, protocol, host, port, uri, created_at, updated_at)
		VALUES (?, ?, 'landing', 'vless', 'example.com', 443, 'vless://example', 1, 1)`, id, userID)
}

func mustExecDeleteTest(t *testing.T, db *sql.DB, statement string, arguments ...any) {
	t.Helper()
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("execute deletion fixture: %v\n%s", err, statement)
	}
}

func assertDeletionMutations(t *testing.T, mutations []proxystore.Mutation, err error, serverIDs ...int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if len(mutations) != len(serverIDs) {
		t.Fatalf("DeleteUser() mutations = %+v, want server IDs %v", mutations, serverIDs)
	}
	for index, serverID := range serverIDs {
		if mutations[index].ServerID != serverID || mutations[index].Version != 2 {
			t.Fatalf("DeleteUser() mutation[%d] = %+v, want server %d version 2", index, mutations[index], serverID)
		}
	}
}

func assertTableCount(t *testing.T, db *sql.DB, table, condition string, want int, arguments ...any) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+condition, arguments...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func assertServerVersion(t *testing.T, db *sql.DB, serverID, want int64) {
	t.Helper()
	var version int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != want {
		t.Fatalf("server %d desired_state_version = %d, want %d", serverID, version, want)
	}
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign key violation: table=%s rowid=%v parent=%s fk=%d", table, rowID, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
