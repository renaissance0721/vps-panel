package api

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestTemporaryAvailabilityPreservesConfiguredEnabledAndRecovers(t *testing.T) {
	f := newProxyDeleteFixture(t)
	servers := serverstore.NewService(f.db)
	agents := agentcontrol.NewService(f.db, time.Now)
	relays := relaystore.NewService(f.db)
	createServer := func(name, address string) agentcontrol.RegisteredAgent {
		t.Helper()
		created, err := servers.CreateForUser(t.Context(), name, serverstore.VisibilityPublic, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		agent, err := agents.RegisterAgent(t.Context(), created.EnrollmentToken, "test", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := agents.SetAgentConnected(t.Context(), agent.ID, agent.ServerID); err != nil {
			t.Fatal(err)
		}
		if _, err := servers.ReportSystemInfo(t.Context(), agent.ID, agent.ServerID, serverstore.SystemInfoReport{PublicIPv4: address}); err != nil {
			t.Fatal(err)
		}
		return agent
	}
	source := createServer("source", "198.51.100.10")
	target := createServer("target", "217.116.172.208")
	proxy := f.createProxy(t, target.ServerID, 443, "target")
	local, _, err := relays.Create(t.Context(), relaystore.CreateInput{
		ServerID: source.ServerID, Name: "local", ListenPort: 9000, EntryHostMode: relaystore.EntryHostAuto,
		TargetType: relaystore.TargetProxy, TargetProxyID: &proxy.ID, TargetClientID: &proxy.Clients[0].ID,
		Network: relaystore.NetworkTCP, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	published, _, err := f.subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: "published", Mode: subscriptionstore.NodeModeRelay, SourceServerID: &source.ServerID, TargetProxyID: proxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{Name: "plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.subscriptions.SetPlanNodes(t.Context(), plan.ID, []int64{published.ID}); err != nil {
		t.Fatal(err)
	}
	_, userID := registerAccount(t, f.db, f.handler, f.cookie, "subscriber", "subscriber")
	if _, _, err := f.subscriptions.UpdateSubscriber(t.Context(), userID, subscriptionstore.UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	var subscriberClientID int64
	if err := f.db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = ?`, userID).Scan(&subscriberClientID); err != nil {
		t.Fatal(err)
	}
	actor := subscriptionstore.PersonalSubscriptionActor{UserID: 1, Role: "admin"}
	group, err := f.subscriptions.CreatePersonalSubscription(t.Context(), actor, subscriptionstore.CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	external, err := landingstore.NewService(f.db).Create(t.Context(), actor.UserID, landingstore.CreateInput{Name: "healthy", URI: "vless://uuid@healthy.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []subscriptionstore.SetPersonalSubscriptionNodeInput{
		{SourceType: "proxy", SourceID: proxy.ID, DisplayName: "direct", Enabled: true},
		{SourceType: "relay", SourceID: local.ID, DisplayName: "relay", Enabled: true},
		{SourceType: "landing", SourceID: external.ID, DisplayName: "healthy", Enabled: true},
		{SourceType: "proxy", SourceID: proxy.ID, DisplayName: "disabled", Enabled: false},
	}
	group, err = f.subscriptions.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, inputs)
	if err != nil {
		t.Fatal(err)
	}
	assertPersonal := func(status string, names ...string) {
		t.Helper()
		stored, err := f.subscriptions.GetPersonalSubscription(t.Context(), actor, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i, node := range stored.Nodes {
			if node.Enabled != inputs[i].Enabled || (i < 2 && status != "" && node.Status != status) {
				t.Fatalf("personal node %q: enabled=%v status=%s", node.DisplayName, node.Enabled, node.Status)
			}
		}
		data, err := f.subscriptions.GeneratePersonalSubscriptionData(t.Context(), group.Token)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(data.Nodes))
		for _, node := range data.Nodes {
			got = append(got, node.Name)
		}
		if !reflect.DeepEqual(got, names) {
			t.Fatalf("personal output=%v, want %v", got, names)
		}
	}
	assertDesired := func(agent agentcontrol.RegisteredAgent, proxies, relayCount int) {
		t.Helper()
		state, err := agents.GetDesiredState(t.Context(), agent.ID, agent.ServerID)
		if err != nil || len(state.Proxies) != proxies || len(state.Relays) != relayCount {
			t.Fatalf("desired counts proxies=%d relays=%d: %+v, %v", proxies, relayCount, state, err)
		}
	}
	assertPreserved := func() {
		t.Helper()
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM proxies WHERE id = ? AND enabled = 1`, 1, proxy.ID)
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM relays WHERE server_id = ? AND enabled = 1`, 2, source.ServerID)
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM subscription_published_nodes WHERE id = ? AND enabled = 1`, 1, published.ID)
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM personal_subscription_nodes WHERE group_id = ? AND enabled = 1`, 3, group.ID)
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = ? AND client_id = ?`, 1, userID, subscriberClientID)
	}
	report := func(agent agentcontrol.RegisteredAgent, address string) {
		t.Helper()
		changed, err := servers.ReportSystemInfo(t.Context(), agent.ID, agent.ServerID,
			serverstore.SystemInfoReport{IPv4: []string{"217.116.172.208"}, PublicIPv4: address})
		if err != nil || !changed {
			t.Fatalf("system info changed=%v, error=%v", changed, err)
		}
		if mutations, err := relays.BumpForAutoTargetServer(t.Context(), agent.ServerID); err != nil || (agent.ServerID == target.ServerID && len(mutations) != 1) {
			t.Fatalf("address dependency notification: %v, %v", mutations, err)
		}
	}
	assertShared := func(available bool) {
		t.Helper()
		data, _, err := f.subscriptions.GenerateSubscriptionDataForUser(t.Context(), userID)
		if available && (err != nil || len(data.Nodes) != 1) {
			t.Fatalf("shared subscription did not recover: nodes=%d, %v", len(data.Nodes), err)
		}
		if !available && err == nil {
			t.Fatal("shared subscription with missing source address was generated")
		}
	}

	assertDesired(source, 0, 2)
	assertPersonal(subscriptionstore.PersonalNodeReady, "direct", "relay", "healthy")
	assertShared(true)
	for _, agent := range []agentcontrol.RegisteredAgent{target, source} {
		if err := agents.SetAgentOffline(t.Context(), agent.ServerID); err != nil {
			t.Fatal(err)
		}
	}
	assertDesired(target, 1, 0) // Reconnection can fetch the configured Proxy even while status is offline.
	assertDesired(source, 0, 2)
	assertPersonal(subscriptionstore.PersonalNodeEndpointUnavailable, "healthy")
	assertPreserved()
	for _, agent := range []agentcontrol.RegisteredAgent{target, source} {
		if err := agents.SetAgentConnected(t.Context(), agent.ID, agent.ServerID); err != nil {
			t.Fatal(err)
		}
	}
	assertPersonal(subscriptionstore.PersonalNodeReady, "direct", "relay", "healthy")

	report(target, "") // Interface IPv4 exists, but public IPv4 detection failed.
	for range 2 {
		assertDesired(source, 0, 0)
		assertDesired(target, 1, 0)
		assertPersonal(subscriptionstore.PersonalNodeEndpointUnavailable, "healthy")
		assertPreserved()
	}
	report(target, "217.116.172.208")
	assertDesired(source, 0, 2)
	assertPersonal(subscriptionstore.PersonalNodeReady, "direct", "relay", "healthy")
	assertShared(true)

	report(source, "")
	assertDesired(source, 0, 2) // Missing entry IP does not invalidate Realm's listener/target configuration.
	assertShared(false)
	assertPreserved()
	report(source, "198.51.100.10")
	assertShared(true)
	assertPreserved()

	// Explicitly disabled configuration must survive the same failure/recovery cycle.
	response := performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/proxies/%d", proxy.ID), map[string]bool{"enabled": false}, f.cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("disable Proxy: %d %s", response.Code, response.Body.String())
	}
	if _, _, err := relays.Update(t.Context(), local.ID, relaystore.UpdateInput{Enabled: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.subscriptions.UpdatePublishedNode(t.Context(), published.ID, subscriptionstore.UpdatePublishedNodeInput{Enabled: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	report(target, "")
	assertDesired(source, 0, 0)
	report(target, "217.116.172.208")
	assertDesired(source, 0, 0)
	assertDesired(target, 0, 0)
	assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM proxies WHERE id = ? AND enabled = 0`, 1, proxy.ID)
	assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM relays WHERE server_id = ? AND enabled = 0`, 2, source.ServerID)
	assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM subscription_published_nodes WHERE id = ? AND enabled = 0`, 1, published.ID)
	assertPersonal("", "healthy")
}

func TestProxyEnableChangeNotifiesRelayDependency(t *testing.T) {
	f := newProxyDeleteFixture(t)
	targetID, sourceID := f.createServer(t, "target"), f.createServer(t, "source")
	proxy := f.createProxy(t, targetID, 443, "target")
	if _, _, err := f.proxies.Update(t.Context(), proxy.ID, proxystore.UpdateInput{EntryHostMode: stringPointer("manual"), EntryHost: stringPointer("target.example.com")}); err != nil {
		t.Fatal(err)
	}
	relays := relaystore.NewService(f.db)
	if _, _, err := relays.Create(t.Context(), relaystore.CreateInput{ServerID: sourceID, Name: "local", ListenPort: 9000,
		TargetType: relaystore.TargetProxy, TargetProxyID: &proxy.ID, TargetClientID: &proxy.Clients[0].ID, Network: relaystore.NetworkTCP, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, sourceID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		response := performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/proxies/%d", proxy.ID), map[string]bool{"enabled": enabled}, f.cookie)
		if response.Code != http.StatusOK {
			t.Fatalf("toggle target Proxy: %d %s", response.Code, response.Body.String())
		}
		version++
		var got int64
		if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, sourceID).Scan(&got); err != nil || got != version {
			t.Fatalf("Relay source version=%d, want %d, error=%v", got, version, err)
		}
		desired, err := relays.ListDesired(t.Context(), f.db, sourceID)
		if err != nil || (len(desired) == 1) != enabled {
			t.Fatalf("target enabled=%v: desired=%v error=%v", enabled, desired, err)
		}
		assertDatabaseCount(t, f.db, `SELECT COUNT(*) FROM relays WHERE server_id = ? AND enabled = 1`, 1, sourceID)
	}
}
