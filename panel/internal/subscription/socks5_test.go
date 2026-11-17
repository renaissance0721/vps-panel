package subscription

import (
	"errors"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"gopkg.in/yaml.v3"
)

func TestSOCKS5PersonalImportToProviderAndFullConfig(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "owner", "admin")
	insertPersonalTestUser(t, db, 101, "other", "vip")
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	node, err := landingstore.NewService(db).Create(t.Context(), actor.UserID, landingstore.CreateInput{NodeRole: "landing", URI: "socks5://example%40user:p%3A%23%25%22@203.0.113.10:9492#Webshare-Japan"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	upstream := "机场入口"
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceLanding, SourceID: node.ID, DisplayName: "Webshare-Japan", Enabled: true, DialerProxy: &upstream}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalMihomoProviderData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 {
		t.Fatalf("resolve: %v", err)
	}
	body, err := RenderPersonalMihomoProvider(data)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Proxies []struct {
			Name     string `yaml:"name"`
			Type     string `yaml:"type"`
			Server   string `yaml:"server"`
			Port     int    `yaml:"port"`
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			UDP      *bool  `yaml:"udp"`
			Dialer   string `yaml:"dialer-proxy"`
			Cipher   string `yaml:"cipher"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Proxies) != 1 {
		t.Fatal("missing node")
	}
	proxy := output.Proxies[0]
	if proxy.Name != "Webshare-Japan" || proxy.Type != "socks5" || proxy.Server != "203.0.113.10" || proxy.Port != 9492 || proxy.Username != "example@user" || proxy.Password != "p:#%\"" || proxy.UDP == nil || *proxy.UDP || proxy.Dialer != upstream || proxy.Cipher != "" {
		t.Fatal("provider node did not preserve SOCKS5 fields")
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil {
		t.Fatal(err)
	}
	data.RoutingPreset = &RoutingPreset{Name: "internal", Enabled: true, Groups: []RoutingGroup{{Key: "grp_airport", Name: upstream, Type: "select", Proxies: []string{"DIRECT"}}}, Rules: []string{"MATCH,机场入口"}}
	full, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var fullOutput struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(full, &fullOutput); err != nil || len(fullOutput.Proxies) != 1 || fullOutput.Proxies[0]["password"] != "p:#%\"" || fullOutput.Proxies[0]["dialer-proxy"] != upstream {
		t.Fatal("full config lost SOCKS5 authentication or chain")
	}
	if _, err := RenderPersonalBase64Subscription(data); !errors.Is(err, ErrSOCKS5Export) {
		t.Fatalf("Base64: %v", err)
	}
	if _, err := RenderPersonalShadowrocketSubscription(data); !errors.Is(err, ErrSOCKS5Export) {
		t.Fatalf("Shadowrocket: %v", err)
	}
	other := PersonalSubscriptionActor{UserID: 101, Role: "vip"}
	otherGroup, err := service.CreatePersonalSubscription(t.Context(), other, CreatePersonalSubscriptionInput{Name: "Other", ClientName: "other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPersonalSubscriptionNodes(t.Context(), other, otherGroup.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceLanding, SourceID: node.ID, DisplayName: "Denied", Enabled: true}}); err == nil {
		t.Fatal("private node accessible to another user")
	}
}
