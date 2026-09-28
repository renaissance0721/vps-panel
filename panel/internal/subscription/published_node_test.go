package subscription

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestDirectPublishedNodeCRUD(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "Target", "203.0.113.10")
	insertSubscriptionTestProxy(t, db, 10, 1, "SG Native", 443, relay.EntryHostAuto, "")

	created, mutation, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: " 🇸🇬 SG-01 ", Mode: NodeModeDirect, TargetProxyID: 10, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mutation != nil || created.Name != "🇸🇬 SG-01" || created.Mode != NodeModeDirect ||
		created.TargetProxyID != 10 || created.SourceProxyID != nil || created.RelayID != nil ||
		created.TrafficMultiplierBP != 100 || !created.Enabled {
		t.Fatalf("created direct published node = %+v, mutation = %+v", created, mutation)
	}

	name := "SG Premium"
	updated, mutations, err := service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{Name: &name})
	if err != nil || len(mutations) != 0 || updated.Name != name {
		t.Fatalf("updated direct published node = %+v, mutations = %+v, error = %v", updated, mutations, err)
	}
	if mutation, err := service.DeletePublishedNode(t.Context(), created.ID); err != nil || mutation != nil {
		t.Fatalf("delete direct published node mutation = %+v, error = %v", mutation, err)
	}
	if _, err := service.GetPublishedNode(t.Context(), created.ID); !errors.Is(err, ErrPublishedNodeNotFound) {
		t.Fatalf("deleted node error = %v", err)
	}
}

func TestPublishedNodeTrafficMultiplierValidationAndMetadataUpdate(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "Target", "203.0.113.10")
	insertSubscriptionTestProxy(t, db, 10, 1, "SG Native", 443, relay.EntryHostAuto, "")
	for _, multiplier := range []int{10, 50, 125, 500} {
		created, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
			Name: "Node", Mode: NodeModeDirect, TargetProxyID: 10,
			TrafficMultiplierBP: multiplier, Enabled: true,
		})
		if err != nil || created.TrafficMultiplierBP != multiplier {
			t.Fatalf("create multiplier %d = %+v, error = %v", multiplier, created, err)
		}
	}
	for _, multiplier := range []int{9, 501} {
		if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
			Name: "Invalid", Mode: NodeModeDirect, TargetProxyID: 10,
			TrafficMultiplierBP: multiplier, Enabled: true,
		}); !errors.Is(err, ErrInvalidTrafficMultiplier) {
			t.Fatalf("invalid create multiplier %d error = %v", multiplier, err)
		}
	}
	created, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Mutable", Mode: NodeModeDirect, TargetProxyID: 10, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var versionBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = 1`).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	multiplier := 200
	updated, mutations, err := service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{
		TrafficMultiplierBP: &multiplier,
	})
	if err != nil || updated.TrafficMultiplierBP != 200 || len(mutations) != 0 {
		t.Fatalf("multiplier-only update = %+v, mutations = %+v, error = %v", updated, mutations, err)
	}
	var versionAfter int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = 1`).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore {
		t.Fatalf("multiplier-only update bumped desired config version from %d to %d", versionBefore, versionAfter)
	}
	invalid := 0
	if _, _, err := service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{
		TrafficMultiplierBP: &invalid,
	}); !errors.Is(err, ErrInvalidTrafficMultiplier) {
		t.Fatalf("zero update multiplier error = %v", err)
	}
}

func TestRelayPublishedNodeCreatesSharedRelayAndProtectsIt(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "HK", "198.51.100.10")
	insertSubscriptionTestServer(t, db, 2, "SG", "203.0.113.20")
	insertSubscriptionTestProxy(t, db, 10, 1, "HK CN2", 20000, relay.EntryHostManual, "hk.example.com")
	insertSubscriptionTestProxy(t, db, 20, 2, "SG Native", 443, relay.EntryHostAuto, "")
	if _, err := db.Exec(`INSERT INTO clients
		(id, proxy_id, name, credential_json, created_at, updated_at)
		VALUES (30, 20, 'reserved', '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_relay_ports (client_id, server_id, port, created_at) VALUES (30, 1, 20002, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_host, target_port, network, created_at, updated_at)
		VALUES (1, 'existing', '0.0.0.0', 20001, 'manual', 'example.com', 443, 'tcp', 1, 1)`); err != nil {
		t.Fatal(err)
	}

	sourceID := int64(10)
	created, mutation, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "🇭🇰 HK-01", Mode: NodeModeRelay, SourceProxyID: &sourceID, TargetProxyID: 20, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mutation == nil || mutation.ServerID != 1 || mutation.Version != 2 || created.RelayID == nil ||
		created.SourceServerID == nil || *created.SourceServerID != 1 || created.EntryAddress != "hk.example.com" ||
		created.EntryPort < relay.UserRelayPortStart || created.EntryPort > relay.UserRelayPortEnd {
		t.Fatalf("created relay published node = %+v, mutation = %+v", created, mutation)
	}
	for _, occupied := range []int{20000, 20001, 20002} {
		if created.EntryPort == occupied {
			t.Fatalf("shared Relay chose occupied port %d", occupied)
		}
	}

	var ownerID, sourceClientID, targetClientID sql.NullInt64
	var relayServerID, targetProxyID int64
	var relayName, entryHostMode, entryHost, network string
	if err := db.QueryRow(`SELECT server_id, owner_user_id, source_client_id, name, entry_host_mode, entry_host,
		target_proxy_id, target_client_id, network FROM relays WHERE id = ?`, *created.RelayID).
		Scan(&relayServerID, &ownerID, &sourceClientID, &relayName, &entryHostMode, &entryHost,
			&targetProxyID, &targetClientID, &network); err != nil {
		t.Fatal(err)
	}
	if relayServerID != 1 || ownerID.Valid || sourceClientID.Valid || targetClientID.Valid || targetProxyID != 20 ||
		relayName != created.Name || entryHostMode != relay.EntryHostManual || entryHost != "hk.example.com" || network != relay.NetworkTCP {
		t.Fatalf("shared Relay fields = server %d owner %v source client %v target proxy %d target client %v name %q entry %s/%q network %s",
			relayServerID, ownerID, sourceClientID, targetProxyID, targetClientID, relayName, entryHostMode, entryHost, network)
	}

	relayService := relay.NewService(db)
	storedRelay, err := relayService.Get(t.Context(), *created.RelayID)
	if err != nil || !storedRelay.SubscriptionPublished {
		t.Fatalf("stored shared Relay = %+v, error = %v", storedRelay, err)
	}
	newName := "blocked"
	if _, _, err := relayService.Update(t.Context(), *created.RelayID, relay.UpdateInput{Name: &newName}); !errors.Is(err, relay.ErrSubscriptionManaged) {
		t.Fatalf("generic Relay update error = %v", err)
	}
	if _, err := relayService.Delete(t.Context(), *created.RelayID); !errors.Is(err, relay.ErrSubscriptionManaged) {
		t.Fatalf("generic Relay delete error = %v", err)
	}
	renamed := "🇭🇰 HK Premium"
	updated, mutations, err := service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{Name: &renamed})
	if err != nil || updated.Name != renamed || len(mutations) != 1 || mutations[0].Version != 3 {
		t.Fatalf("renamed published Relay = %+v, mutations = %+v, error = %v", updated, mutations, err)
	}
	multiplier := 50
	updated, mutations, err = service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{
		TrafficMultiplierBP: &multiplier,
	})
	if err != nil || updated.TrafficMultiplierBP != 50 || len(mutations) != 0 {
		t.Fatalf("updated published Relay multiplier = %+v, mutations = %+v, error = %v", updated, mutations, err)
	}
	var versionAfterMultiplier int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = 1`).Scan(&versionAfterMultiplier); err != nil {
		t.Fatal(err)
	}
	if versionAfterMultiplier != 3 {
		t.Fatalf("relay multiplier update version = %d, want 3", versionAfterMultiplier)
	}

	desired, err := relayService.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 2 {
		t.Fatalf("desired Relays before disable = %+v, error = %v", desired, err)
	}
	disabled := false
	updated, mutations, err = service.UpdatePublishedNode(t.Context(), created.ID, UpdatePublishedNodeInput{Enabled: &disabled})
	if err != nil || updated.Enabled || len(mutations) != 1 || mutations[0].Version != 4 {
		t.Fatalf("disabled published node = %+v, mutations = %+v, error = %v", updated, mutations, err)
	}
	desired, err = relayService.ListDesired(t.Context(), db, 1)
	if err != nil || len(desired) != 1 || desired[0].ListenPort != 20001 {
		t.Fatalf("desired Relays after disable = %+v, error = %v", desired, err)
	}

	mutation, err = service.DeletePublishedNode(t.Context(), created.ID)
	if err != nil || mutation == nil || mutation.Version != 5 {
		t.Fatalf("delete relay published node mutation = %+v, error = %v", mutation, err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscription_published_nodes`, 0)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM relays WHERE id = ?`, 0, *created.RelayID)
}

func TestRelayPublishedNodeCreationRollsBackRelay(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "Source", "198.51.100.10")
	insertSubscriptionTestServer(t, db, 2, "Target", "203.0.113.20")
	insertSubscriptionTestProxy(t, db, 10, 1, "Source Proxy", 8443, relay.EntryHostAuto, "")
	insertSubscriptionTestProxy(t, db, 20, 2, "Target Proxy", 443, relay.EntryHostAuto, "")
	if _, err := db.Exec(`CREATE TRIGGER reject_published_node BEFORE INSERT ON subscription_published_nodes
		BEGIN SELECT RAISE(ABORT, 'reject published node'); END`); err != nil {
		t.Fatal(err)
	}
	sourceID := int64(10)
	if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "rollback", Mode: NodeModeRelay, SourceProxyID: &sourceID, TargetProxyID: 20, Enabled: true,
	}); err == nil {
		t.Fatal("expected published node creation failure")
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscription_published_nodes`, 0)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM relays`, 0)
	var version int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("source desired state version = %d, want 1", version)
	}
}

func newSubscriptionTestService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := NewService(db, relay.NewService(db))
	service.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return db, service
}

func insertSubscriptionTestServer(t *testing.T, db *sql.DB, id int64, name, publicIPv4 string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (?, ?, 'offline', 1, 1)`, id, name); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (?, '', '', '', '', '', '[]', '[]', ?, '', 1)`, id, publicIPv4); err != nil {
		t.Fatal(err)
	}
}

func insertSubscriptionTestProxy(t *testing.T, db *sql.DB, id, serverID int64, name string, port int, entryHostMode, entryHost string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO proxies
		(id, server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		VALUES (?, ?, ?, 'vless', ?, ?, ?, 1, '{}', 1, 1)`, id, serverID, name, port, entryHostMode, entryHost); err != nil {
		t.Fatal(err)
	}
}

func assertSubscriptionCount(t *testing.T, db *sql.DB, statement string, want int, arguments ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(statement, arguments...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", statement, got, want)
	}
}
