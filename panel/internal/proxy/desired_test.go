package proxy

import (
	"database/sql"
	"testing"
)

func TestDesiredStateFiltersDisabledRecordsAndClientUDPDoesNotChangeIt(t *testing.T) {
	db, service, serverID := newTestService(t)
	first := createRealityProxy(t, service, serverID, 443, "First")
	second := createRealityProxy(t, service, serverID, 8443, "Second")
	client, _, err := service.CreateClient(t.Context(), first.ID, ClientCreateInput{Name: "enabled", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	falseValue := false
	if _, _, err := service.UpdateClient(t.Context(), first.Clients[0].ID, ClientUpdateInput{Enabled: &falseValue}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Update(t.Context(), second.ID, UpdateInput{Enabled: &falseValue}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := ListDesired(t.Context(), tx, serverID)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 1 || desired[0].ID != first.ID || len(desired[0].Clients) != 1 ||
		desired[0].Clients[0].ID != client.ID || desired[0].Clients[0].StatsID != clientStatsIdentifier(client.ID) {
		t.Fatalf("desired proxies = %+v", desired)
	}
	if desired[0].Reality == nil || desired[0].Reality.PrivateKey == "" {
		t.Fatal("desired REALITY state lacks private material")
	}
}

func TestDesiredMutationsMarkExistingAgentPendingWithoutAdvancingAppliedVersion(t *testing.T) {
	db, service, serverID := newTestService(t)
	if _, err := db.Exec(`INSERT INTO agents
		(server_id, token_hash, version, registered_at, last_seen_at, applied_config_version,
		 config_sync_status, config_sync_error, config_synced_at, created_at, updated_at)
		VALUES (?, 'agent-token', 'test', 1, 1, 7, 'failed', 'old failure', 123, 1, 1)`, serverID); err != nil {
		t.Fatal(err)
	}
	assertPending := func(label string) {
		t.Helper()
		var applied int64
		var status, message string
		var syncedAt sql.NullInt64
		if err := db.QueryRow(`SELECT applied_config_version, config_sync_status, config_sync_error, config_synced_at
			FROM agents WHERE server_id = ?`, serverID).Scan(&applied, &status, &message, &syncedAt); err != nil {
			t.Fatal(err)
		}
		if applied != 7 || status != "pending" || message != "" || !syncedAt.Valid || syncedAt.Int64 != 123 {
			t.Fatalf("%s Agent config state = applied %d status %q error %q synced %v", label, applied, status, message, syncedAt)
		}
	}
	reset := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE agents SET config_sync_status = 'failed', config_sync_error = 'old failure' WHERE server_id = ?`, serverID); err != nil {
			t.Fatal(err)
		}
	}

	proxyValue := createRealityProxy(t, service, serverID, 443, "Proxy")
	assertPending("Proxy create")
	reset()
	proxyName := "Proxy updated"
	if _, _, err := service.Update(t.Context(), proxyValue.ID, UpdateInput{Name: &proxyName}); err != nil {
		t.Fatal(err)
	}
	assertPending("Proxy update")
	reset()
	client, _, err := service.CreateClient(t.Context(), proxyValue.ID, ClientCreateInput{Name: "second", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	assertPending("Client create")
	reset()
	clientName := "second updated"
	if _, _, err := service.UpdateClient(t.Context(), client.ID, ClientUpdateInput{Name: &clientName}); err != nil {
		t.Fatal(err)
	}
	assertPending("Client update")
	reset()
	if _, err := service.DeleteClient(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	assertPending("Client delete")
	reset()
	if _, err := service.Delete(t.Context(), proxyValue.ID); err != nil {
		t.Fatal(err)
	}
	assertPending("Proxy delete")
}
