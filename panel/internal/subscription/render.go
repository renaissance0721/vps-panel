package subscription

import (
	"encoding/base64"
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
	proxyNames := make([]string, 0, len(data.Nodes)+1)
	for _, node := range data.Nodes {
		value, err := renderMihomoProxy(node)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, value)
		proxyNames = append(proxyNames, node.DisplayName)
	}
	proxyNames = append(proxyNames, "DIRECT")
	return yaml.Marshal(mihomoConfig{
		Mode:    "rule",
		Proxies: proxies,
		ProxyGroups: []mihomoProxyGroup{{
			Name: "节点选择", Type: "select", Proxies: proxyNames,
		}},
		Rules: []string{"MATCH,节点选择"},
	})
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
