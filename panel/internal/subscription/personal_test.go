package subscription

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	"gopkg.in/yaml.v3"
)

func TestPersonalEndpointOverrideRemainsReadyWhenPublicIPv4IsMissing(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "target", "")
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "direct", 443)
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	host := "fallback.example.com"
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: proxy.ID, DisplayName: "auto", Enabled: true},
		{SourceType: PersonalSourceProxy, SourceID: proxy.ID, DisplayName: "manual", EntryHost: &host, Enabled: true},
		{SourceType: PersonalSourceProxy, SourceID: proxy.ID, DisplayName: "disabled", EntryHost: &host, Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if group.Nodes[0].Status != PersonalNodeEndpointUnavailable || group.Nodes[1].Status != PersonalNodeReady {
		t.Fatalf("entry availability: auto=%s manual=%s", group.Nodes[0].Status, group.Nodes[1].Status)
	}
	data, err := service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "manual" || data.Nodes[0].Address != host {
		t.Fatalf("manual fallback output: nodes=%d, error=%v", len(data.Nodes), err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM personal_subscription_nodes WHERE group_id = ? AND enabled = 1`, 2, group.ID)
}

func TestPersonalDisabledSourcesRejectNewNodesAndPreserveExisting(t *testing.T) {
	for _, sourceType := range []string{PersonalSourceProxy, PersonalSourceRelay} {
		t.Run(sourceType, func(t *testing.T) {
			db, service := newSubscriptionTestService(t)
			insertPersonalTestUser(t, db, 100, "admin", "admin")
			insertSubscriptionTestServer(t, db, 1, "source", "198.51.100.10")
			actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
			external, err := service.landings.Create(t.Context(), actor.UserID, landingstore.CreateInput{
				Name: "external", URI: "vless://uuid@healthy.example.com:443",
			})
			if err != nil {
				t.Fatal(err)
			}
			var sourceID int64
			var setEnabled func(bool)
			wantStatus, wantDetail := PersonalNodeProxyDisabled, "Proxy 已停用"
			if sourceType == PersonalSourceProxy {
				value := createSubscriptionTestRealityProxy(t, db, 1, "proxy", 443)
				sourceID = value.ID
				setEnabled = func(enabled bool) {
					t.Helper()
					if _, _, err := service.proxies.Update(t.Context(), sourceID, proxystore.UpdateInput{Enabled: &enabled}); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				value, _, err := service.relays.Create(t.Context(), relaystore.CreateInput{
					ServerID: 1, Name: "relay", ListenPort: 9000, EntryHostMode: relaystore.EntryHostAuto,
					TargetType: relaystore.TargetLanding, TargetLandingID: &external.ID, Network: relaystore.NetworkTCP, Enabled: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				sourceID = value.ID
				wantStatus, wantDetail = PersonalNodeSourceDisabled, "Relay 已停用"
				setEnabled = func(enabled bool) {
					t.Helper()
					if _, _, err := service.relays.Update(t.Context(), sourceID, relaystore.UpdateInput{Enabled: &enabled}); err != nil {
						t.Fatal(err)
					}
				}
			}
			assertCandidate := func(want bool) {
				t.Helper()
				sources, err := service.ListPersonalSubscriptionSources(t.Context(), actor, "default")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, source := range sources {
					if source.SourceType == sourceType && source.SourceID == sourceID {
						found = true
					}
				}
				if found != want {
					t.Fatalf("candidate present=%v, want %v", found, want)
				}
			}
			assertCandidate(true)
			group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{
				Name: "personal", ClientName: "default", Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			input := SetPersonalSubscriptionNodeInput{SourceType: sourceType, SourceID: sourceID, DisplayName: "selected", Enabled: true}
			fallback := SetPersonalSubscriptionNodeInput{SourceType: PersonalSourceLanding, SourceID: external.ID, DisplayName: "fallback", Enabled: true}
			group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input, fallback})
			if err != nil {
				t.Fatal(err)
			}
			nodeID, fallbackID := group.Nodes[0].ID, group.Nodes[1].ID
			setEnabled(false)
			assertCandidate(false)
			group, err = service.GetPersonalSubscription(t.Context(), actor, group.ID)
			if err != nil || len(group.Nodes) != 2 {
				t.Fatalf("retained nodes count=%d, error=%v", len(group.Nodes), err)
			}
			node := group.Nodes[0]
			if node.ID != nodeID || node.SourceType != sourceType || node.SourceID != sourceID || !node.Enabled ||
				node.Status != wantStatus || node.StatusDetail != wantDetail {
				t.Fatalf("disabled source lost its saved identity or status: id=%d status=%s", node.ID, node.Status)
			}
			data, err := service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
			if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "fallback" {
				t.Fatalf("disabled source output count=%d, error=%v", len(data.Nodes), err)
			}
			if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input}); !errors.Is(err, ErrPersonalSourceNotFound) {
				t.Fatalf("new disabled source accepted: %v", err)
			}
			input.ID = &fallbackID
			if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input}); !errors.Is(err, ErrPersonalSourceNotFound) {
				t.Fatalf("existing ID changed to disabled source: %v", err)
			}
			input.ID, fallback.ID = &nodeID, &fallbackID
			input.DisplayName = "renamed"
			group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input, fallback})
			if err != nil || len(group.Nodes) != 2 || group.Nodes[0].Status != wantStatus {
				t.Fatalf("save existing disabled node: %v", err)
			}
			assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM personal_subscription_nodes WHERE id = ?`, 1, nodeID)
			setEnabled(true)
			assertCandidate(true)
			group, err = service.GetPersonalSubscription(t.Context(), actor, group.ID)
			if err != nil || group.Nodes[0].ID != nodeID || group.Nodes[0].Status != PersonalNodeReady {
				t.Fatalf("saved source did not recover: %v", err)
			}
			data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
			if err != nil || len(data.Nodes) != 2 || data.Nodes[0].Name != "renamed" {
				t.Fatalf("recovered output count=%d, error=%v", len(data.Nodes), err)
			}
		})
	}
}

func TestPersonalGenerationSkipsUnavailableClientsAndEndpoints(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "source", "198.51.100.10")
	value := createSubscriptionTestRealityProxy(t, db, 1, "proxy", 443)
	external, err := service.landings.Create(t.Context(), 100, landingstore.CreateInput{Name: "fallback", URI: "vless://uuid@healthy.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: value.ID, DisplayName: "proxy", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: external.ID, DisplayName: "fallback", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 100, 0, 100, 0, 1, 1)`, value.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, status, serverStatus, address string
		enabled                             bool
		expiresAt, limit                    any
	}{
		{name: "ready", status: PersonalNodeReady, serverStatus: "online", address: "198.51.100.10", enabled: true},
		{name: "disabled client", status: PersonalNodeClientDisabled, serverStatus: "online", address: "198.51.100.10"},
		{name: "expired client", status: PersonalNodeClientExpired, serverStatus: "online", address: "198.51.100.10", enabled: true, expiresAt: service.now().Add(-time.Second).Unix()},
		{name: "exhausted client", status: PersonalNodeClientExhausted, serverStatus: "online", address: "198.51.100.10", enabled: true, limit: 100},
		{name: "offline server", status: PersonalNodeEndpointUnavailable, serverStatus: "offline", address: "198.51.100.10", enabled: true},
		{name: "missing entry address", status: PersonalNodeEndpointUnavailable, serverStatus: "online", enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE clients SET enabled = ?, expires_at = ?, traffic_limit_bytes = ? WHERE id = ?`,
				test.enabled, test.expiresAt, test.limit, value.Clients[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE servers SET status = ? WHERE id = 1`, test.serverStatus); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = ? WHERE server_id = 1`, test.address); err != nil {
				t.Fatal(err)
			}
			stored, err := service.GetPersonalSubscription(t.Context(), actor, group.ID)
			if err != nil || len(stored.Nodes) != 2 || stored.Nodes[0].Status != test.status || !stored.Nodes[0].Enabled {
				t.Fatalf("stored node status or enabled changed: %v", err)
			}
			data, err := service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
			wantCount := 1
			if test.status == PersonalNodeReady {
				wantCount = 2
			}
			if err != nil || len(data.Nodes) != wantCount || data.Nodes[wantCount-1].Name != "fallback" {
				t.Fatalf("generated node count=%d, want %d, error=%v", len(data.Nodes), wantCount, err)
			}
		})
	}
}

func TestPruneDeletedPersonalSourcesKeepsDisabledSourcesAndRepairsHistoricalOrphans(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "source", "198.51.100.10")
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "proxy", 443)
	external, err := service.landings.Create(t.Context(), 100, landingstore.CreateInput{Name: "external", URI: "vless://uuid@external.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	local, _, err := service.relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: 1, Name: "local", ListenPort: 9000, EntryHostMode: relaystore.EntryHostAuto,
		TargetType: relaystore.TargetLanding, TargetLandingID: &external.ID, Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: proxy.ID, DisplayName: "proxy", Enabled: true},
		{SourceType: PersonalSourceRelay, SourceID: local.ID, DisplayName: "relay", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: external.ID, DisplayName: "external", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce an orphan left by a deletion before reference cleanup existed.
	if _, err := db.Exec(`INSERT INTO personal_subscription_nodes
		(group_id, source_type, source_id, display_name, enabled, position, created_at, updated_at)
		VALUES (?, 'landing', 99999, 'orphan', 1, 4, 1, 1)`, group.ID); err != nil {
		t.Fatal(err)
	}
	preset, err := service.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetPersonalSubscription(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(stored.Nodes))
	for _, node := range stored.Nodes {
		ids = append(ids, node.ID)
	}
	key := preset.Groups[0].Key
	if _, err := service.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, group.ID, RoutingBindings{key: ids}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE proxies SET enabled = 0 WHERE id = ?`, proxy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE relays SET enabled = 0 WHERE id = ?`, local.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := PruneDeletedPersonalSubscriptionSourcesTx(t.Context(), tx, service.now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err = service.GetPersonalSubscription(t.Context(), actor, group.ID)
	if err != nil || len(stored.Nodes) != 3 || len(stored.RoutingBindings[key]) != 3 {
		t.Fatalf("cleanup removed existing sources or retained an orphan: %v", err)
	}
	for i, node := range stored.Nodes {
		if node.ID != ids[i] || !node.Enabled || stored.RoutingBindings[key][i] != ids[i] {
			t.Fatal("cleanup changed an existing node or binding")
		}
	}
	if stored.Nodes[0].Status != PersonalNodeProxyDisabled || stored.Nodes[1].Status != PersonalNodeSourceDisabled {
		t.Fatal("cleanup lost disabled source status")
	}
}

func TestPersonalSubscriptionCRUDOwnerAndToken(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin-a", "admin")
	insertPersonalTestUser(t, db, 101, "vip-b", "vip")
	admin := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	vip := PersonalSubscriptionActor{UserID: 101, Role: "vip"}

	created, err := service.CreatePersonalSubscription(t.Context(), admin, CreatePersonalSubscriptionInput{
		Name: " 我的日常 ", SubscriptionTitle: " 日常订阅 ", ClientName: " admin ", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "我的日常" || created.SubscriptionTitle != "日常订阅" || created.ClientName != "admin" ||
		created.Token == "" || created.RoutingPresetID == 0 {
		t.Fatalf("created personal subscription = %+v", created)
	}
	second, err := service.CreatePersonalSubscription(t.Context(), admin, CreatePersonalSubscriptionInput{
		Name: "备用", ClientName: "admin", Enabled: true,
	})
	if err != nil || second.Token == created.Token {
		t.Fatalf("second personal subscription = %+v, %v", second, err)
	}
	if _, err := service.GetPersonalSubscription(t.Context(), vip, created.ID); !errors.Is(err, ErrPersonalSubscriptionNotFound) {
		t.Fatalf("VIP read another owner = %v", err)
	}
	updatedName := "工作"
	updatedClient := "work-client"
	updated, err := service.UpdatePersonalSubscription(t.Context(), admin, created.ID, UpdatePersonalSubscriptionInput{
		Name: &updatedName, ClientName: &updatedClient,
	})
	if err != nil || updated.Name != updatedName || updated.ClientName != updatedClient {
		t.Fatalf("updated personal subscription = %+v, %v", updated, err)
	}
	regenerated, err := service.RegeneratePersonalSubscriptionToken(t.Context(), admin, created.ID)
	if err != nil || regenerated.Token == created.Token {
		t.Fatalf("regenerated token = %q, %v", regenerated.Token, err)
	}
	if _, err := service.GeneratePersonalSubscriptionData(t.Context(), created.Token); !errors.Is(err, ErrPersonalSubscriptionNotFound) {
		t.Fatalf("old personal token error = %v", err)
	}
	if err := service.DeletePersonalSubscription(t.Context(), admin, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetPersonalSubscription(t.Context(), admin, created.ID); !errors.Is(err, ErrPersonalSubscriptionNotFound) {
		t.Fatalf("deleted personal subscription error = %v", err)
	}
}

func TestPersonalClientMatchingSecurityAndLifecycle(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertPersonalTestUser(t, db, 101, "vip-a", "vip")
	insertPersonalTestUser(t, db, 102, "vip-b", "vip")
	insertPersonalTestUser(t, db, 103, "subscriber", "subscriber")
	insertSubscriptionTestServer(t, db, 1, "Tokyo", "1.1.1.1")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "Tokyo Proxy", 443)
	proxies := proxystore.NewService(db)
	client, _, err := proxies.CreateClient(t.Context(), proxyValue.ID, proxystore.ClientCreateInput{
		Name: "shared-name", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	vipA := PersonalSubscriptionActor{UserID: 101, Role: "vip"}

	if _, status, _, err := service.matchPersonalClient(t.Context(), admin, proxyValue.ID, "missing"); err != nil || status != PersonalNodeMissing {
		t.Fatalf("missing match = %q, %v", status, err)
	}
	if _, status, _, err := service.matchPersonalClient(t.Context(), admin, proxyValue.ID, "shared-name"); err != nil || status != PersonalNodeReady {
		t.Fatalf("ready match = %q, %v", status, err)
	}
	duplicate, _, err := proxies.CreateClient(t.Context(), proxyValue.ID, proxystore.ClientCreateInput{Name: "shared-name", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := service.matchPersonalClient(t.Context(), admin, proxyValue.ID, "shared-name"); err != nil || status != PersonalNodeAmbiguous {
		t.Fatalf("ambiguous match = %q, %v", status, err)
	}
	if _, err := db.Exec(`UPDATE clients SET name = 'other' WHERE id = ?`, duplicate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clients SET assigned_user_id = 102 WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := service.matchPersonalClient(t.Context(), admin, proxyValue.ID, "shared-name"); err != nil || status != PersonalNodeMissing {
		t.Fatalf("admin matched another user = %q, %v", status, err)
	}
	if _, err := db.Exec(`UPDATE clients SET assigned_user_id = 101 WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	otherVIP, _, err := proxies.CreateClient(t.Context(), proxyValue.ID, proxystore.ClientCreateInput{Name: "shared-name", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clients SET assigned_user_id = 102 WHERE id = ?`, otherVIP.ID); err != nil {
		t.Fatal(err)
	}
	if matched, status, _, err := service.matchPersonalClient(t.Context(), vipA, proxyValue.ID, "shared-name"); err != nil ||
		status != PersonalNodeReady || matched.ID != client.ID {
		t.Fatalf("VIP isolated match = client %d status %q, %v", matched.ID, status, err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_profiles
		(user_id, enabled, subscription_token, created_at, updated_at) VALUES (103, 1, 'managed-token', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_clients
		(user_id, proxy_id, client_id, created_at) VALUES (103, ?, ?, 1)`, proxyValue.ID, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := service.matchPersonalClient(t.Context(), vipA, proxyValue.ID, "shared-name"); err != nil || status != PersonalNodeMissing {
		t.Fatalf("managed client match = %q, %v", status, err)
	}
	if _, err := db.Exec(`DELETE FROM subscriber_clients WHERE client_id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clients SET enabled = 0 WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, _, _ := service.matchPersonalClient(t.Context(), vipA, proxyValue.ID, "shared-name"); status != PersonalNodeClientDisabled {
		t.Fatalf("disabled status = %q", status)
	}
	if _, err := db.Exec(`UPDATE clients SET enabled = 1, expires_at = ? WHERE id = ?`, service.now().Add(-time.Second).Unix(), client.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, _, _ := service.matchPersonalClient(t.Context(), vipA, proxyValue.ID, "shared-name"); status != PersonalNodeClientExpired {
		t.Fatalf("expired status = %q", status)
	}
	if _, err := db.Exec(`UPDATE clients SET expires_at = NULL, traffic_limit_bytes = 100 WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 100, 0, 100, 0, 1, 1)`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, status, _, _ := service.matchPersonalClient(t.Context(), vipA, proxyValue.ID, "shared-name"); status != PersonalNodeClientExhausted {
		t.Fatalf("exhausted status = %q", status)
	}
}

func TestPersonalSubscriptionResolvesAllSourcesNamesAndOrder(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "US", "1.1.1.1")
	insertSubscriptionTestServer(t, db, 2, "JP Relay", "2.2.2.2")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "US Proxy", 443)
	proxies := proxystore.NewService(db)
	client, _, err := proxies.CreateClient(t.Context(), proxyValue.ID, proxystore.ClientCreateInput{Name: "admin", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	landings := landingstore.NewService(db)
	vlessLanding, err := landings.Create(t.Context(), 100, landingstore.CreateInput{
		Name: "UK VLESS", Visibility: landingstore.VisibilityPrivate,
		URI: "vless://landing-uuid@uk.example.com:8443?type=tcp&security=reality&sni=www.example.com&pbk=public&sid=abcd&fp=chrome#Old",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetClientID := client.ID
	relays := relaystore.NewService(db)
	proxyRelay, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: 2, Name: "US via JP", ListenPort: 20000, EntryHostMode: relaystore.EntryHostAuto,
		TargetType: relaystore.TargetProxy, TargetProxyID: &proxyValue.ID, TargetClientID: &targetClientID,
		Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	landingRelay, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: 2, Name: "UK via JP", ListenPort: 20001, EntryHostMode: relaystore.EntryHostAuto,
		TargetType: relaystore.TargetLanding, TargetLandingID: &vlessLanding.ID,
		Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manualRelay, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: 2, Name: "Manual only", ListenPort: 20002, EntryHostMode: relaystore.EntryHostAuto,
		TargetType: relaystore.TargetManual, TargetHost: "manual.example.com", TargetPort: 443,
		Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ssLanding, err := landings.Create(t.Context(), 100, landingstore.CreateInput{
		Name: "SG SS", Visibility: landingstore.VisibilityPrivate,
		URI: "ss://aes-256-gcm:secret@sg.example.com:8388#Old",
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{
		Name: "Daily", SubscriptionTitle: "My Nodes", ClientName: "admin", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := service.ListPersonalSubscriptionSources(t.Context(), actor, group.ClientName)
	if err != nil {
		t.Fatal(err)
	}
	sourceByKey := make(map[string]PersonalSubscriptionSource, len(sources))
	for _, source := range sources {
		if source.SourceType == "published" {
			t.Fatalf("published source leaked into personal subscription candidates: %+v", source)
		}
		sourceByKey[personalSourceKey(source.SourceType, source.SourceID)] = source
	}
	if sourceByKey[personalSourceKey(PersonalSourceRelay, proxyRelay.ID)].RequiresClient != true ||
		sourceByKey[personalSourceKey(PersonalSourceRelay, landingRelay.ID)].RequiresClient ||
		sourceByKey[personalSourceKey(PersonalSourceProxy, proxyValue.ID)].RequiresClient != true {
		t.Fatalf("personal source client requirements = %+v", sourceByKey)
	}
	if _, exists := sourceByKey[personalSourceKey(PersonalSourceRelay, manualRelay.ID)]; exists {
		t.Fatalf("manual relay must not be a personal subscription candidate: %+v", sourceByKey)
	}
	if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceRelay, SourceID: manualRelay.ID, DisplayName: "Manual bypass", Enabled: true},
	}); !errors.Is(err, ErrPersonalSourceNotFound) {
		t.Fatalf("manual relay bypass error = %v", err)
	}
	proxyHost, proxyPort := "proxy-alt.example.com", 1443
	relayHost, relayPort := "2001:db8::2", 2443
	landingHost, landingPort := "2001:db8::3", 3443
	requested := []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: proxyValue.ID, DisplayName: "1 US Proxy", Enabled: true},
		{SourceType: PersonalSourceProxy, SourceID: proxyValue.ID, DisplayName: "2 US Proxy Alt", Enabled: true,
			EntryHost: &proxyHost, EntryPort: &proxyPort},
		{SourceType: PersonalSourceRelay, SourceID: proxyRelay.ID, DisplayName: "3 US via JP", Enabled: true},
		{SourceType: PersonalSourceRelay, SourceID: proxyRelay.ID, DisplayName: "4 US via JP v6", Enabled: true,
			EntryHost: &relayHost, EntryPort: &relayPort},
		{SourceType: PersonalSourceLanding, SourceID: vlessLanding.ID, DisplayName: "5 UK Home", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: vlessLanding.ID, DisplayName: "6 UK Home v6", Enabled: true,
			EntryHost: &landingHost, EntryPort: &landingPort},
		{SourceType: PersonalSourceRelay, SourceID: landingRelay.ID, DisplayName: "7 UK via JP", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: ssLanding.ID, DisplayName: "8 SG SS", Enabled: true},
	}
	stored, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, requested)
	if err != nil || len(stored.Nodes) != len(requested) || stored.Nodes[0].Position != 1 ||
		stored.Nodes[len(stored.Nodes)-1].Position != len(requested) || stored.Nodes[1].EntryHost == nil ||
		*stored.Nodes[1].EntryHost != proxyHost || stored.Nodes[1].EntryPort == nil || *stored.Nodes[1].EntryPort != proxyPort {
		t.Fatalf("stored personal nodes = %+v, %v", stored.Nodes, err)
	}
	data, err := service.GeneratePersonalSubscriptionDataForOwner(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != len(requested) {
		t.Fatalf("resolved nodes = %+v", data.Nodes)
	}
	for index, input := range requested {
		if data.Nodes[index].Name != input.DisplayName {
			t.Fatalf("resolved order[%d] = %q, want %q", index, data.Nodes[index].Name, input.DisplayName)
		}
	}
	if data.Nodes[0].Address != "1.1.1.1" || data.Nodes[0].Port != 443 ||
		data.Nodes[1].Address != proxyHost || data.Nodes[1].Port != proxyPort ||
		data.Nodes[2].Address != "2.2.2.2" || data.Nodes[2].Port != 20000 ||
		data.Nodes[3].Address != relayHost || data.Nodes[3].Port != relayPort ||
		data.Nodes[4].Address != "uk.example.com" || data.Nodes[4].Port != 8443 ||
		data.Nodes[5].Address != landingHost || data.Nodes[5].Port != landingPort ||
		data.Nodes[6].Address != "2.2.2.2" || data.Nodes[6].Port != 20001 {
		t.Fatalf("resolved endpoints = %+v", data.Nodes)
	}
	decoded, err := base64.StdEncoding.DecodeString(RenderResolvedBase64Subscription(data.Nodes))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(decoded), "\n")
	if len(lines) != len(requested) {
		t.Fatalf("Base64 lines = %q", decoded)
	}
	for index, line := range lines {
		if fragment := personalTestURIFragment(t, line); fragment != requested[index].DisplayName {
			t.Fatalf("line %d fragment = %q", index, fragment)
		}
	}
	mihomo, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Proxies []struct {
			Name string `yaml:"name"`
			Flow string `yaml:"flow"`
		} `yaml:"proxies"`
		Groups []struct {
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Providers map[string]any `yaml:"rule-providers"`
		Rules     []string       `yaml:"rules"`
	}
	if err := yaml.Unmarshal(mihomo, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Proxies) != len(requested) || config.Proxies[0].Name != requested[0].DisplayName || len(config.Providers) == 0 || len(config.Rules) == 0 {
		t.Fatalf("Mihomo config = proxies %+v providers %d rules %d", config.Proxies, len(config.Providers), len(config.Rules))
	}
	if config.Proxies[4].Flow != "" || config.Proxies[5].Flow != "" || config.Proxies[6].Flow != "" {
		t.Fatalf("landing VLESS flows = %q/%q/%q, want URI values to remain empty",
			config.Proxies[4].Flow, config.Proxies[5].Flow, config.Proxies[6].Flow)
	}
	if !containsString(config.Groups[0].Proxies, requested[0].DisplayName) {
		t.Fatalf("Mihomo first group = %+v", config.Groups[0].Proxies)
	}
	wantShare, err := proxies.GetClientShareAtEndpointWithOptions(t.Context(), client.ID, proxystore.ShareEndpoint{
		Address: proxyHost, Port: proxyPort,
	}, proxystore.ShareOptions{DisplayName: requested[1].DisplayName})
	if err != nil || data.Nodes[1].URI != wantShare.URI {
		t.Fatalf("proxy share = %q, want %q, %v", data.Nodes[1].URI, wantShare.URI, err)
	}
}

func TestPersonalSubscriptionSkipsUnavailableNodesAndErrorsWhenEmpty(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "One", "1.1.1.1")
	insertSubscriptionTestServer(t, db, 2, "Two", "2.2.2.2")
	readyProxy := createSubscriptionTestRealityProxy(t, db, 1, "Ready", 443)
	missingProxy := createSubscriptionTestRealityProxy(t, db, 2, "Missing", 8443)
	client, _, err := proxystore.NewService(db).CreateClient(t.Context(), readyProxy.ID,
		proxystore.ClientCreateInput{Name: "admin", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{
		Name: "Mixed", ClientName: "admin", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: missingProxy.ID, DisplayName: "Missing", Enabled: true},
		{SourceType: PersonalSourceProxy, SourceID: readyProxy.ID, DisplayName: "Ready", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := service.GetRoutingPreset(t.Context(), stored.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	groupKey := preset.Groups[0].Key
	stored, err = service.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, group.ID,
		RoutingBindings{groupKey: {stored.Nodes[0].ID, stored.Nodes[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalSubscriptionDataForOwner(t.Context(), actor, group.ID)
	if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "Ready" {
		t.Fatalf("partially available data = %+v, %v", data.Nodes, err)
	}
	if len(data.NodeNames) != 1 || data.NodeNames[stored.Nodes[1].ID] != "Ready" ||
		len(data.RoutingBindings[groupKey]) != 2 {
		t.Fatalf("unavailable binding was not retained: names=%+v bindings=%+v", data.NodeNames, data.RoutingBindings)
	}
	mihomo, err := RenderPersonalMihomoSubscription(data)
	if err != nil || strings.Contains(string(mihomo), "- Missing\n") || !strings.Contains(string(mihomo), "- Ready\n") {
		t.Fatalf("unavailable personal binding render = %v\n%s", err, mihomo)
	}
	if _, err := db.Exec(`UPDATE clients SET name = 'removed' WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GeneratePersonalSubscriptionDataForOwner(t.Context(), actor, group.ID); !errors.Is(err, ErrPersonalSubscriptionEmpty) {
		t.Fatalf("empty personal subscription error = %v", err)
	}
}

func TestPersonalNodeIDsStayStableAndBindingsAreScopedAndPruned(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	landings := landingstore.NewService(db)
	firstSource, err := landings.Create(t.Context(), actor.UserID, landingstore.CreateInput{
		Name: "First", Visibility: landingstore.VisibilityPrivate,
		URI: "ss://aes-256-gcm:first@first.example.com:8388#First",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := landings.Create(t.Context(), actor.UserID, landingstore.CreateInput{
		Name: "Second", Visibility: landingstore.VisibilityPrivate,
		URI: "ss://aes-256-gcm:second@second.example.com:8388#Second",
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Bound", Enabled: true,
		Groups: []RoutingGroup{{Name: "Selected", Type: "select"}},
		Rules:  []string{"MATCH,Selected"},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstGroup, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{
		Name: "First", ClientName: "admin", Enabled: true, RoutingPresetID: &preset.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondGroup, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{
		Name: "Second", ClientName: "admin", Enabled: true, RoutingPresetID: &preset.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstGroup, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, firstGroup.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceLanding, SourceID: firstSource.ID, DisplayName: "A", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: secondSource.ID, DisplayName: "B", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondGroup, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, secondGroup.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceLanding, SourceID: secondSource.ID, DisplayName: "Other", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	if _, err := service.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, firstGroup.ID,
		RoutingBindings{key: {secondGroup.Nodes[0].ID}}); !errors.Is(err, ErrInvalidRoutingBindings) {
		t.Fatalf("other personal subscription binding error = %v", err)
	}
	firstGroup, err = service.SetPersonalSubscriptionRoutingBindings(t.Context(), actor, firstGroup.ID,
		RoutingBindings{key: {firstGroup.Nodes[0].ID, firstGroup.Nodes[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	stableID := firstGroup.Nodes[0].ID
	removedID := firstGroup.Nodes[1].ID
	firstGroup, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, firstGroup.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceLanding, SourceID: secondSource.ID, DisplayName: "New", Enabled: true},
		{ID: &stableID, SourceType: PersonalSourceLanding, SourceID: firstSource.ID, DisplayName: "A renamed", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstGroup.Nodes) != 2 || firstGroup.Nodes[1].ID != stableID || firstGroup.Nodes[0].ID == stableID ||
		firstGroup.Nodes[0].ID == removedID || firstGroup.Nodes[1].DisplayName != "A renamed" {
		t.Fatalf("stable/new personal node IDs = %+v", firstGroup.Nodes)
	}
	if got := firstGroup.RoutingBindings[key]; len(got) != 1 || got[0] != stableID {
		t.Fatalf("personal bindings after node deletion = %+v", firstGroup.RoutingBindings)
	}
}

func insertPersonalTestUser(t *testing.T, db *sql.DB, id int64, username, role string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (?, ?, 'hash', ?, 1, 1)`, id, username, role); err != nil {
		t.Fatal(err)
	}
}

func personalTestURIFragment(t *testing.T, raw string) string {
	t.Helper()
	value, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value.Fragment
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
