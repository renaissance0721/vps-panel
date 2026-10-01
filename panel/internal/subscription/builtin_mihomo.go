package subscription

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const BuiltinMihomoName = "内置默认 Mihomo 模板"

type MihomoConfiguration struct {
	YAML string
}

// builtinMihomoTemplate is the built-in client base configuration. Proxies are
// injected by Panel and routing is provided by the plan's RoutingPreset.
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
`

func BuildMihomoConfiguration(template *SubscriptionTemplate) (MihomoConfiguration, error) {
	document, _, err := decodeMihomoTemplateDocument(template)
	if err != nil {
		return MihomoConfiguration{}, err
	}
	if template == nil {
		return MihomoConfiguration{YAML: builtinMihomoTemplate}, nil
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return MihomoConfiguration{}, fmt.Errorf("encode effective Mihomo template: %w", err)
	}
	return MihomoConfiguration{YAML: string(encoded)}, nil
}
