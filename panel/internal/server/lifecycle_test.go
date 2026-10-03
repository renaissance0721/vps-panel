package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestRequestDecommissionRequiresRegisteredAgentAndBothStrictCapabilities(t *testing.T) {
	t.Run("unregistered", func(t *testing.T) {
		service, _ := newTestService(t)
		created, err := service.Create(t.Context(), "Unregistered")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.RequestDecommission(t.Context(), created.ID); !errors.Is(err, ErrAgentNotRegistered) {
			t.Fatalf("RequestDecommission() error = %v, want ErrAgentNotRegistered", err)
		}
	})

	for _, test := range []struct {
		name         string
		metadata     agentcontrol.Metadata
		wantRejected bool
	}{
		{name: "legacy", metadata: agentcontrol.Metadata{Version: "v0.19.0"}, wantRejected: true},
		{name: "missing managed purge", metadata: agentcontrol.Metadata{Implementation: "third-party", Version: "v0.20.0", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilitySelfDecommission}}, wantRejected: true},
		{name: "missing self decommission", metadata: agentcontrol.Metadata{Implementation: "third-party", Version: "v0.20.0", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilityManagedRuntimePurge}}, wantRejected: true},
		{name: "both declared", metadata: agentcontrol.Metadata{Implementation: "third-party", Version: "v0.20.0", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilityManagedRuntimePurge, agentcontrol.CapabilitySelfDecommission}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, db := newTestService(t)
			created, err := service.Create(t.Context(), test.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := agentcontrol.NewService(db, time.Now).RegisterAgentWithMetadata(t.Context(), created.EnrollmentToken, test.metadata, false); err != nil {
				t.Fatal(err)
			}
			version, err := service.RequestDecommission(t.Context(), created.ID)
			if test.wantRejected {
				if !errors.Is(err, ErrDecommissionUnsupported) {
					t.Fatalf("RequestDecommission() = (%d, %v), want ErrDecommissionUnsupported", version, err)
				}
				return
			}
			if err != nil || version != 2 {
				t.Fatalf("RequestDecommission() = (%d, %v), want version 2", version, err)
			}
		})
	}
}

func TestArchiveKeepsServerAndRemovesUnusedEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Singapore 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	servers, err := service.List(context.Background())
	if err != nil || len(servers) != 1 || servers[0].ID != created.ID {
		t.Fatalf("List() = (%+v, %v), want created server", servers, err)
	}
	got, err := service.Get(context.Background(), created.ID)
	if err != nil || got.Name != created.Name {
		t.Fatalf("Get() = (%+v, %v), want created server", got, err)
	}

	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := service.Get(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after archive error = %v, want ErrNotFound", err)
	}
	active, err := service.List(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("List() after archive = (%+v, %v), want empty", active, err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].ID != created.ID || archived[0].ArchivedAt == nil {
		t.Fatalf("ListArchived() = (%+v, %v), want archived server %d", archived, err, created.ID)
	}
	var serverCount int
	var archivedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT COUNT(*), archived_at FROM servers WHERE id = ?`, created.ID,
	).Scan(&serverCount, &archivedAt); err != nil {
		t.Fatalf("read archived server: %v", err)
	}
	if serverCount != 1 || !archivedAt.Valid {
		t.Fatalf("archived server state = (count %d, archived %v), want preserved and archived", serverCount, archivedAt.Valid)
	}
	var enrollmentCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&enrollmentCount); err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("unused enrollment count after archive = %d, want 0", enrollmentCount)
	}
}

func TestPermanentlyDeleteOnlyDeletesArchivedServer(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Permanent Delete")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PermanentlyDelete() active server error = %v, want ErrNotFound", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count permanently deleted server: %v", err)
	}
	if count != 0 {
		t.Fatalf("server count after permanent delete = %d, want 0", count)
	}
}

func TestFinalizeDecommissionPurgesThenDisablesCrossServerDependencies(t *testing.T) {
	service, db := newTestService(t)
	source, err := service.Create(t.Context(), "Relay source")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), "Proxy target")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterAgentWithMetadata(t.Context(), target.EnrollmentToken, agentcontrol.Metadata{
		Implementation: "official", Version: "v0.79.0", APIVersion: 1,
		Capabilities: []string{agentcontrol.CapabilityManagedRuntimePurge, agentcontrol.CapabilitySelfDecommission},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	proxyID, clientID, relayID := insertLifecycleRelayTopology(t, db, source.ID, target.ID)
	var sourceBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.ID).Scan(&sourceBefore); err != nil {
		t.Fatal(err)
	}
	version, err := service.RequestDecommission(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.GetDesiredState(t.Context(), registered.ID, target.ID)
	if err != nil || !state.Decommission || !state.XrayPurge || !state.RealmPurge || len(state.Proxies) != 0 || len(state.Relays) != 0 {
		t.Fatalf("decommission desired state = (%+v, %v)", state, err)
	}
	finalized, mutations, err := service.FinalizeDecommissionWithMutations(t.Context(), target.ID, version, agentcontrol.ConfigSyncSuccess, "")
	if err != nil || !finalized || len(mutations) != 1 || mutations[0].ServerID != source.ID || mutations[0].Version != sourceBefore+1 {
		t.Fatalf("finalize = (%t, %+v, %v)", finalized, mutations, err)
	}
	assertLifecycleDependencyState(t, db, target.ID, proxyID, relayID, false, false, sourceBefore+1)
	var clientCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients WHERE id = ?`, clientID).Scan(&clientCount); err != nil || clientCount != 1 {
		t.Fatalf("ordinary target client count = %d, %v", clientCount, err)
	}
}

func TestForceArchiveSourceDoesNotChangeTargetProxyOrLanding(t *testing.T) {
	service, db := newTestService(t)
	source, err := service.Create(t.Context(), "Relay source")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), "Proxy target")
	if err != nil {
		t.Fatal(err)
	}
	_, _, relayID := insertLifecycleRelayTopology(t, db, source.ID, target.ID)
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (100, 'landing-owner', 'hash', 'vip', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landing_nodes (id, owner_user_id, name, visibility, protocol, host, port, uri, created_at, updated_at)
		VALUES (200, 100, 'External landing', 'private', 'vless', 'landing.example.com', 443, 'vless://redacted', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays (server_id, name, listen_address, listen_port, target_type, target_landing_id, network, enabled, created_at, updated_at)
		VALUES (?, 'Landing relay', '0.0.0.0', 9503, 'landing', 200, 'tcp', 1, 1, 1)`, source.ID); err != nil {
		t.Fatal(err)
	}
	var targetVersion int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, target.ID).Scan(&targetVersion); err != nil {
		t.Fatal(err)
	}
	mutations, err := service.ForceArchiveWithMutations(t.Context(), source.ID)
	if err != nil || len(mutations) != 0 {
		t.Fatalf("archive source mutations = %+v, %v", mutations, err)
	}
	var proxyEnabled, relaysEnabled, landingCount int
	var targetVersionAfter int64
	if err := db.QueryRow(`SELECT enabled FROM proxies WHERE server_id = ?`, target.ID).Scan(&proxyEnabled); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM relays WHERE server_id = ? AND enabled = 1`, source.ID).Scan(&relaysEnabled); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM landing_nodes WHERE id = 200`).Scan(&landingCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, target.ID).Scan(&targetVersionAfter); err != nil {
		t.Fatal(err)
	}
	if proxyEnabled != 1 || relaysEnabled != 0 || landingCount != 1 || targetVersionAfter != targetVersion {
		t.Fatalf("source archive changed target: proxy=%d relays=%d landing=%d version=%d->%d relay=%d", proxyEnabled, relaysEnabled, landingCount, targetVersion, targetVersionAfter, relayID)
	}
}

func TestForceArchiveDisablesSubscriptionDependenciesAndPermanentDeleteIsForeignKeyClean(t *testing.T) {
	service, db := newTestService(t)
	source, err := service.Create(t.Context(), "Relay source")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), "Proxy target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterAgent(t.Context(), source.EnrollmentToken, "v0.79.0", false); err != nil {
		t.Fatal(err)
	}
	proxyID, _, relayID := insertLifecycleRelayTopology(t, db, source.ID, target.ID)
	if _, err := db.Exec(`INSERT INTO subscription_published_nodes
		(id, name, mode, target_proxy_id, source_server_id, relay_id, enabled, created_at, updated_at)
		VALUES (300, 'Published relay', 'relay', ?, ?, ?, 1, 1, 1)`, proxyID, source.ID, relayID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plans (id, name, enabled, created_at, updated_at) VALUES (301, 'Plan', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (301, 300, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (302, 'personal-owner', 'hash', 'vip', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var presetID int64
	if err := db.QueryRow(`SELECT id FROM subscription_routing_presets ORDER BY id LIMIT 1`).Scan(&presetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO personal_subscription_groups
		(id, owner_user_id, name, token, client_name, routing_preset_id, created_at, updated_at)
		VALUES (303, 302, 'Personal', 'personal-token', 'client', ?, 1, 1)`, presetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO personal_subscription_nodes
		(group_id, source_type, source_id, display_name, enabled, position, created_at, updated_at)
		VALUES (303, 'relay', ?, 'Relay node', 1, 1, 1, 1)`, relayID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (304, 'subscriber', 'hash', 'subscriber', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_profiles
		(user_id, plan_id, subscription_token, created_at, updated_at) VALUES (304, 301, 'subscriber-token', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_usage (user_id, cycle_started_at, updated_at) VALUES (304, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	subscriberClientResult, err := db.Exec(`INSERT INTO clients
		(proxy_id, assigned_user_id, name, credential_json, enabled, created_at, updated_at)
		VALUES (?, 304, 'Subscriber Client', '{}', 1, 1, 1)`, proxyID)
	if err != nil {
		t.Fatal(err)
	}
	subscriberClientID, _ := subscriberClientResult.LastInsertId()
	if _, err := db.Exec(`INSERT INTO subscriber_clients (user_id, proxy_id, client_id, created_at)
		VALUES (304, ?, ?, 1)`, proxyID, subscriberClientID); err != nil {
		t.Fatal(err)
	}
	summary, err := service.GetDependencySummary(t.Context(), target.ID)
	if err != nil || summary.ProxyCount != 1 || summary.ClientCount != 2 || len(summary.ReferencingRelays) != 1 || summary.PublishedNodeCount != 1 || summary.PersonalNodeCount != 1 || summary.SubscriberClientCount != 1 {
		t.Fatalf("dependency summary = %+v, %v", summary, err)
	}
	var sourceBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.ID).Scan(&sourceBefore); err != nil {
		t.Fatal(err)
	}
	mutations, err := service.ForceArchiveWithMutations(t.Context(), target.ID)
	if err != nil || len(mutations) != 1 || mutations[0].ServerID != source.ID || mutations[0].Version != sourceBefore+1 {
		t.Fatalf("force archive target = %+v, %v", mutations, err)
	}
	assertLifecycleDependencyState(t, db, target.ID, proxyID, relayID, false, false, sourceBefore+1)
	for name, query := range map[string]string{
		"published": `SELECT enabled FROM subscription_published_nodes WHERE id = 300`,
		"personal":  `SELECT enabled FROM personal_subscription_nodes WHERE group_id = 303`,
	} {
		var enabled int
		if err := db.QueryRow(query).Scan(&enabled); err != nil || enabled != 0 {
			t.Fatalf("%s dependency enabled = %d, %v", name, enabled, err)
		}
	}
	var subscriberMappingCount, subscriberClientCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 304`).Scan(&subscriberMappingCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM clients WHERE id = ?`, subscriberClientID).Scan(&subscriberClientCount)
	if subscriberMappingCount != 0 || subscriberClientCount != 0 {
		t.Fatalf("subscriber references remained: mapping=%d client=%d", subscriberMappingCount, subscriberClientCount)
	}
	desired, err := relaystore.NewService(db).ListDesired(t.Context(), db, source.ID)
	if err != nil || len(desired) != 0 {
		t.Fatalf("source desired relays after target archive = %+v, %v", desired, err)
	}
	if _, err := service.PermanentlyDeleteWithMutations(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	var serverCount, relayCount, publishedCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = ?`, target.ID).Scan(&serverCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM relays WHERE id = ?`, relayID).Scan(&relayCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM subscription_published_nodes WHERE id = 300`).Scan(&publishedCount)
	if serverCount != 0 || relayCount != 0 || publishedCount != 0 {
		t.Fatalf("permanent deletion leftovers: server=%d relay=%d published=%d", serverCount, relayCount, publishedCount)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check returned a violation")
	}
}

func insertLifecycleRelayTopology(t *testing.T, db *sql.DB, sourceServerID, targetServerID int64) (int64, int64, int64) {
	t.Helper()
	result, err := db.Exec(`INSERT INTO proxies
		(server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		VALUES (?, 'Target Proxy', 'vless', 443, 'manual', 'target.example.com', 1, '{}', 1, 1)`, targetServerID)
	if err != nil {
		t.Fatal(err)
	}
	proxyID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO clients (proxy_id, name, credential_json, enabled, created_at, updated_at)
		VALUES (?, 'Target Client', '{}', 1, 1, 1)`, proxyID)
	if err != nil {
		t.Fatal(err)
	}
	clientID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_proxy_id, target_client_id, network, enabled, created_at, updated_at)
		VALUES (?, 'Cross Relay', '0.0.0.0', 9502, 'proxy', ?, ?, 'tcp', 1, 1, 1)`, sourceServerID, proxyID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	relayID, _ := result.LastInsertId()
	return proxyID, clientID, relayID
}

func assertLifecycleDependencyState(t *testing.T, db *sql.DB, targetServerID, proxyID, relayID int64, wantProxy, wantRelay bool, wantSourceVersion int64) {
	t.Helper()
	var archived sql.NullInt64
	var proxyEnabled, relayEnabled int
	var sourceVersion int64
	if err := db.QueryRow(`SELECT archived_at FROM servers WHERE id = ?`, targetServerID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT enabled FROM proxies WHERE id = ?`, proxyID).Scan(&proxyEnabled); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT relays.enabled, servers.desired_state_version
		FROM relays JOIN servers ON servers.id = relays.server_id WHERE relays.id = ?`, relayID).Scan(&relayEnabled, &sourceVersion); err != nil {
		t.Fatal(err)
	}
	if !archived.Valid || (proxyEnabled != 0) != wantProxy || (relayEnabled != 0) != wantRelay || sourceVersion != wantSourceVersion {
		t.Fatalf("dependency state archived=%v proxy=%d relay=%d sourceVersion=%d", archived.Valid, proxyEnabled, relayEnabled, sourceVersion)
	}
}

func TestUpdateOutboundPreferenceBumpsDesiredStateAndMarksAgentPending(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(t.Context(), "Outbound preference")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.21.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET config_sync_status = 'failed', config_sync_error = 'previous failure' WHERE id = ?`, registered.ID); err != nil {
		t.Fatal(err)
	}
	for index, preference := range []string{OutboundPreferIPv4, OutboundPreferIPv6, OutboundAuto} {
		updated, version, err := service.UpdateOutboundPreference(t.Context(), created.ID, preference)
		if err != nil || updated.OutboundPreference != preference || version != int64(index+2) {
			t.Fatalf("UpdateOutboundPreference(%q) = (%+v, %d, %v)", preference, updated, version, err)
		}
		var status, syncError string
		var storedVersion int64
		if err := db.QueryRow(`SELECT agents.config_sync_status, agents.config_sync_error, servers.desired_state_version
			FROM agents JOIN servers ON servers.id = agents.server_id WHERE agents.id = ?`, registered.ID,
		).Scan(&status, &syncError, &storedVersion); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || syncError != "" || storedVersion != version {
			t.Fatalf("sync state = (%q, %q, %d), want pending, empty, %d", status, syncError, storedVersion, version)
		}
		listed, err := service.List(t.Context())
		if err != nil || len(listed) != 1 || listed[0].OutboundPreference != preference {
			t.Fatalf("List() = (%+v, %v)", listed, err)
		}
	}
	for _, invalid := range []string{"", "ipv4", "force_ipv6"} {
		if _, _, err := service.UpdateOutboundPreference(t.Context(), created.ID, invalid); !errors.Is(err, ErrInvalidOutboundPreference) {
			t.Fatalf("invalid preference %q error = %v", invalid, err)
		}
	}
}

func TestUpdateBlockChinaInboundChangesDesiredStateOnlyWhenNeeded(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(t.Context(), "China inbound block")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.29.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET config_sync_status = 'success', config_sync_error = '' WHERE id = ?`, registered.ID); err != nil {
		t.Fatal(err)
	}
	unchanged, version, changed, err := service.UpdateBlockChinaInbound(t.Context(), created.ID, false)
	if err != nil || changed || version != 1 || unchanged.BlockChinaInbound {
		t.Fatalf("no-op update = (%+v, %d, %t, %v)", unchanged, version, changed, err)
	}
	var status string
	if err := db.QueryRow(`SELECT config_sync_status FROM agents WHERE id = ?`, registered.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "success" {
		t.Fatalf("no-op config status = %q, want success", status)
	}
	updated, version, changed, err := service.UpdateBlockChinaInbound(t.Context(), created.ID, true)
	if err != nil || !changed || version != 2 || !updated.BlockChinaInbound {
		t.Fatalf("enabled update = (%+v, %d, %t, %v)", updated, version, changed, err)
	}
	state, err := service.GetDesiredState(t.Context(), registered.ID, registered.ServerID)
	if err != nil || !state.BlockChinaInbound || state.Version != 2 {
		t.Fatalf("desired state = (%+v, %v)", state, err)
	}
	if err := db.QueryRow(`SELECT config_sync_status FROM agents WHERE id = ?`, registered.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("changed config status = %q, want pending", status)
	}
}

func TestUpdateExpirationSetsModifiesClearsAndReturnsFromQueries(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Expiration")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstUpdatedAt := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	firstExpiration := time.Date(2026, 12, 31, 15, 59, 0, 0, time.UTC)
	service.now = func() time.Time { return firstUpdatedAt }
	updated, err := service.UpdateExpiration(context.Background(), created.ID, &firstExpiration)
	if err != nil {
		t.Fatalf("UpdateExpiration() error = %v", err)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(firstExpiration) || !updated.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("updated expiration = (%v, updated %v)", updated.ExpiresAt, updated.UpdatedAt)
	}
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ExpiresAt == nil || !listed[0].ExpiresAt.Equal(firstExpiration) {
		t.Fatalf("List() expiration = (%+v, %v)", listed, err)
	}
	got, err := service.Get(context.Background(), created.ID)
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(firstExpiration) {
		t.Fatalf("Get() expiration = (%v, %v)", got.ExpiresAt, err)
	}

	secondUpdatedAt := firstUpdatedAt.Add(time.Minute)
	secondExpiration := firstExpiration.Add(24 * time.Hour)
	service.now = func() time.Time { return secondUpdatedAt }
	updated, err = service.UpdateExpiration(context.Background(), created.ID, &secondExpiration)
	if err != nil || updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(secondExpiration) ||
		!updated.UpdatedAt.Equal(secondUpdatedAt) {
		t.Fatalf("modified expiration = (%v, updated %v, error %v)", updated.ExpiresAt, updated.UpdatedAt, err)
	}

	clearedAt := secondUpdatedAt.Add(time.Minute)
	service.now = func() time.Time { return clearedAt }
	updated, err = service.UpdateExpiration(context.Background(), created.ID, nil)
	if err != nil || updated.ExpiresAt != nil || !updated.UpdatedAt.Equal(clearedAt) {
		t.Fatalf("cleared expiration = (%v, updated %v, error %v)", updated.ExpiresAt, updated.UpdatedAt, err)
	}
	if _, err := service.UpdateExpiration(context.Background(), created.ID+100, &firstExpiration); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing server expiration error = %v, want ErrNotFound", err)
	}
}

func TestExpirationSurvivesAgentLifecycleAndArchive(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Expiration Lifecycle")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	expiration := time.Date(2020, 1, 2, 3, 4, 0, 0, time.UTC)
	if _, err := service.UpdateExpiration(context.Background(), created.ID, &expiration); err != nil {
		t.Fatalf("UpdateExpiration() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.1", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), firstAgent.ID, firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	online, err := service.Get(context.Background(), created.ID)
	if err != nil || online.Status != StatusOnline || online.ExpiresAt == nil || !online.ExpiresAt.Equal(expiration) {
		t.Fatalf("online expired server = (%+v, %v)", online, err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil || rebind.ExpiresAt == nil || !rebind.ExpiresAt.Equal(expiration) {
		t.Fatalf("CreateEnrollment() expiration = (%v, %v)", rebind.ExpiresAt, err)
	}
	if _, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.7.0", true); err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	afterRebind, err := service.Get(context.Background(), created.ID)
	if err != nil || afterRebind.ExpiresAt == nil || !afterRebind.ExpiresAt.Equal(expiration) {
		t.Fatalf("expiration after rebind = (%v, %v)", afterRebind.ExpiresAt, err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].ExpiresAt == nil || !archived[0].ExpiresAt.Equal(expiration) {
		t.Fatalf("archived expiration = (%+v, %v)", archived, err)
	}
}
