package subscription

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestHysteria2MihomoFields(t *testing.T) {
	share := proxystore.ClientShare{Client: proxystore.Client{Password: "client-password"}, Protocol: proxystore.ProtocolHysteria2, DisplayName: "HY2", Address: "node.example.com", Port: 443, ServerName: "tls.example.com", ObfsPassword: "obfs-password"}
	value, err := renderMihomoProxy(resolvedNodeFromClientShare(share))
	if err != nil || value.Type != "hysteria2" || value.Password != share.Password || value.SNI != share.ServerName || value.Obfs != "salamander" || value.ObfsPassword != share.ObfsPassword {
		t.Fatalf("invalid HY2 Mihomo fields: %v", err)
	}
	encoded, err := encodeYAMLValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if node := mappingValue(encoded, "skip-cert-verify"); node != nil && strings.EqualFold(node.Value, "true") {
		t.Fatal("insecure TLS enabled")
	}
}

func TestHysteria2MihomoHoppingPorts(t *testing.T) {
	share := proxystore.ClientShare{Client: proxystore.Client{Password: "client-password"}, Protocol: proxystore.ProtocolHysteria2, DisplayName: "HY2", Address: "node.example.com", Port: 443, ServerName: "tls.example.com", ObfsPassword: "obfs", Hysteria2Ports: "20000-30000"}
	value, err := renderMihomoProxy(resolvedNodeFromClientShare(share))
	if err != nil || value.Ports != "20000-30000" || value.Port != 443 || value.Password != share.Password || value.SNI != share.ServerName || value.ObfsPassword != "obfs" {
		t.Fatalf("Hysteria2 hopping export = %+v, %v", value, err)
	}
}

func TestRemoteHysteria2HoppingURIAndMihomo(t *testing.T) {
	raw := "hysteria2://synthetic%40password@[2001:db8::1]:20000-30000/?sni=tls.example.com&obfs=salamander&obfs-password=synthetic#Original"
	parsed, err := parseRemoteSubscription([]byte(raw))
	if err != nil || len(parsed.nodes) != 1 {
		t.Fatalf("parse hopping URI: %v", err)
	}
	node := ResolvedSubscriptionNode{Name: "Renamed", Protocol: "hysteria2", RemoteFormat: "uri", RemotePayload: raw}
	value, err := renderRemoteMihomo(node)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := value.Decode(&fields); err != nil || fields["ports"] != "20000-30000" || fields["password"] != "synthetic@password" || fields["sni"] != "tls.example.com" {
		t.Fatalf("Mihomo hopping fields invalid: %v", err)
	}
	uri, err := remoteURI(node)
	if err != nil || !strings.Contains(uri, "]:20000-30000/") || !strings.HasSuffix(uri, "#Renamed") {
		t.Fatalf("hopping URI did not retain range: %v", err)
	}
	for _, ports := range []string{"0-10", "20-10", "1-65536", "1-5;delete", "1,,2"} {
		if _, ok := validateRemoteHysteriaPorts(ports); ok {
			t.Fatalf("accepted invalid port range %q", ports)
		}
	}
	mihomo := ResolvedSubscriptionNode{Name: "Renamed", Protocol: "hysteria2", RemoteFormat: "mihomo", RemotePayload: "name: Old\ntype: hysteria2\nserver: node.example.com\nport: 443\nports: 20000-30000\npassword: synthetic\nsni: tls.example.com\n"}
	uri, err = remoteURI(mihomo)
	if err != nil || !strings.Contains(uri, "@node.example.com:20000-30000") {
		t.Fatalf("Mihomo-to-URI hopping conversion failed: %v", err)
	}
}

func TestHysteria2URIAndUnsupportedShadowrocketConfig(t *testing.T) {
	share := proxystore.ClientShare{Client: proxystore.Client{Password: "secret"}, Protocol: proxystore.ProtocolHysteria2, DisplayName: "HY2", Address: "node.example.com", Port: 443, ServerName: "tls.example.com", URI: "hysteria2://secret@node.example.com:443/?sni=tls.example.com#HY2"}
	encoded := RenderBase64Subscription(SubscriptionData{Nodes: []proxystore.ClientShare{share}})
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != share.URI {
		t.Fatal("Hysteria2 URI subscription changed")
	}
	if _, err := renderShadowrocketProxy(resolvedNodeFromClientShare(share)); !errors.Is(err, ErrUnsupportedShadowrocketProtocol) {
		t.Fatalf("unsupported Shadowrocket format: %v", err)
	}
}

func TestHysteria2CannotBeAddedToTrafficAccountedPlans(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "Target", "203.0.113.10")
	if _, err := db.Exec(`INSERT INTO proxies(id,server_id,name,protocol,kernel,listen_port,config_json,created_at,updated_at) VALUES (77,1,'hy2','hysteria2','sing-box',443,'{}',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{Name: "HY2", Mode: NodeModeDirect, TargetProxyID: 77, Enabled: true}); !errors.Is(err, ErrHysteriaPlanUnsupported) {
		t.Fatalf("published node unexpectedly accepted: %v", err)
	}
}
