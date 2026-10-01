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
	if _, err := service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: missingProxy.ID, DisplayName: "Missing", Enabled: true},
		{SourceType: PersonalSourceProxy, SourceID: readyProxy.ID, DisplayName: "Ready", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalSubscriptionDataForOwner(t.Context(), actor, group.ID)
	if err != nil || len(data.Nodes) != 1 || data.Nodes[0].Name != "Ready" {
		t.Fatalf("partially available data = %+v, %v", data.Nodes, err)
	}
	if _, err := db.Exec(`UPDATE clients SET name = 'removed' WHERE id = ?`, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GeneratePersonalSubscriptionDataForOwner(t.Context(), actor, group.ID); !errors.Is(err, ErrPersonalSubscriptionEmpty) {
		t.Fatalf("empty personal subscription error = %v", err)
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
