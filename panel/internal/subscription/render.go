package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

type mihomoConfig struct {
	Mode        string                 `yaml:"mode"`
	Proxies     []mihomoProxy          `yaml:"proxies"`
	ProxyGroups []resolvedRoutingGroup `yaml:"proxy-groups"`
	Rules       []string               `yaml:"rules"`
}

type mihomoProxy struct {
	Name              string             `yaml:"name"`
	Type              string             `yaml:"type"`
	Server            string             `yaml:"server"`
	Port              int                `yaml:"port"`
	UUID              string             `yaml:"uuid,omitempty"`
	Network           string             `yaml:"network,omitempty"`
	UDP               bool               `yaml:"udp"`
	TLS               bool               `yaml:"tls,omitempty"`
	ServerName        string             `yaml:"servername,omitempty"`
	Flow              string             `yaml:"flow,omitempty"`
	ClientFingerprint string             `yaml:"client-fingerprint,omitempty"`
	RealityOptions    *mihomoRealityOpts `yaml:"reality-opts,omitempty"`
	Cipher            string             `yaml:"cipher,omitempty"`
	Password          string             `yaml:"password,omitempty"`
}

type mihomoRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type resolvedRoutingGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

func RenderBase64Subscription(data SubscriptionData) string {
	nodes := make([]ResolvedSubscriptionNode, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		nodes = append(nodes, resolvedNodeFromClientShare(node))
	}
	return RenderResolvedBase64Subscription(nodes)
}

func RenderResolvedBase64Subscription(nodes []ResolvedSubscriptionNode) string {
	values := make([]string, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node.URI)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(values, "\n")))
}

func RenderMihomoSubscription(data SubscriptionData) ([]byte, error) {
	nodes := make([]ResolvedSubscriptionNode, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		nodes = append(nodes, resolvedNodeFromClientShare(node))
	}
	return renderMihomoResolvedSubscription(nodes, data.NodeNames, data.RoutingBindings, data.RoutingPreset, data.MihomoTemplate)
}

func RenderPersonalMihomoSubscription(data PersonalSubscriptionData) ([]byte, error) {
	return renderMihomoResolvedSubscription(data.Nodes, data.NodeNames, data.RoutingBindings, data.RoutingPreset, data.MihomoTemplate)
}

func renderMihomoResolvedSubscription(nodes []ResolvedSubscriptionNode, nodeNames map[int64]string,
	routingBindings RoutingBindings, routingPreset *RoutingPreset, template *SubscriptionTemplate,
) ([]byte, error) {
	proxies := make([]mihomoProxy, 0, len(nodes))
	proxyNames := make([]string, 0, len(nodes))
	for _, node := range nodes {
		value, err := renderMihomoProxy(node)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, value)
		proxyNames = append(proxyNames, node.Name)
	}
	document, root, err := decodeMihomoTemplateDocument(template)
	if err != nil {
		return nil, err
	}
	proxyNode, err := encodeYAMLValue(proxies)
	if err != nil {
		return nil, fmt.Errorf("encode Mihomo proxies: %w", err)
	}
	setMappingValue(root, "proxies", proxyNode)

	if routingPreset == nil {
		return nil, ErrInvalidRoutingPreset
	}
	routing := *routingPreset
	if err := normalizeRoutingPreset(&routing); err != nil {
		return nil, err
	}
	resolvedGroups, err := resolveRoutingGroups(routing.Groups, routingBindings, nodeNames, proxyNames)
	if err != nil {
		return nil, err
	}
	groupsNode, err := encodeYAMLValue(resolvedGroups)
	if err != nil {
		return nil, fmt.Errorf("encode Mihomo routing groups: %w", err)
	}
	providersYAML, err := marshalRoutingRuleProviders(routing.RuleProviders)
	if err != nil {
		return nil, err
	}
	var providersDocument yaml.Node
	if err := yaml.Unmarshal([]byte(providersYAML), &providersDocument); err != nil {
		return nil, fmt.Errorf("decode Mihomo rule providers: %w", err)
	}
	rulesNode, err := encodeYAMLValue(routing.Rules)
	if err != nil {
		return nil, fmt.Errorf("encode Mihomo routing rules: %w", err)
	}
	setMappingValue(root, "proxy-groups", groupsNode)
	setMappingValue(root, "rule-providers", providersDocument.Content[0])
	setMappingValue(root, "rules", rulesNode)
	if err := validateRenderedMihomo(root, proxyNames); err != nil {
		return nil, err
	}
	return yaml.Marshal(document)
}

func decodeMihomoTemplateDocument(template *SubscriptionTemplate) (*yaml.Node, *yaml.Node, error) {
	document, root, err := decodeMihomoDocument(builtinMihomoTemplate, "built-in Mihomo template")
	if err != nil {
		return nil, nil, err
	}
	if template == nil {
		return document, root, nil
	}
	if template.Type != TemplateTypeMihomo {
		return nil, nil, ErrTemplateTypeMismatch
	}
	_, overlay, err := decodeMihomoDocument(template.Content, "custom Mihomo template")
	if err != nil {
		return nil, nil, err
	}
	for _, key := range []string{"proxies", "proxy-groups", "rule-providers", "rules"} {
		if mappingValue(overlay, key) != nil {
			return nil, nil, ErrInvalidTemplate
		}
	}
	for index := 0; index < len(overlay.Content); index += 2 {
		setMappingValue(root, overlay.Content[index].Value, overlay.Content[index+1])
	}
	return document, root, nil
}

func decodeMihomoDocument(source, label string) (*yaml.Node, *yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !safeYAMLNode(document.Content[0]) {
		if err != nil {
			return nil, nil, fmt.Errorf("decode %s: %w", label, err)
		}
		return nil, nil, fmt.Errorf("decode %s: root must be a safe mapping", label)
	}
	root := document.Content[0]
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index].Value
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("decode %s: duplicate key %q", label, key)
		}
		seen[key] = struct{}{}
	}
	return &document, root, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func encodeYAMLValue(value any) (*yaml.Node, error) {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func resolveRoutingGroups(groups []RoutingGroup, bindings RoutingBindings, names map[int64]string,
	allNames []string,
) ([]resolvedRoutingGroup, error) {
	values := make([]resolvedRoutingGroup, 0, len(groups))
	for _, group := range groups {
		members := make([]string, 0, len(group.Proxies)+len(bindings[group.Key])+len(allNames))
		members = append(members, group.Proxies...)
		for _, nodeID := range bindings[group.Key] {
			if name, exists := names[nodeID]; exists {
				members = append(members, name)
			}
		}
		if group.IncludeAll {
			members = append(members, allNames...)
		}
		members = uniqueStrings(members)
		if len(members) == 0 {
			return nil, fmt.Errorf("策略组 %q 没有任何可用成员: %w", group.Name, ErrRoutingGroupEmpty)
		}
		values = append(values, resolvedRoutingGroup{Name: group.Name, Type: group.Type, Proxies: members})
	}
	return values, nil
}

func validateRenderedMihomo(root *yaml.Node, proxyNames []string) error {
	groupsNode := mappingValue(root, "proxy-groups")
	rulesNode := mappingValue(root, "rules")
	if groupsNode == nil || groupsNode.Kind != yaml.SequenceNode || rulesNode == nil || rulesNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("Mihomo template must contain proxy-groups and rules sequences")
	}
	proxySet := make(map[string]struct{}, len(proxyNames))
	for _, name := range proxyNames {
		proxySet[name] = struct{}{}
	}
	groups := make(map[string]RoutingGroup, len(groupsNode.Content))
	for _, groupNode := range groupsNode.Content {
		nameNode, typeNode, proxiesNode := mappingValue(groupNode, "name"), mappingValue(groupNode, "type"), mappingValue(groupNode, "proxies")
		if nameNode == nil || nameNode.Kind != yaml.ScalarNode || nameNode.Value == "" ||
			typeNode == nil || typeNode.Kind != yaml.ScalarNode || proxiesNode == nil || proxiesNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("Mihomo proxy group is missing name, type, or proxies")
		}
		if _, exists := groups[nameNode.Value]; exists {
			return fmt.Errorf("Mihomo proxy group name %q is duplicated", nameNode.Value)
		}
		members := make([]string, 0, len(proxiesNode.Content))
		for _, member := range proxiesNode.Content {
			if member.Kind != yaml.ScalarNode || member.Tag != "!!str" || member.Value == "{{all}}" {
				return fmt.Errorf("Mihomo proxy group %q contains an invalid member", nameNode.Value)
			}
			members = append(members, member.Value)
		}
		groups[nameNode.Value] = RoutingGroup{Name: nameNode.Value, Type: typeNode.Value, Proxies: members}
	}
	for name, group := range groups {
		for _, member := range group.Proxies {
			if member == "DIRECT" || member == "REJECT" {
				continue
			}
			if _, exists := proxySet[member]; exists {
				continue
			}
			if _, exists := groups[member]; !exists {
				return fmt.Errorf("Mihomo proxy group %q references unknown member %q", name, member)
			}
		}
	}
	if routingGroupsCyclic(groups) {
		return fmt.Errorf("Mihomo proxy groups contain a cycle")
	}
	groupNames := make(map[string]struct{}, len(groups))
	for name := range groups {
		groupNames[name] = struct{}{}
	}
	providers := make(map[string]struct{})
	if providersNode := mappingValue(root, "rule-providers"); providersNode != nil {
		if providersNode.Kind != yaml.MappingNode {
			return fmt.Errorf("Mihomo rule-providers must be a mapping")
		}
		for index := 0; index < len(providersNode.Content); index += 2 {
			providers[providersNode.Content[index].Value] = struct{}{}
		}
	}
	if len(rulesNode.Content) > maxRoutingRules {
		return fmt.Errorf("Mihomo rules exceed the supported limit")
	}
	for _, ruleNode := range rulesNode.Content {
		if ruleNode.Kind != yaml.ScalarNode || ruleNode.Tag != "!!str" {
			return fmt.Errorf("Mihomo rule must be a string")
		}
		policy, provider, err := parseRoutingRule(ruleNode.Value)
		if err != nil || !validRoutingPolicy(policy, groupNames) {
			return fmt.Errorf("Mihomo rule %q has an invalid policy", ruleNode.Value)
		}
		if provider != "" {
			if _, exists := providers[provider]; !exists {
				return fmt.Errorf("Mihomo rule %q references missing rule provider %q", ruleNode.Value, provider)
			}
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func resolvedNodeFromClientShare(share proxystore.ClientShare) ResolvedSubscriptionNode {
	flow := share.Flow
	if share.Protocol == proxystore.ProtocolVLESS {
		// Managed VLESS subscriptions have always rendered the server-side Vision
		// flow, including when the share URI carries the udp443 client variant.
		flow = proxystore.ServerFlow
	}
	return ResolvedSubscriptionNode{
		Name: share.DisplayName, Protocol: share.Protocol, Address: share.Address, Port: share.Port,
		UUID: share.UUID, Security: share.Security, TLS: share.Protocol == proxystore.ProtocolVLESS,
		ServerName: share.ServerName, Flow: flow, Fingerprint: share.Fingerprint,
		RealityPublicKey: share.RealityPublicKey, RealityShortID: share.RealityShortID,
		Method: share.Method, Network: share.Network, ShadowsocksPassword: share.ShadowsocksPassword,
		URI: share.URI,
	}
}

func renderMihomoProxy(share ResolvedSubscriptionNode) (mihomoProxy, error) {
	value := mihomoProxy{
		Name: share.Name, Server: share.Address, Port: share.Port, UDP: true,
	}
	switch share.Protocol {
	case proxystore.ProtocolVLESS:
		value.Type = "vless"
		value.UUID = share.UUID
		value.Network = proxystore.TransportTCP
		value.TLS = share.TLS
		value.ServerName = share.ServerName
		value.Flow = subscriptionVLESSFlow(share.Flow)
		value.ClientFingerprint = share.Fingerprint
		if share.Security == proxystore.SecurityReality {
			value.RealityOptions = &mihomoRealityOpts{
				PublicKey: share.RealityPublicKey, ShortID: share.RealityShortID,
			}
		}
	case proxystore.ProtocolShadowsocks:
		value.Type = "ss"
		value.Cipher = share.Method
		value.Password = share.ShadowsocksPassword
		if value.Password == "" {
			return mihomoProxy{}, fmt.Errorf("render Shadowsocks node %q: password is empty", share.Name)
		}
	default:
		return mihomoProxy{}, fmt.Errorf("render subscription node %q: unsupported protocol %q", share.Name, share.Protocol)
	}
	return value, nil
}

func subscriptionVLESSFlow(value string) string {
	value = strings.TrimSpace(value)
	if value == proxystore.ServerFlow || value == proxystore.ServerFlow+"-udp443" {
		return proxystore.ServerFlow
	}
	return value
}
