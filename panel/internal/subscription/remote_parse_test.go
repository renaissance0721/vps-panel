package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRemoteParseMihomoRetainsParametersAndIdentityAcrossRename(t *testing.T) {
	input := `proxies:
  - {name: First, type: vless, server: example.com, port: 443, uuid: synthetic, tls: true, network: ws, ws-opts: {path: /path, headers: {Host: edge.example}}, reality-opts: {public-key: example, short-id: abcd}}
  - {name: Second, type: hysteria2, server: hy.example, port: 443, password: synthetic, obfs: salamander, obfs-password: synthetic}
  - {name: Third, type: tuic, server: tuic.example, port: 443, uuid: synthetic, password: synthetic, alpn: [h3], udp-relay-mode: native}
  - {name: Unsupported, type: vmess, server: example.com, port: 443}
`
	result, err := parseRemoteSubscription([]byte(input))
	if err != nil || len(result.nodes) != 3 || len(result.warnings) != 1 {
		t.Fatalf("unexpected parsed counts: %d nodes, %d warnings, %v", len(result.nodes), len(result.warnings), err)
	}
	if !strings.Contains(result.nodes[0].payload, "reality-opts") || !strings.Contains(result.nodes[0].payload, "ws-opts") || !strings.Contains(result.nodes[1].payload, "obfs-password") || !strings.Contains(result.nodes[2].payload, "udp-relay-mode") {
		t.Fatal("protocol options were lost")
	}
	renamed := strings.Replace(input, "name: First", "name: Renamed", 1)
	updated, err := parseRemoteSubscription([]byte(renamed))
	if err != nil || updated.nodes[0].identity != result.nodes[0].identity {
		t.Fatal("renaming changed identity")
	}
}

func TestRemoteParseURIsPlainAndBase64(t *testing.T) {
	input := "vless://synthetic@example.com:443?security=reality&pbk=example&type=ws#One\n" +
		"trojan://synthetic@example.com:443?sni=example.com#Two\n" +
		"hysteria2://synthetic@example.com:443?obfs=salamander&obfs-password=synthetic#Three\n" +
		"tuic://synthetic:synthetic@example.com:443?congestion_control=bbr#Four\n" +
		"ss://YWVzLTEyOC1nY206c3ludGhldGlj@example.com:443#Five\n" +
		"vmess://unsupported#Six\n"
	for _, data := range []string{input, base64.StdEncoding.EncodeToString([]byte(input)), base64.RawURLEncoding.EncodeToString([]byte(input))} {
		result, err := parseRemoteSubscription([]byte(data))
		if err != nil || len(result.nodes) != 5 || len(result.warnings) != 1 {
			t.Fatalf("unexpected parsed counts: %d nodes, %d warnings, %v", len(result.nodes), len(result.warnings), err)
		}
		if !strings.Contains(result.nodes[0].payload, "security=reality") || !strings.Contains(result.nodes[2].payload, "obfs-password") {
			t.Fatal("URI options were lost")
		}
		for _, warning := range result.warnings {
			if strings.Contains(warning, "synthetic") || strings.Contains(warning, "://") {
				t.Fatal("warning contains a node credential")
			}
		}
	}
	first, _ := parseRemoteSubscription([]byte(input))
	renamed, _ := parseRemoteSubscription([]byte(strings.Replace(input, "#One", "#Other", 1)))
	if first.nodes[0].identity != renamed.nodes[0].identity {
		t.Fatal("URI fragment changed identity")
	}
}

func TestRemoteParseRejectsUnsafeYAMLAndMalformedContent(t *testing.T) {
	for _, input := range []string{
		"", "not a subscription", "proxies: [", "proxies: []",
		"proxies:\n  - {name: a, type: vless, type: ss}\n",
		"proxies:\n  - &node {name: a, type: ss}\n  - *node\n",
		"proxies: []\n---\nproxies: []\n",
		"proxies:\n  - {name: a, type: ss, opts: !evil content}\n",
		"proxies:\n  - {name: a, type: ss, opts: " + strings.Repeat("[", 70) + "x" + strings.Repeat("]", 70) + "}\n",
		"proxies:\n  - {name: Missing, type: vless, server: example.com}\n",
		"trojan://example.com:443#NoCredential\n",
	} {
		if _, err := parseRemoteSubscription([]byte(input)); err == nil {
			t.Errorf("malformed content accepted")
		}
	}
}

func TestRemoteParseDeduplicatesAndDisambiguatesNames(t *testing.T) {
	a := "trojan://alpha@one.example:443#Same"
	b := "trojan://beta@two.example:443#Same"
	first, err := parseRemoteSubscription([]byte(a + "\n" + b + "\n" + a))
	if err != nil || len(first.nodes) != 2 || len(first.warnings) != 3 || first.nodes[0].name == first.nodes[1].name {
		t.Fatalf("duplicate handling: %d nodes, %d warnings, %v", len(first.nodes), len(first.warnings), err)
	}
	reordered, err := parseRemoteSubscription([]byte(b + "\n" + a))
	if err != nil || first.nodes[0].identity != reordered.nodes[1].identity || first.nodes[0].name != reordered.nodes[1].name {
		t.Fatal("reordering changed node identity or disambiguated name")
	}
}
