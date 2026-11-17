package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRemoteParseMihomoRetainsParametersAndDistinguishesRename(t *testing.T) {
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
	if err != nil || updated.nodes[0].identity == result.nodes[0].identity || updated.nodes[0].connectionIdentity != result.nodes[0].connectionIdentity {
		t.Fatal("rename identity did not preserve connection identity")
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
	if first.nodes[0].identity == renamed.nodes[0].identity || first.nodes[0].connectionIdentity != renamed.nodes[0].connectionIdentity {
		t.Fatal("URI fragment did not affect node identity")
	}
}

func TestRemoteParseNameAndConfigurationIdentity(t *testing.T) {
	input := `proxies:
  - {name: traffic, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: expire, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: 香港实验性节点, type: trojan, server: example.com, port: 443, password: fictional}
  - {name: 香港01, type: trojan, server: example.com, port: 443, password: fictional}
  - {password: fictional, port: 443, server: example.com, type: trojan, name: 香港01}
  - {name: 香港01, type: trojan, server: other.example.com, port: 443, password: fictional}
  - {name: 香港02, type: trojan, server: example.com, port: 443, password: fictional}
  - {type: trojan, name: 香港01, password: fictional, server: example.com, port: 443}
  - {name: Unsupported, type: vmess, server: example.com, port: 443}
`
	result, err := parseRemoteSubscription([]byte(input))
	if err != nil || len(result.nodes) != 6 {
		t.Fatalf("nodes=%d, err=%v", len(result.nodes), err)
	}
	if strings.Join(result.warnings, "|") != "第 9 个节点协议不支持|发现 2 个完全重复的节点，已保留第一项|发现重名节点，已添加序号|发现重名节点，已添加序号" {
		t.Fatalf("warnings: %v", result.warnings)
	}
	for i, name := range []string{"traffic", "expire", "香港实验性节点"} {
		if result.nodes[i].originalName != name || result.nodes[i].name != name || !strings.Contains(result.nodes[i].payload, "fictional") {
			t.Fatal("different named nodes or connection parameters were lost")
		}
	}
	if result.nodes[3].identity == result.nodes[4].identity || result.nodes[3].name == result.nodes[4].name {
		t.Fatal("same-name different configurations must remain distinct")
	}
	if result.nodes[0].identity == result.nodes[1].identity || result.nodes[0].connectionIdentity != result.nodes[1].connectionIdentity {
		t.Fatal("different names with identical connection were merged")
	}
	if strings.Contains(strings.Join(result.warnings, " "), "fictional") {
		t.Fatal("warning leaked credentials")
	}
	reordered, err := parseRemoteSubscription([]byte(`proxies:
  - {name: 香港01, type: trojan, server: other.example.com, port: 443, password: fictional}
  - {name: 香港01, type: trojan, server: example.com, port: 443, password: fictional}
`))
	if err != nil || reordered.nodes[0].identity != result.nodes[4].identity || reordered.nodes[0].name != result.nodes[4].name || reordered.nodes[1].identity != result.nodes[3].identity || reordered.nodes[1].name != result.nodes[3].name {
		t.Fatal("YAML reordering changed identities or suffixes")
	}
}

func TestRemoteParseURIDistinctNamesAndDuplicates(t *testing.T) {
	a := "trojan://fictional@example.com:443#traffic"
	b := "trojan://fictional@example.com:443#expire"
	result, err := parseRemoteSubscription([]byte(a + "\n" + b + "\n" + a + "\n" + b + "\n"))
	if err != nil || len(result.nodes) != 2 || len(result.warnings) != 1 || result.warnings[0] != "发现 2 个完全重复的节点，已保留第一项" {
		t.Fatalf("URI duplicate count or warning: %d, %v, %v", len(result.nodes), result.warnings, err)
	}
	reordered, err := parseRemoteSubscription([]byte(b + "\n" + a))
	if err != nil || result.nodes[0].identity != reordered.nodes[1].identity || result.nodes[1].identity != reordered.nodes[0].identity {
		t.Fatal("URI identities changed with input order")
	}
}

func TestRemoteWarningsSeparateInvalidUnsupportedAndDuplicates(t *testing.T) {
	raw := "trojan://fictional@example.com:443#Same\n" +
		"trojan://fictional@example.com:443#Same\n" +
		"vmess://fictional@example.com:443#Unsupported\n" +
		"trojan://example.com:443#Invalid\n"
	result, err := parseRemoteSubscription([]byte(raw))
	if err != nil || !result.invalid || len(result.nodes) != 1 || len(result.warnings) != 3 {
		t.Fatalf("node count or warning count: %d %d %v", len(result.nodes), len(result.warnings), err)
	}
	if result.warnings[0] != "第 3 行节点协议不支持" || result.warnings[1] != "第 4 行节点缺少必要连接字段" || result.warnings[2] != "发现 1 个完全重复的节点，已保留第一项" || strings.Contains(strings.Join(result.warnings, " "), "fictional") {
		t.Fatalf("incorrect or sensitive warnings: %v", result.warnings)
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
