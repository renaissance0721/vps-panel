package proxy

import (
	"errors"
	"sync"
	"testing"

	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func insertRelayPortTestUser(t *testing.T, service *Service, username string) int64 {
	t.Helper()
	result, err := service.db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES (?, 'hash', 'user', 1, 1)`, username)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func createAssignedRelayPortClient(t *testing.T, service *Service, userID, proxyID int64, count int) Client {
	t.Helper()
	value, _, err := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
		UserID: userID, ProxyID: proxyID, Name: "Assigned", Enabled: true,
		UserRelayPortCount: count,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAssignedClientRelayPortCountsAreValidatedAndContiguous(t *testing.T) {
	db, service, serverID := newTestService(t)
	userID := insertRelayPortTestUser(t, service, "relay-count-user")
	for index, count := range []int{0, 1, 5} {
		proxyValue := createRealityProxy(t, service, serverID, 443+index, "Count")
		client := createAssignedRelayPortClient(t, service, userID, proxyValue.ID, count)
		if client.UserRelayPortCount != count {
			t.Fatalf("count %d client allocation = %+v", count, client)
		}
		if count == 0 {
			if client.UserRelayPortStart != nil || client.UserRelayPortEnd != nil {
				t.Fatalf("zero allocation = %+v", client)
			}
			continue
		}
		if client.UserRelayPortStart == nil || client.UserRelayPortEnd == nil ||
			*client.UserRelayPortEnd-*client.UserRelayPortStart+1 != count {
			t.Fatalf("count %d is not contiguous: %+v", count, client)
		}
		var rowCount, distinctCount int
		if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT port) FROM client_relay_ports WHERE client_id = ?`, client.ID).
			Scan(&rowCount, &distinctCount); err != nil || rowCount != count || distinctCount != count {
			t.Fatalf("count %d rows = %d/%d, %v", count, rowCount, distinctCount, err)
		}
	}

	proxyValue := createRealityProxy(t, service, serverID, 9000, "Invalid Count")
	for _, count := range []int{-1, 6} {
		if _, _, err := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
			UserID: userID, ProxyID: proxyValue.ID, Name: "Invalid", Enabled: true, UserRelayPortCount: count,
		}); !errors.Is(err, ErrInvalidClientRelayPortCount) {
			t.Fatalf("count %d error = %v", count, err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_client_relay_port BEFORE INSERT ON client_relay_ports
		BEGIN SELECT RAISE(ABORT, 'reject relay port'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
		UserID: userID, ProxyID: proxyValue.ID, Name: "Atomic", Enabled: true, UserRelayPortCount: 1,
	}); err == nil {
		t.Fatal("relay port reservation failure did not fail assigned client creation")
	}
	var assignedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients WHERE proxy_id = ? AND assigned_user_id = ?`, proxyValue.ID, userID).Scan(&assignedCount); err != nil || assignedCount != 0 {
		t.Fatalf("failed allocation left %d assigned clients, %v", assignedCount, err)
	}
}

func TestClientRelayReservationsAvoidListenersAndBlockAdminListeners(t *testing.T) {
	_, service, serverID := newTestService(t)
	userID := insertRelayPortTestUser(t, service, "relay-conflict-user")
	_ = createRealityProxy(t, service, serverID, relaystore.UserRelayPortStart, "Occupied Proxy")
	relays := relaystore.NewService(service.db)
	if _, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: serverID, Name: "Occupied Relay", ListenPort: relaystore.UserRelayPortStart + 1,
		TargetType: relaystore.TargetManual, TargetHost: "1.1.1.1", TargetPort: 443,
		Network: relaystore.NetworkTCP, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	sourceProxy := createRealityProxy(t, service, serverID, 8443, "Source")
	client := createAssignedRelayPortClient(t, service, userID, sourceProxy.ID, 5)
	if client.UserRelayPortStart == nil || *client.UserRelayPortStart == relaystore.UserRelayPortStart ||
		*client.UserRelayPortStart == relaystore.UserRelayPortStart+1 {
		t.Fatalf("allocation overlaps existing listener: %+v", client)
	}
	reservedPort := *client.UserRelayPortStart
	if _, _, err := service.Create(t.Context(), CreateInput{
		ServerID: serverID, Name: "Blocked Proxy", ListenPort: reservedPort, EntryHostMode: EntryHostAuto,
		Enabled: true, Security: SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "default",
	}); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("proxy on reservation error = %v", err)
	}
	if _, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: serverID, Name: "Blocked Relay", ListenPort: reservedPort,
		TargetType: relaystore.TargetManual, TargetHost: "1.1.1.1", TargetPort: 443,
		Network: relaystore.NetworkTCP, Enabled: true,
	}); !errors.Is(err, relaystore.ErrPortConflict) {
		t.Fatalf("admin relay on reservation error = %v", err)
	}
	if _, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: serverID, OwnerUserID: &userID, SourceClientID: &client.ID,
		Name: "Owned Relay", ListenPort: reservedPort,
		TargetType: relaystore.TargetManual, TargetHost: "1.1.1.1", TargetPort: 443,
		Network: relaystore.NetworkTCP, Enabled: true,
	}); err != nil {
		t.Fatalf("owned relay could not consume reservation: %v", err)
	}
}

func TestClientRelayPortUniqueScopeAndConcurrentAllocation(t *testing.T) {
	db, service, firstServerID := newTestService(t)
	secondResult, err := db.Exec(`INSERT INTO servers (name, status, created_at, updated_at) VALUES ('second', 'offline', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	secondServerID, _ := secondResult.LastInsertId()
	firstProxy := createRealityProxy(t, service, firstServerID, 443, "First")
	secondProxy := createRealityProxy(t, service, secondServerID, 443, "Second")
	thirdClient, _, err := service.CreateClient(t.Context(), firstProxy.ID, ClientCreateInput{Name: "Third", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_relay_ports (client_id, server_id, port, created_at) VALUES (?, ?, 25000, 1)`, firstProxy.Clients[0].ID, firstServerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_relay_ports (client_id, server_id, port, created_at) VALUES (?, ?, 25000, 1)`, secondProxy.Clients[0].ID, secondServerID); err != nil {
		t.Fatalf("same port on another server: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO client_relay_ports (client_id, server_id, port, created_at) VALUES (?, ?, 25000, 1)`, thirdClient.ID, firstServerID); err == nil {
		t.Fatal("duplicate reservation on the same server succeeded")
	}

	userID := insertRelayPortTestUser(t, service, "relay-concurrency-user")
	proxyIDs := make([]int64, 6)
	for index := range proxyIDs {
		proxyIDs[index] = createRealityProxy(t, service, firstServerID, 10000+index, "Concurrent").ID
	}
	errorsByClient := make(chan error, len(proxyIDs))
	var wait sync.WaitGroup
	for _, proxyID := range proxyIDs {
		wait.Add(1)
		go func(proxyID int64) {
			defer wait.Done()
			_, _, createErr := service.CreateAssignedClient(t.Context(), AssignedClientCreateInput{
				UserID: userID, ProxyID: proxyID, Name: "Concurrent", Enabled: true, UserRelayPortCount: 5,
			})
			errorsByClient <- createErr
		}(proxyID)
	}
	wait.Wait()
	close(errorsByClient)
	for createErr := range errorsByClient {
		if createErr != nil {
			t.Fatalf("concurrent allocation: %v", createErr)
		}
	}
	var total, distinct int
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT port) FROM client_relay_ports WHERE server_id = ?`, firstServerID).
		Scan(&total, &distinct); err != nil || total != distinct {
		t.Fatalf("concurrent reservations total/distinct = %d/%d, %v", total, distinct, err)
	}
}

func TestClientRelayPortReallocationRequiresNoActiveRelayAndDeleteReleases(t *testing.T) {
	db, service, serverID := newTestService(t)
	userID := insertRelayPortTestUser(t, service, "relay-reallocate-user")
	proxyValue := createRealityProxy(t, service, serverID, 443, "Source")
	client := createAssignedRelayPortClient(t, service, userID, proxyValue.ID, 2)
	relays := relaystore.NewService(db)
	relayValue, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: serverID, OwnerUserID: &userID, SourceClientID: &client.ID,
		Name: "Active", ListenPort: *client.UserRelayPortStart,
		TargetType: relaystore.TargetManual, TargetHost: "1.1.1.1", TargetPort: 443,
		Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateClientRelayPortCount(t.Context(), client.ID, 1); !errors.Is(err, ErrClientRelayPortsActive) {
		t.Fatalf("active relay reallocation error = %v", err)
	}
	if _, err := relays.Delete(t.Context(), relayValue.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateClientRelayPortCount(t.Context(), client.ID, 1)
	if err != nil || updated.UserRelayPortCount != 1 {
		t.Fatalf("reallocation = %+v, %v", updated, err)
	}
	if _, err := service.DeleteClient(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM client_relay_ports WHERE client_id = ?`, client.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reservations after client delete = %d, %v", count, err)
	}
}
