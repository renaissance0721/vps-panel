package subscription

const BuiltinShadowrocketName = "内置默认 Shadowrocket 模板"

// Only client settings live here. Nodes and routing always come from Panel.
const builtinShadowrocketTemplate = `# Shadowrocket

[General]
bypass-system = true
skip-proxy = 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local
ipv6 = true

[Proxy]
{{PROXIES}}

[Proxy Group]
{{PROXY_GROUPS}}

[Rule]
{{RULES}}
`

func BuildShadowrocketConfiguration(template *SubscriptionTemplate) (string, error) {
	if template == nil {
		return builtinShadowrocketTemplate, nil
	}
	if template.Type != TemplateTypeShadowrocket {
		return "", ErrTemplateTypeMismatch
	}
	if err := validateShadowrocketTemplate(template.Content); err != nil {
		return "", err
	}
	return template.Content, nil
}
