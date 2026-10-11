package subscription

import (
	"errors"
	"strings"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"github.com/renaissance0721/vps-panel/panel/internal/noderole"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestPersonalNodeEligibilityAndLegacyReferences(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	insertSubscriptionTestServer(t, db, 1, "target", "203.0.113.1")
	insertSubscriptionTestServer(t, db, 2, "source", "203.0.113.2")
	proxies, landings, relays := proxystore.NewService(db), landingstore.NewService(db), relaystore.NewService(db)
	direct := createSubscriptionTestRealityProxy(t, db, 1, "direct", 443)
	landing := createSubscriptionTestRealityProxy(t, db, 1, "landing", 444)
	landingRole := noderole.Landing
	if _, _, err := proxies.Update(t.Context(), landing.ID, proxystore.UpdateInput{NodeRole: &landingRole}); err != nil {
		t.Fatal(err)
	}
	externalDirect, err := landings.Create(t.Context(), actor.UserID, landingstore.CreateInput{Name: "external-direct", URI: "vless://uuid@direct.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	externalLanding, err := landings.Create(t.Context(), actor.UserID, landingstore.CreateInput{Name: "external-landing", NodeRole: noderole.Landing, URI: "vless://uuid@landing.example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	local, _, err := relays.Create(t.Context(), relaystore.CreateInput{ServerID: 2, Name: "local", ListenPort: 9000,
		EntryHostMode: relaystore.EntryHostAuto, TargetType: relaystore.TargetLanding, TargetLandingID: &externalLanding.ID,
		Network: relaystore.NetworkTCP, Enabled: true})
	if err != nil || local.SubscriptionPublished {
		t.Fatalf("local relay creation: %v", err)
	}
	// Both direct and landing managed targets retain their existing published topology.
	if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{Name: "published-direct", Mode: NodeModeDirect, TargetProxyID: direct.ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sourceID := int64(2)
	published, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{Name: "published-relay", Mode: NodeModeRelay, SourceServerID: &sourceID, TargetProxyID: landing.ID, Enabled: true})
	if err != nil || published.RelayID == nil {
		t.Fatalf("published relay creation: %v", err)
	}
	publishedRelay, err := relays.Get(t.Context(), *published.RelayID)
	if err != nil || !publishedRelay.SubscriptionPublished {
		t.Fatalf("published relay flag: %v", err)
	}
	for _, target := range []int64{direct.ID, landing.ID} {
		value, err := proxies.Get(t.Context(), target)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := relays.Create(t.Context(), relaystore.CreateInput{ServerID: 2, Name: "target-check", ListenPort: 9100 + int(target), EntryHostMode: relaystore.EntryHostAuto, TargetType: relaystore.TargetProxy, TargetProxyID: &target, TargetClientID: &value.Clients[0].ID, Network: relaystore.NetworkTCP, Enabled: true}); err != nil {
			t.Fatalf("managed relay target: %v", err)
		}
	}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	allowed := []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: direct.ID, DisplayName: "managed", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: externalDirect.ID, DisplayName: "external", Enabled: true},
		{SourceType: PersonalSourceRelay, SourceID: local.ID, DisplayName: "relay", Enabled: true},
	}
	forbidden := []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: landing.ID, DisplayName: "forbidden-managed", Enabled: true},
		{SourceType: PersonalSourceLanding, SourceID: externalLanding.ID, DisplayName: "forbidden-external", Enabled: true},
		{SourceType: PersonalSourceRelay, SourceID: publishedRelay.ID, DisplayName: "forbidden-published", Enabled: true},
	}
	sources, err := service.ListPersonalSubscriptionSources(t.Context(), actor, "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range append(append([]SetPersonalSubscriptionNodeInput{}, allowed...), forbidden...) {
		found := false
		for _, source := range sources {
			if source.SourceType == input.SourceType && source.SourceID == input.SourceID {
				found = true
			}
		}
		want := !strings.HasPrefix(input.DisplayName, "forbidden")
		if found != want {
			t.Fatalf("candidate %s found=%v", input.DisplayName, found)
		}
	}
	for _, input := range forbidden {
		if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input}); !errors.Is(err, ErrPersonalSourceNotFound) {
			t.Fatalf("bypass accepted %s: %v", input.DisplayName, err)
		}
	}
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, allowed)
	if err != nil || len(group.Nodes) != 3 {
		t.Fatalf("allowed sources: %v", err)
	}
	if _, _, err := proxies.Update(t.Context(), direct.ID, proxystore.UpdateInput{NodeRole: &landingRole}); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := landings.Update(t.Context(), externalDirect.ID, actor.UserID, landingstore.UpdateInput{NodeRole: &landingRole}); err != nil || changed {
		t.Fatalf("external role: %v", err)
	}
	// Emulate an existing accidental reference without allowing it through the new API.
	if _, err := db.Exec(`INSERT INTO personal_subscription_nodes (group_id, source_type, source_id, display_name, enabled, position, created_at, updated_at) VALUES (?, 'relay', ?, 'legacy-published', 1, 4, 1, 1)`, group.ID, publishedRelay.ID); err != nil {
		t.Fatal(err)
	}
	group, err = service.GetPersonalSubscription(t.Context(), actor, group.ID)
	if err != nil || len(group.Nodes) != 4 {
		t.Fatalf("legacy records retained: %v", err)
	}
	inputs := make([]SetPersonalSubscriptionNodeInput, 0, len(group.Nodes))
	for _, node := range group.Nodes {
		if node.SourceID != local.ID || node.SourceType != PersonalSourceRelay {
			if node.Status != PersonalNodeUnavailable || node.StatusDetail == "" {
				t.Fatalf("legacy node status = %s", node.Status)
			}
		}
		id := node.ID
		inputs = append(inputs, SetPersonalSubscriptionNodeInput{ID: &id, SourceType: node.SourceType, SourceID: node.SourceID, DisplayName: node.DisplayName, Enabled: true})
	}
	if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, inputs); err != nil {
		t.Fatalf("keep existing unavailable sources: %v", err)
	}
	for _, input := range forbidden {
		id := group.Nodes[0].ID
		input.ID = &id
		if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{input}); !errors.Is(err, ErrPersonalSourceNotFound) {
			t.Fatalf("changed existing source bypassed eligibility: %v", err)
		}
	}
	data, err := service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "relay" {
		t.Fatalf("legacy sources emitted: count=%d, %v", len(data.Nodes), err)
	}
	// Ordinary relay entry points remain unable to mutate published internal resources.
	for _, enabled := range []bool{true, false} {
		if _, _, err := relays.Update(t.Context(), publishedRelay.ID, relaystore.UpdateInput{Enabled: &enabled}); !errors.Is(err, relaystore.ErrSubscriptionManaged) {
			t.Fatalf("published toggle: %v", err)
		}
	}
	if _, err := relays.Delete(t.Context(), publishedRelay.ID); !errors.Is(err, relaystore.ErrSubscriptionManaged) {
		t.Fatalf("published delete: %v", err)
	}
	name := "updated-published"
	if _, _, err := service.UpdatePublishedNode(t.Context(), published.ID, UpdatePublishedNodeInput{Name: &name}); err != nil {
		t.Fatalf("published lifecycle update: %v", err)
	}
	if _, err := service.DeletePublishedNode(t.Context(), published.ID); err != nil {
		t.Fatalf("published lifecycle delete: %v", err)
	}
}
