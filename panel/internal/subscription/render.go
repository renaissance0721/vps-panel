package subscription

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

type mihomoConfig struct {
	Mode        string             `yaml:"mode"`
	Proxies     []mihomoProxy      `yaml:"proxies"`
	ProxyGroups []mihomoProxyGroup `yaml:"proxy-groups"`
	Rules       []string           `yaml:"rules"`
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

type mihomoProxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

func RenderBase64Subscription(data SubscriptionData) string {
	values := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		values = append(values, node.URI)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(values, "\n")))
}

func RenderMihomoSubscription(data SubscriptionData) ([]byte, error) {
	proxies := make([]mihomoProxy, 0, len(data.Nodes))
	proxyNames := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		value, err := renderMihomoProxy(node)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, value)
		proxyNames = append(proxyNames, node.DisplayName)
	}
	defaultProxyNames := append(append([]string(nil), proxyNames...), "DIRECT")
	groups := []mihomoProxyGroup{{Name: "节点选择", Type: "select", Proxies: defaultProxyNames}}
	rules := []string{"MATCH,节点选择"}
	if data.RoutingPreset != nil {
		var err error
		groups, rules, err = renderRoutingPreset(*data.RoutingPreset, data.PublishedNodeNames)
		if err != nil {
			return nil, err
		}
	}
	templateYAML := builtinMihomoTemplate
	if data.Template != nil {
		templateYAML = data.Template.ConfigYAML
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(templateYAML), &config); err != nil {
		return nil, fmt.Errorf("decode subscription template: %w", err)
	}
	if _, exists := config["mode"]; !exists {
		config["mode"] = "rule"
	}
	config["proxies"] = proxies
	if data.Template == nil && data.RoutingPreset == nil {
		if err := expandBuiltinMihomoProxyGroups(config, proxyNames); err != nil {
			return nil, err
		}
	} else {
		config["proxy-groups"] = groups
		config["rules"] = rules
	}
	return yaml.Marshal(config)
}

func expandBuiltinMihomoProxyGroups(config map[string]any, proxyNames []string) error {
	rawGroups, exists := config["proxy-groups"]
	if !exists {
		return errors.New("built-in Mihomo template has no proxy groups")
	}
	encodedGroups, err := yaml.Marshal(rawGroups)
	if err != nil {
		return fmt.Errorf("encode built-in Mihomo proxy groups: %w", err)
	}
	var groups []mihomoProxyGroup
	if err := yaml.Unmarshal(encodedGroups, &groups); err != nil {
		return fmt.Errorf("decode built-in Mihomo proxy groups: %w", err)
	}
	for index := range groups {
		members := make([]string, 0, len(groups[index].Proxies)+len(proxyNames))
		for _, member := range groups[index].Proxies {
			if member == "{{all}}" {
				members = append(members, proxyNames...)
				continue
			}
			members = append(members, member)
		}
		groups[index].Proxies = members
	}
	config["proxy-groups"] = groups
	return nil
}

func renderRoutingPreset(preset RoutingPreset, names map[int64]string) ([]mihomoProxyGroup, []string, error) {
	groupNames := make(map[string]string, len(preset.Groups))
	for _, group := range preset.Groups {
		groupNames[group.ID] = group.Name
	}
	groups := make([]mihomoProxyGroup, 0, len(preset.Groups))
	for _, group := range preset.Groups {
		members := make([]string, 0, len(group.Members))
		for _, member := range group.Members {
			switch member.Type {
			case "published_node":
				name, exists := names[member.PublishedNodeID]
				if !exists {
					return nil, nil, fmt.Errorf("routing group %q references unavailable published node %d", group.Name, member.PublishedNodeID)
				}
				members = append(members, name)
			case "direct":
				members = append(members, "DIRECT")
			case "group":
				name, exists := groupNames[member.GroupID]
				if !exists {
					return nil, nil, fmt.Errorf("routing group %q references unavailable group %q", group.Name, member.GroupID)
				}
				members = append(members, name)
			default:
				return nil, nil, ErrInvalidRoutingPreset
			}
		}
		groups = append(groups, mihomoProxyGroup{Name: group.Name, Type: "select", Proxies: members})
	}
	rules := make([]string, 0, len(preset.Rules))
	for _, rule := range preset.Rules {
		groupName, exists := groupNames[rule.TargetGroupID]
		if !exists {
			return nil, nil, ErrInvalidRoutingPreset
		}
		if rule.Type == "MATCH" {
			rules = append(rules, "MATCH,"+groupName)
		} else {
			rules = append(rules, strings.Join([]string{rule.Type, rule.Value, groupName}, ","))
		}
	}
	return groups, rules, nil
}

func renderMihomoProxy(share proxystore.ClientShare) (mihomoProxy, error) {
	value := mihomoProxy{
		Name: share.DisplayName, Server: share.Address, Port: share.Port, UDP: true,
	}
	switch share.Protocol {
	case proxystore.ProtocolVLESS:
		value.Type = "vless"
		value.UUID = share.UUID
		value.Network = proxystore.TransportTCP
		value.TLS = true
		value.ServerName = share.ServerName
		value.Flow = proxystore.ServerFlow
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
			return mihomoProxy{}, fmt.Errorf("render Shadowsocks node %q: password is empty", share.DisplayName)
		}
	default:
		return mihomoProxy{}, fmt.Errorf("render subscription node %q: unsupported protocol %q", share.DisplayName, share.Protocol)
	}
	return value, nil
}
