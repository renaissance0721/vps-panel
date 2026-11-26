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
