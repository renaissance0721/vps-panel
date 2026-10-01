package subscription

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const BuiltinMihomoName = "内置默认 Mihomo 模板"

type MihomoTemplateGroup struct {
	Name    string   `json:"name" yaml:"name"`
	Type    string   `json:"type" yaml:"type"`
	Proxies []string `json:"proxies" yaml:"proxies"`
}

type MihomoConfiguration struct {
	YAML          string
	Groups        []MihomoTemplateGroup
	Rules         []string
	RuleProviders []string
}

// builtinMihomoTemplate is VPS Panel Default Mihomo Template v1.
const builtinMihomoTemplate = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: true
unified-delay: true
tcp-concurrent: true

profile:
  store-selected: true
  store-fake-ip: true

sniffer:
  enable: true
  sniff:
    HTTP:
      ports:
        - 80
        - 8080-8880
      override-destination: true
    TLS:
      ports:
        - 443
        - 8443
    QUIC:
      ports:
        - 443
        - 8443

dns:
  enable: true
  ipv6: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - "*.local"
    - "geosite:cn"
    - "geosite:private"
  default-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  nameserver:
    - https://1.1.1.1/dns-query
    - https://8.8.8.8/dns-query
  proxy-server-nameserver:
    - https://223.5.5.5/dns-query

proxies: []

proxy-groups:
  - name: 🚀 默认代理
    type: select
    proxies:
      - "{{all}}"
      - DIRECT
  - name: 🤖 AI
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: ▶️ YouTube
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🎬 Netflix
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: ✈️ Telegram
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🎵 TikTok
    type: select
    proxies:
      - 🚀 默认代理
      - "{{all}}"
  - name: 🍎 Apple
    type: select
    proxies:
      - DIRECT
      - 🚀 默认代理
      - "{{all}}"
  - name: Ⓜ️ Microsoft
    type: select
    proxies:
      - DIRECT
      - 🚀 默认代理
      - "{{all}}"

rule-providers:
  OpenAI:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/OpenAI/OpenAI.yaml
  Claude:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Claude/Claude.yaml
  Gemini:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Gemini/Gemini.yaml
  YouTube:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/YouTube/YouTube.yaml
  Netflix:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Netflix/Netflix_Classical.yaml
  Telegram:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Telegram/Telegram.yaml
  TikTok:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/TikTok/TikTok.yaml
  Apple:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Apple/Apple_Classical.yaml
  Copilot:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Copilot/Copilot.yaml
  Microsoft:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Microsoft/Microsoft.yaml

rules:
  - "RULE-SET,OpenAI,🤖 AI"
  - "RULE-SET,Claude,🤖 AI"
  - "RULE-SET,Gemini,🤖 AI"
  - "RULE-SET,YouTube,▶️ YouTube"
  - "RULE-SET,Netflix,🎬 Netflix"
  - "RULE-SET,Telegram,✈️ Telegram"
  - "RULE-SET,TikTok,🎵 TikTok"
  - "RULE-SET,Apple,🍎 Apple"
  - "RULE-SET,Copilot,Ⓜ️ Microsoft"
  - "RULE-SET,Microsoft,Ⓜ️ Microsoft"
  - "GEOIP,CN,DIRECT,no-resolve"
  - "MATCH,🚀 默认代理"
`

func BuildMihomoConfiguration(template *SubscriptionTemplate) (MihomoConfiguration, error) {
	document, root, err := decodeMihomoTemplateDocument(template)
	if err != nil {
		return MihomoConfiguration{}, err
	}
	groupsNode := mappingValue(root, "proxy-groups")
	rulesNode := mappingValue(root, "rules")
	if groupsNode == nil || groupsNode.Kind != yaml.SequenceNode || rulesNode == nil || rulesNode.Kind != yaml.SequenceNode {
		return MihomoConfiguration{}, fmt.Errorf("Mihomo template must contain proxy-groups and rules sequences")
	}
	var groups []MihomoTemplateGroup
	if err := groupsNode.Decode(&groups); err != nil {
		return MihomoConfiguration{}, fmt.Errorf("decode Mihomo template proxy groups: %w", err)
	}
	var rules []string
	if err := rulesNode.Decode(&rules); err != nil {
		return MihomoConfiguration{}, fmt.Errorf("decode Mihomo template rules: %w", err)
	}
	providers := make([]string, 0)
	if providersNode := mappingValue(root, "rule-providers"); providersNode != nil {
		if providersNode.Kind != yaml.MappingNode {
			return MihomoConfiguration{}, fmt.Errorf("Mihomo template rule-providers must be a mapping")
		}
		providers = make([]string, 0, len(providersNode.Content)/2)
		for index := 0; index < len(providersNode.Content); index += 2 {
			providers = append(providers, providersNode.Content[index].Value)
		}
	}
	source := builtinMihomoTemplate
	if template != nil {
		encoded, err := yaml.Marshal(document)
		if err != nil {
			return MihomoConfiguration{}, fmt.Errorf("encode effective Mihomo template: %w", err)
		}
		source = string(encoded)
	}
	return MihomoConfiguration{YAML: source, Groups: groups, Rules: rules, RuleProviders: providers}, nil
}
