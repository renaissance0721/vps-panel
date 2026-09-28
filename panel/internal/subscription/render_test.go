package subscription

import (
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

func TestRenderMihomoSubscriptionUsesStructuredShares(t *testing.T) {
	data := SubscriptionData{
		Title: "我的机场",
		Nodes: []proxystore.ClientShare{
			{
				Client:      proxystore.Client{UUID: "11111111-1111-1111-1111-111111111111"},
				DisplayName: "韩国: 联通 #1 🚀", Protocol: proxystore.ProtocolVLESS,
				Address: "203.0.113.10", Port: 443, Security: proxystore.SecurityReality,
				ServerName: "www.example.com", Fingerprint: proxystore.Fingerprint,
				Flow: proxystore.ServerFlow + "-udp443", RealityPublicKey: "public-key",
				RealityShortID: "short-id",
			},
			{
				DisplayName: "日本 SS", Protocol: proxystore.ProtocolShadowsocks,
				Address: "198.51.100.20", Port: 8388,
				Method:              proxystore.ShadowsocksMethodAES128GCM,
				ShadowsocksPassword: "master-password:client-password",
			},
		},
	}
	body, err := RenderMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse Mihomo YAML: %v\n%s", err, body)
	}
	if parsed.Mode != "rule" || len(parsed.Proxies) != 2 || len(parsed.ProxyGroups) != 1 ||
		len(parsed.Rules) != 1 || parsed.Rules[0] != "MATCH,节点选择" {
		t.Fatalf("Mihomo config = %+v", parsed)
	}
	reality := parsed.Proxies[0]
	if reality.Name != data.Nodes[0].DisplayName || reality.Type != "vless" ||
		reality.Server != "203.0.113.10" || reality.Port != 443 ||
		reality.UUID != data.Nodes[0].UUID || reality.Network != proxystore.TransportTCP ||
		!reality.UDP || !reality.TLS || reality.ServerName != "www.example.com" ||
		reality.Flow != proxystore.ServerFlow || reality.ClientFingerprint != proxystore.Fingerprint ||
		reality.RealityOptions == nil || reality.RealityOptions.PublicKey != "public-key" ||
		reality.RealityOptions.ShortID != "short-id" {
		t.Fatalf("Mihomo Reality proxy = %+v", reality)
	}
	shadowsocks := parsed.Proxies[1]
	if shadowsocks.Name != data.Nodes[1].DisplayName || shadowsocks.Type != "ss" ||
		shadowsocks.Cipher != proxystore.ShadowsocksMethodAES128GCM ||
		shadowsocks.Password != "master-password:client-password" || !shadowsocks.UDP {
		t.Fatalf("Mihomo Shadowsocks proxy = %+v", shadowsocks)
	}
	wantGroup := []string{data.Nodes[0].DisplayName, data.Nodes[1].DisplayName, "DIRECT"}
	if len(parsed.ProxyGroups[0].Proxies) != len(wantGroup) {
		t.Fatalf("Mihomo proxy group = %+v", parsed.ProxyGroups[0])
	}
	for index, value := range wantGroup {
		if parsed.ProxyGroups[0].Proxies[index] != value {
			t.Fatalf("Mihomo proxy group = %+v", parsed.ProxyGroups[0])
		}
	}
}

func TestRenderMihomoTLSOmitsRealityOptions(t *testing.T) {
	body, err := RenderMihomoSubscription(SubscriptionData{Nodes: []proxystore.ClientShare{{
		Client: proxystore.Client{UUID: "uuid"}, DisplayName: "TLS 节点", Protocol: proxystore.ProtocolVLESS,
		Address: "tls.example.com", Port: 443, Security: proxystore.SecurityTLS,
		ServerName: "www.example.com", Fingerprint: proxystore.Fingerprint,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Proxies) != 1 {
		t.Fatalf("Mihomo TLS proxy = %+v", parsed.Proxies)
	}
	proxy := parsed.Proxies[0]
	if proxy.Name != "TLS 节点" || proxy.Type != "vless" || proxy.Server != "tls.example.com" ||
		proxy.Port != 443 || proxy.UUID != "uuid" || proxy.Network != proxystore.TransportTCP ||
		!proxy.UDP || !proxy.TLS || proxy.ServerName != "www.example.com" ||
		proxy.Flow != proxystore.ServerFlow || proxy.ClientFingerprint != proxystore.Fingerprint ||
		proxy.RealityOptions != nil {
		t.Fatalf("Mihomo TLS proxy = %+v", proxy)
	}
}
