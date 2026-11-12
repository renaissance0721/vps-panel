package subscription

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"gopkg.in/yaml.v3"
)

func TestPersonalRemoteAggregationOwnershipAndRefresh(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "owner", "vip")
	insertPersonalTestUser(t, db, 101, "other", "admin")
	owner := PersonalSubscriptionActor{UserID: 100, Role: "vip"}
	other := PersonalSubscriptionActor{UserID: 101, Role: "admin"}
	body := "trojan://synthetic-one@one.example:443#Same\ntrojan://synthetic-two@two.example:443#Two\n"
	var mu sync.Mutex
	requests := 0
	upstreamStatus := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if upstreamStatus != http.StatusOK {
			w.WriteHeader(upstreamStatus)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	remote := NewRemoteService(db, filepath.Join(t.TempDir(), "mail.key"))
	service.remoteSecrets = remote.secrets
	remote.fetcher = remoteTestFetcher(server)
	now := time.Unix(1700000000, 0)
	remote.now = func() time.Time { return now }
	source, err := remote.Create(t.Context(), owner, RemoteInput{Name: "Airport", URL: "http://public.example/?token=synthetic", Enabled: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Refresh(t.Context(), owner, source.ID); err != nil {
		t.Fatal(err)
	}
	cached, err := remote.Nodes(t.Context(), owner, source.ID)
	if err != nil || len(cached) != 2 {
		t.Fatalf("cached nodes: %v %v", cached, err)
	}
	local, err := service.landings.Create(t.Context(), owner.UserID, landingstore.CreateInput{Name: "Local", Visibility: landingstore.VisibilityPrivate, URI: "ss://aes-256-gcm:local-secret@local.example:8388#Local"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := service.CreatePersonalSubscription(t.Context(), owner, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherGroup, err := service.CreatePersonalSubscription(t.Context(), other, CreatePersonalSubscriptionInput{Name: "Other", ClientName: "other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetPersonalSubscriptionNodes(t.Context(), other, otherGroup.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceRemoteSource, SourceID: source.ID, DisplayName: "stolen", Enabled: true}}); !errors.Is(err, ErrPersonalSourceNotFound) {
		t.Fatalf("cross-owner source: %v", err)
	}
	if _, err = service.SetPersonalSubscriptionNodes(t.Context(), other, otherGroup.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceRemoteNode, SourceID: cached[0].ID, DisplayName: "stolen", Enabled: true}}); !errors.Is(err, ErrPersonalSourceNotFound) {
		t.Fatalf("cross-owner node: %v", err)
	}
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), owner, group.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceLanding, SourceID: local.ID, DisplayName: "Same", Enabled: true},
		{SourceType: PersonalSourceRemoteSource, SourceID: source.ID, DisplayName: "Airport", Enabled: true},
		{SourceType: PersonalSourceRemoteNode, SourceID: cached[1].ID, DisplayName: "Individually renamed", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := service.GetRoutingPreset(t.Context(), group.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	group, err = service.SetPersonalSubscriptionRoutingBindings(t.Context(), owner, group.ID, RoutingBindings{key: {group.Nodes[1].ID, group.Nodes[2].ID}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fetches := requests
	mu.Unlock()
	if fetches != 1 {
		t.Fatal("public subscription unexpectedly fetched upstream")
	}
	if len(data.Nodes) != 4 || data.Nodes[0].Name != "Same" || data.Nodes[1].Name == "Same" || data.Nodes[3].Name != "Individually renamed" {
		t.Fatalf("merged nodes: %+v", data.Nodes)
	}
	names, bindings := expandedPersonalBindings(data)
	if len(bindings[key]) != 3 {
		t.Fatalf("source binding did not expand: %v %v", names, bindings)
	}
	all := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		all = append(all, node.Name)
	}
	groups, err := resolveRoutingGroups([]RoutingGroup{{Key: "regex", Name: "Matched", Type: "select", NodePatterns: []string{"^Two$"}}}, nil, nil, all, all)
	if err != nil || len(groups) != 1 || len(groups[0].Proxies) != 1 || groups[0].Proxies[0] != "Two" {
		t.Fatalf("remote regex: %+v %v", groups, err)
	}
	ini := ParseRoutingINI("[custom]\ncustom_proxy_group=匹配`select`[]DIRECT`^Two$\nruleset=匹配,[]DOMAIN-SUFFIX,example.test\nruleset=匹配,[]FINAL\n")
	if len(ini.Diagnostics) != 0 {
		t.Fatalf("INI: %+v", ini.Diagnostics)
	}
	iniData := data
	iniData.RoutingPreset = &RoutingPreset{Name: "INI", Enabled: true, Groups: ini.Groups, Rules: ini.Rules, RuleProviders: ini.RuleProviders}
	iniData.RoutingBindings = nil
	iniYAML, err := RenderPersonalMihomoSubscription(iniData)
	if err != nil {
		t.Fatal(err)
	}
	var iniOutput mihomoConfig
	if err = yaml.Unmarshal(iniYAML, &iniOutput); err != nil {
		t.Fatal(err)
	}
	if len(iniOutput.ProxyGroups) != 1 || strings.Join(iniOutput.ProxyGroups[0].Proxies, ",") != "DIRECT,Two" || strings.Join(iniOutput.Rules, ",") != "DOMAIN-SUFFIX,example.test,匹配,MATCH,匹配" {
		t.Fatalf("INI output: %+v", iniOutput)
	}
	if _, err = RenderPersonalMihomoSubscription(data); err != nil {
		t.Fatalf("Mihomo: %v", err)
	}
	if _, err = RenderPersonalShadowrocketSubscription(data); err != nil {
		t.Fatalf("Shadowrocket: %v", err)
	}
	encoded, err := RenderPersonalBase64Subscription(data)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	if !strings.Contains(string(decoded), "synthetic-two") || !strings.Contains(string(decoded), "Individually%20renamed") {
		t.Fatal("missing URI or renamed fragment")
	}
	mu.Lock()
	body = "trojan://synthetic-two@two.example:443#Renamed\ntrojan://synthetic-three@three.example:443#New\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err = remote.Refresh(t.Context(), owner, source.ID); err != nil {
		t.Fatal(err)
	}
	after, err := remote.Nodes(t.Context(), owner, source.ID)
	if err != nil || len(after) != 2 || after[0].ID != cached[1].ID {
		t.Fatalf("identity: %+v %v", after, err)
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 4 || data.Nodes[1].Name != "Renamed" || data.Nodes[2].Name != "New" || data.Nodes[3].Name != "Individually renamed" {
		t.Fatalf("dynamic refresh: %+v %v", data.Nodes, err)
	}
	mu.Lock()
	body = "trojan://synthetic-three@three.example:443#New\n"
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err = remote.Refresh(t.Context(), owner, source.ID); err != nil {
		t.Fatal(err)
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 2 || data.Nodes[1].Name != "New" {
		t.Fatalf("removed node: %+v %v", data.Nodes, err)
	}
	stored, err := service.GetPersonalSubscription(t.Context(), owner, group.ID)
	if err != nil || stored.Nodes[2].Status != PersonalNodeUnavailable || len(stored.RoutingBindings[key]) != 2 {
		t.Fatalf("removed selection or binding: %+v %v", stored.Nodes, err)
	}
	mu.Lock()
	upstreamStatus = http.StatusInternalServerError
	mu.Unlock()
	now = now.Add(time.Minute)
	if _, err = remote.Refresh(t.Context(), owner, source.ID); err == nil {
		t.Fatal("expected refresh failure")
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 2 || data.Nodes[1].Name != "New" {
		t.Fatalf("failed refresh lost cache: %+v %v", data.Nodes, err)
	}
	if _, err = db.Exec(`UPDATE remote_subscription_sources SET enabled=0 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 {
		t.Fatalf("disabled source: %+v %v", data.Nodes, err)
	}
	if err = remote.Delete(t.Context(), owner, source.ID); err != nil {
		t.Fatal(err)
	}
	stored, err = service.GetPersonalSubscription(t.Context(), owner, group.ID)
	if err != nil || stored.Nodes[1].Status != PersonalNodeUnavailable || len(stored.RoutingBindings[key]) != 2 {
		t.Fatalf("deleted source: %+v %v", stored.Nodes, err)
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 {
		t.Fatalf("deleted source generation: %+v %v", data.Nodes, err)
	}
}

func TestRemoteRenderingPreservesFieldsAndRejectsLoss(t *testing.T) {
	tests := []struct{ protocol, payload, want string }{
		{"vless", "vless://synthetic-uuid@v.example:443?type=ws&security=tls&sni=tls.example&host=ws.example&path=%2Fproxy#Old", "ws-opts"},
		{"vless", "vless://synthetic-uuid@v.example:443?type=tcp&security=reality&sni=tls.example&pbk=public&sid=ab#Old", "reality-opts"},
		{"ss", "ss://YWVzLTEyOC1nY206c3ludGhldGljLXBhc3N3b3Jk@ss.example:8388#Old", "synthetic-password"},
		{"trojan", "trojan://synthetic-password@t.example:443?sni=tls.example#Old", "synthetic-password"},
		{"hysteria2", "hysteria2://synthetic-password@h.example:443?obfs=salamander&obfs-password=synthetic-obfs#Old", "synthetic-obfs"},
		{"tuic", "tuic://synthetic-uuid:synthetic-password@t.example:443?congestion_control=bbr#Old", "congestion-controller"},
	}
	for _, tc := range tests {
		t.Run(tc.protocol+tc.want, func(t *testing.T) {
			n := ResolvedSubscriptionNode{Name: "Renamed", Protocol: tc.protocol, RemoteFormat: "uri", RemotePayload: tc.payload}
			value, err := renderRemoteMihomo(n)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := yaml.Marshal(value)
			if err != nil || !strings.Contains(string(raw), tc.want) {
				t.Fatalf("missing field: %s %v", raw, err)
			}
			uri, err := remoteURI(n)
			if err != nil || !strings.HasSuffix(uri, "#Renamed") {
				t.Fatalf("URI: %s %v", uri, err)
			}
		})
	}
	unknown := ResolvedSubscriptionNode{Name: "N", Protocol: "vless", RemoteFormat: "uri", RemotePayload: "vless://secret@v.example:443?type=tcp&security=tls&unrecognized=secret#N"}
	if _, err := renderRemoteMihomo(unknown); !errors.Is(err, ErrRemoteUnsupported) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unknown parameter: %v", err)
	}
	yamlNode := ResolvedSubscriptionNode{Name: "New", Protocol: "trojan", RemoteFormat: "mihomo", RemotePayload: "name: Old\ntype: trojan\nserver: example.com\nport: 443\npassword: synthetic\nws-opts:\n  path: /secret-path\n"}
	value, err := renderRemoteMihomo(yamlNode)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(value)
	if !strings.Contains(string(raw), "/secret-path") || !strings.Contains(string(raw), "New") {
		t.Fatalf("YAML fields lost: %s", raw)
	}
	if _, err = renderRemoteShadowrocket(yamlNode); !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("unsupported Shadowrocket accepted: %v", err)
	}
	if _, err = RenderPersonalBase64Subscription(PersonalSubscriptionData{Nodes: []ResolvedSubscriptionNode{yamlNode}}); !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("unsupported URI accepted: %v", err)
	}
	simple := ResolvedSubscriptionNode{Name: "Simple", Protocol: "ss", RemoteFormat: "mihomo", RemotePayload: "name: Old\ntype: ss\nserver: example.com\nport: 443\ncipher: aes-128-gcm\npassword: synthetic\n"}
	encoded, err := RenderPersonalBase64Subscription(PersonalSubscriptionData{Nodes: []ResolvedSubscriptionNode{simple}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !strings.HasPrefix(string(decoded), "ss://") || !strings.HasSuffix(string(decoded), "#Simple") {
		t.Fatalf("simple YAML conversion %q: %v", decoded, err)
	}
}

func TestRemoteShadowrocketCompatibilityRejectsUnsupportedVariants(t *testing.T) {
	for _, tc := range []struct {
		protocol, payload string
		supported         bool
	}{
		{"vless", "vless://synthetic-uuid@v.example:443?type=tcp&security=tls&sni=tls.example#N", true},
		{"ss", "ss://YWVzLTEyOC1nY206c3ludGhldGlj@ss.example:8388#N", true},
		{"trojan", "trojan://synthetic@t.example:443?sni=tls.example#N", true},
		{"vless", "vless://synthetic@v.example:443?type=ws&security=tls#N", false},
		{"vless", "vless://synthetic@v.example:443?flow=xtls-rprx-vision-udp443#N", false},
		{"hysteria2", "hysteria2://synthetic@h.example:443#N", false},
		{"tuic", "tuic://synthetic:password@t.example:443#N", false},
	} {
		n := ResolvedSubscriptionNode{Name: "N", Protocol: tc.protocol, RemoteFormat: "uri", RemotePayload: tc.payload}
		line, err := renderRemoteShadowrocket(n)
		if tc.supported && (err != nil || !strings.Contains(line, "N = ")) {
			t.Fatalf("%s: %q %v", tc.protocol, line, err)
		}
		if !tc.supported && !errors.Is(err, ErrRemoteUnsupported) {
			t.Fatalf("unsupported %s: %q %v", tc.protocol, line, err)
		}
	}
	yamlNode := ResolvedSubscriptionNode{Name: "N", Protocol: "ss", RemoteFormat: "mihomo", RemotePayload: "name: N\ntype: ss\nserver: example.com\nport: 443\ncipher: aes-128-gcm\npassword: udp-relay=true\nudp: false\n"}
	line, err := renderRemoteShadowrocket(yamlNode)
	if err != nil || !strings.Contains(line, "password=\"udp-relay=true\"") || !strings.Contains(line, "udp-relay=false") {
		t.Fatalf("credentials or UDP changed: %q %v", line, err)
	}
	if _, err := remoteURI(yamlNode); !errors.Is(err, ErrRemoteUnsupported) {
		t.Fatalf("explicit UDP lost: %v", err)
	}
}
