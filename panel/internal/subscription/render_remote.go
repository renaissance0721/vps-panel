package subscription

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

var ErrRemoteUnsupported = errors.New("远程节点包含当前输出格式不支持的参数")

// Preserve Mihomo mappings rather than reducing them to the local node model.
func renderRemoteMihomo(node ResolvedSubscriptionNode) (*yaml.Node, error) {
	if node.RemoteFormat == "mihomo" {
		var doc yaml.Node
		if yaml.Unmarshal([]byte(node.RemotePayload), &doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || !remoteSafeYAML(doc.Content[0]) {
			return nil, ErrRemoteUnsupported
		}
		protocol := mappingValue(doc.Content[0], "type")
		if protocol == nil || protocol.Kind != yaml.ScalarNode || remoteProtocol(protocol.Value) != node.Protocol {
			return nil, ErrRemoteUnsupported
		}
		protocol.Value = node.Protocol
		setMappingValue(doc.Content[0], "name", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: node.Name})
		return doc.Content[0], nil
	}
	if node.RemoteFormat != "uri" {
		return nil, ErrRemoteUnsupported
	}
	u, err := url.Parse(node.RemotePayload)
	if err != nil || u.Hostname() == "" {
		return nil, ErrRemoteUnsupported
	}
	if u.Path != "" && u.Path != "/" {
		return nil, ErrRemoteUnsupported
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, ErrRemoteUnsupported
	}
	for _, values := range q {
		if len(values) != 1 {
			return nil, ErrRemoteUnsupported
		}
	}
	allowed := map[string]bool{}
	keys := map[string][]string{
		"vless":     {"type", "security", "encryption", "flow", "sni", "fp", "pbk", "sid", "alpn", "path", "host", "serviceName", "allowInsecure"},
		"trojan":    {"type", "security", "sni", "peer", "fp", "alpn", "path", "host", "serviceName", "allowInsecure"},
		"hysteria2": {"sni", "insecure", "obfs", "obfs-password"},
		"tuic":      {"sni", "alpn", "allowInsecure", "congestion_control", "udp_relay_mode"},
	}
	for _, k := range keys[node.Protocol] {
		allowed[k] = true
	}
	for k := range q {
		if !allowed[k] {
			return nil, ErrRemoteUnsupported
		}
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrRemoteUnsupported
		}
	}
	m := map[string]any{"name": node.Name, "type": node.Protocol, "server": u.Hostname(), "port": port}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	switch node.Protocol {
	case "vless":
		if user == "" || pass != "" || (q.Get("encryption") != "" && q.Get("encryption") != "none") {
			return nil, ErrRemoteUnsupported
		}
		if flow := q.Get("flow"); flow != "" && flow != "xtls-rprx-vision" {
			return nil, ErrRemoteUnsupported
		}
		m["uuid"] = user
		if q.Get("flow") != "" {
			m["flow"] = q.Get("flow")
		}
	case "ss":
		if u.User == nil {
			decoded, ok := decodeRemoteBase64(u.Host)
			if !ok {
				return nil, ErrRemoteUnsupported
			}
			credentials, host, ok := strings.Cut(string(decoded), "@")
			if !ok {
				return nil, ErrRemoteUnsupported
			}
			user, pass, ok = strings.Cut(credentials, ":")
			if !ok {
				return nil, ErrRemoteUnsupported
			}
			address, p, e := net.SplitHostPort(host)
			if e != nil {
				return nil, ErrRemoteUnsupported
			}
			n, e := strconv.Atoi(p)
			if e != nil || n < 1 || n > 65535 {
				return nil, ErrRemoteUnsupported
			}
			m["server"], m["port"] = address, n
		} else if pass == "" {
			decoded, ok := decodeRemoteBase64(user)
			if !ok {
				return nil, ErrRemoteUnsupported
			}
			user, pass, ok = strings.Cut(string(decoded), ":")
			if !ok {
				return nil, ErrRemoteUnsupported
			}
		}
		if user == "" || pass == "" || len(q) > 0 {
			return nil, ErrRemoteUnsupported
		}
		m["cipher"], m["password"] = user, pass
	case "trojan", "hysteria2":
		if pass != "" || (node.Protocol == "trojan" && user == "") {
			return nil, ErrRemoteUnsupported
		}
		m["password"] = user
	case "tuic":
		if user == "" || pass == "" {
			return nil, ErrRemoteUnsupported
		}
		m["uuid"], m["password"] = user, pass
	default:
		return nil, ErrRemoteUnsupported
	}
	if node.Protocol == "ss" {
		return encodeYAMLValue(m)
	}
	security := q.Get("security")
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return nil, ErrRemoteUnsupported
	}
	if node.Protocol == "vless" {
		m["tls"] = security == "tls" || security == "reality"
	} else if security == "none" {
		return nil, ErrRemoteUnsupported
	}
	sni := q.Get("sni")
	if q.Get("peer") != "" {
		if sni != "" {
			return nil, ErrRemoteUnsupported
		}
		sni = q.Get("peer")
	}
	if sni != "" {
		if node.Protocol == "vless" {
			m["servername"] = sni
		} else {
			m["sni"] = sni
		}
	}
	if q.Get("fp") != "" {
		m["client-fingerprint"] = q.Get("fp")
	}
	if security == "reality" {
		if q.Get("pbk") == "" {
			return nil, ErrRemoteUnsupported
		}
		m["reality-opts"] = map[string]any{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
	} else if q.Get("pbk") != "" || q.Get("sid") != "" {
		return nil, ErrRemoteUnsupported
	}
	if q.Get("alpn") != "" {
		m["alpn"] = strings.Split(q.Get("alpn"), ",")
	}
	insecure := q.Get("insecure")
	if q.Get("allowInsecure") != "" {
		if insecure != "" {
			return nil, ErrRemoteUnsupported
		}
		insecure = q.Get("allowInsecure")
	}
	if insecure != "" {
		b, e := strconv.ParseBool(insecure)
		if e != nil {
			if insecure == "1" {
				b = true
			} else if insecure != "0" {
				return nil, ErrRemoteUnsupported
			}
		}
		m["skip-cert-verify"] = b
	}
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	if node.Protocol == "vless" || node.Protocol == "trojan" {
		switch network {
		case "tcp":
			if q.Get("path") != "" || q.Get("host") != "" || q.Get("serviceName") != "" {
				return nil, ErrRemoteUnsupported
			}
		case "ws":
			if q.Get("serviceName") != "" {
				return nil, ErrRemoteUnsupported
			}
			m["network"] = "ws"
			opts := map[string]any{}
			if q.Get("path") != "" {
				opts["path"] = q.Get("path")
			}
			if q.Get("host") != "" {
				opts["headers"] = map[string]string{"Host": q.Get("host")}
			}
			m["ws-opts"] = opts
		case "grpc":
			if q.Get("path") != "" || q.Get("host") != "" {
				return nil, ErrRemoteUnsupported
			}
			m["network"] = "grpc"
			m["grpc-opts"] = map[string]string{"grpc-service-name": q.Get("serviceName")}
		default:
			return nil, ErrRemoteUnsupported
		}
	} else if network != "tcp" || q.Get("path") != "" || q.Get("host") != "" || q.Get("serviceName") != "" {
		return nil, ErrRemoteUnsupported
	}
	if q.Get("obfs") != "" || q.Get("obfs-password") != "" {
		if node.Protocol != "hysteria2" || q.Get("obfs") != "salamander" || q.Get("obfs-password") == "" {
			return nil, ErrRemoteUnsupported
		}
		m["obfs"], m["obfs-password"] = q.Get("obfs"), q.Get("obfs-password")
	}
	if q.Get("congestion_control") != "" || q.Get("udp_relay_mode") != "" {
		if node.Protocol != "tuic" {
			return nil, ErrRemoteUnsupported
		}
		if q.Get("congestion_control") != "" {
			if value := q.Get("congestion_control"); value != "cubic" && value != "new_reno" && value != "bbr" {
				return nil, ErrRemoteUnsupported
			}
			m["congestion-controller"] = q.Get("congestion_control")
		}
		if q.Get("udp_relay_mode") != "" {
			if value := q.Get("udp_relay_mode"); value != "native" && value != "quic" {
				return nil, ErrRemoteUnsupported
			}
			m["udp-relay-mode"] = q.Get("udp_relay_mode")
		}
	}
	return encodeYAMLValue(m)
}

func remoteURI(node ResolvedSubscriptionNode) (string, error) {
	if node.RemoteFormat == "mihomo" {
		return remoteYAMLURI(node)
	}
	if node.RemoteFormat != "uri" {
		return "", ErrRemoteUnsupported
	}
	u, err := url.Parse(node.RemotePayload)
	if err != nil || u.Scheme == "" {
		return "", ErrRemoteUnsupported
	}
	u.Fragment = node.Name
	u.RawFragment = ""
	return u.String(), nil
}

func remoteYAMLURI(node ResolvedSubscriptionNode) (string, error) {
	value, err := renderRemoteMihomo(node)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if value.Decode(&m) != nil {
		return "", ErrRemoteUnsupported
	}
	allowed := map[string]bool{"name": true, "type": true, "server": true, "port": true, "udp": true}
	protocolKeys := map[string][]string{
		"vless":     {"uuid", "tls", "servername", "flow", "client-fingerprint", "reality-opts", "network"},
		"ss":        {"cipher", "password"},
		"trojan":    {"password", "sni", "network"},
		"hysteria2": {"password", "sni", "obfs", "obfs-password"},
		"tuic":      {"uuid", "password", "sni", "congestion-controller", "udp-relay-mode"},
	}
	keys, ok := protocolKeys[node.Protocol]
	if !ok {
		return "", ErrRemoteUnsupported
	}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			return "", ErrRemoteUnsupported
		}
	}
	if !remoteStringFieldsValid(m) {
		return "", ErrRemoteUnsupported
	}
	str := func(key string) string { v, _ := m[key].(string); return v }
	if str("type") != node.Protocol {
		return "", ErrRemoteUnsupported
	}
	port, ok := m["port"].(int)
	if !ok || port < 1 || port > 65535 {
		return "", ErrRemoteUnsupported
	}
	host := str("server")
	if host == "" || strings.ContainsAny(host, "/?#@\r\n\t ") {
		return "", ErrRemoteUnsupported
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if strings.ContainsAny(host, "[]") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return "", ErrRemoteUnsupported
	}
	if _, ok := m["udp"]; ok {
		return "", ErrRemoteUnsupported
	}
	if network := str("network"); network != "" && network != "tcp" {
		return "", ErrRemoteUnsupported
	}
	u := url.URL{Scheme: node.Protocol, Host: net.JoinHostPort(host, strconv.Itoa(port)), Fragment: node.Name}
	q := url.Values{}
	switch node.Protocol {
	case "vless":
		if str("uuid") == "" {
			return "", ErrRemoteUnsupported
		}
		if flow := str("flow"); flow != "" && flow != "xtls-rprx-vision" {
			return "", ErrRemoteUnsupported
		}
		u.User = url.User(str("uuid"))
		q.Set("type", "tcp")
		q.Set("encryption", "none")
		tls := false
		if raw, exists := m["tls"]; exists {
			var ok bool
			tls, ok = raw.(bool)
			if !ok {
				return "", ErrRemoteUnsupported
			}
		}
		security := "none"
		if tls {
			security = "tls"
		}
		if raw, exists := m["reality-opts"]; exists {
			opts, ok := raw.(map[string]any)
			if !ok || !tls {
				return "", ErrRemoteUnsupported
			}
			for k := range opts {
				if k != "public-key" && k != "short-id" {
					return "", ErrRemoteUnsupported
				}
			}
			pub, _ := opts["public-key"].(string)
			sid := ""
			if raw, exists := opts["short-id"]; exists {
				var ok bool
				sid, ok = raw.(string)
				if !ok {
					return "", ErrRemoteUnsupported
				}
			}
			if pub == "" {
				return "", ErrRemoteUnsupported
			}
			security = "reality"
			q.Set("pbk", pub)
			if sid != "" {
				q.Set("sid", sid)
			}
		}
		q.Set("security", security)
		for k, v := range map[string]string{"sni": str("servername"), "fp": str("client-fingerprint"), "flow": str("flow")} {
			if v != "" {
				q.Set(k, v)
			}
		}
	case "ss":
		if str("cipher") == "" || str("password") == "" {
			return "", ErrRemoteUnsupported
		}
		if strings.HasPrefix(str("cipher"), "2022-") {
			u.User = url.UserPassword(str("cipher"), str("password"))
		} else {
			u.User = url.User(base64.RawURLEncoding.EncodeToString([]byte(str("cipher") + ":" + str("password"))))
		}
	case "trojan", "hysteria2":
		if str("password") == "" {
			return "", ErrRemoteUnsupported
		}
		u.User = url.User(str("password"))
		if str("sni") != "" {
			q.Set("sni", str("sni"))
		}
		if node.Protocol == "hysteria2" && (str("obfs") != "" || str("obfs-password") != "") {
			if str("obfs") != "salamander" || str("obfs-password") == "" {
				return "", ErrRemoteUnsupported
			}
			q.Set("obfs", str("obfs"))
			q.Set("obfs-password", str("obfs-password"))
		}
	case "tuic":
		if str("uuid") == "" || str("password") == "" {
			return "", ErrRemoteUnsupported
		}
		u.User = url.UserPassword(str("uuid"), str("password"))
		for k, v := range map[string]string{"sni": str("sni"), "congestion_control": str("congestion-controller"), "udp_relay_mode": str("udp-relay-mode")} {
			if v != "" {
				q.Set(k, v)
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func renderRemoteShadowrocket(node ResolvedSubscriptionNode) (string, error) {
	value, err := renderRemoteMihomo(node)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if value.Decode(&m) != nil {
		return "", ErrRemoteUnsupported
	}
	allowed := map[string]bool{"name": true, "type": true, "server": true, "port": true, "udp": true}
	protocolKeys := map[string][]string{
		"vless":  {"uuid", "tls", "servername", "flow", "client-fingerprint", "reality-opts", "network"},
		"ss":     {"cipher", "password"},
		"trojan": {"password", "sni", "network"},
	}
	keys, supported := protocolKeys[node.Protocol]
	if !supported {
		return "", ErrRemoteUnsupported
	}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			return "", ErrRemoteUnsupported
		}
	}
	if !remoteStringFieldsValid(m) {
		return "", ErrRemoteUnsupported
	}
	str := func(key string) string { v, _ := m[key].(string); return v }
	if str("type") != node.Protocol {
		return "", ErrRemoteUnsupported
	}
	port, ok := m["port"].(int)
	if !ok || port < 1 || port > 65535 {
		return "", ErrRemoteUnsupported
	}
	udp := node.RemoteFormat == "uri"
	if raw, exists := m["udp"]; exists {
		var ok bool
		udp, ok = raw.(bool)
		if !ok {
			return "", ErrRemoteUnsupported
		}
	}
	if network := str("network"); network != "" && network != "tcp" {
		return "", ErrRemoteUnsupported
	}
	resolved := ResolvedSubscriptionNode{Name: node.Name, Protocol: node.Protocol, Address: str("server"), Port: port, UUID: str("uuid"), Method: str("cipher"), ShadowsocksPassword: str("password"), Flow: str("flow"), Fingerprint: str("client-fingerprint"), Network: str("network")}
	resolved.ServerName = str("servername")
	if sni := str("sni"); sni != "" {
		if resolved.ServerName != "" {
			return "", ErrRemoteUnsupported
		}
		resolved.ServerName = sni
	}
	if tls, exists := m["tls"]; exists {
		b, ok := tls.(bool)
		if !ok {
			return "", ErrRemoteUnsupported
		}
		resolved.TLS = b
	}
	if reality, exists := m["reality-opts"]; exists {
		if !resolved.TLS {
			return "", ErrRemoteUnsupported
		}
		options, ok := reality.(map[string]any)
		if !ok {
			return "", ErrRemoteUnsupported
		}
		for key := range options {
			if key != "public-key" && key != "short-id" {
				return "", ErrRemoteUnsupported
			}
		}
		resolved.RealityPublicKey, _ = options["public-key"].(string)
		if raw, exists := options["short-id"]; exists {
			var ok bool
			resolved.RealityShortID, ok = raw.(string)
			if !ok {
				return "", ErrRemoteUnsupported
			}
		}
		resolved.Security = "reality"
	}
	switch node.Protocol {
	case "vless", "ss":
		if resolved.Flow != "" && resolved.Flow != "xtls-rprx-vision" {
			return "", ErrRemoteUnsupported
		}
		if node.Protocol == "ss" {
			resolved.Protocol = proxystore.ProtocolShadowsocks
		}
		resolved.RemoteUDP = &udp
		return renderShadowrocketProxy(resolved)
	case "trojan":
		if resolved.Address == "" || resolved.ShadowsocksPassword == "" || !safeShadowrocketValue(resolved.Name) || !safeShadowrocketValue(resolved.Address) || !safeShadowrocketValue(resolved.ShadowsocksPassword) || !safeShadowrocketValue(resolved.ServerName) {
			return "", ErrRemoteUnsupported
		}
		fields := []string{"trojan", shadowrocketValue(resolved.Address), strconv.Itoa(port), "password=" + shadowrocketValue(resolved.ShadowsocksPassword), "tls=true", "udp-relay=" + strconv.FormatBool(udp)}
		if resolved.ServerName != "" {
			fields = append(fields, "peer="+shadowrocketValue(resolved.ServerName))
		}
		if resolved.Fingerprint != "" || resolved.RealityPublicKey != "" || resolved.Flow != "" {
			return "", ErrRemoteUnsupported
		}
		return shadowrocketIdent(node.Name) + " = " + strings.Join(fields, ","), nil
	default:
		return "", ErrRemoteUnsupported
	}
}

func remoteStringFieldsValid(values map[string]any) bool {
	for _, key := range []string{"name", "type", "server", "uuid", "password", "cipher", "servername", "sni", "flow", "network", "client-fingerprint", "obfs", "obfs-password", "congestion-controller", "udp-relay-mode"} {
		if value, exists := values[key]; exists {
			if _, ok := value.(string); !ok {
				return false
			}
		}
	}
	return true
}

func expandedPersonalBindings(data PersonalSubscriptionData) (map[int64]string, RoutingBindings) {
	names := make(map[int64]string, len(data.NodeNames))
	var maxID int64
	for id, name := range data.NodeNames {
		names[id] = name
		if id > maxID {
			maxID = id
		}
	}
	for id := range data.NodeGroupNames {
		if id > maxID {
			maxID = id
		}
	}
	for _, ids := range data.RoutingBindings {
		for _, id := range ids {
			if id > maxID {
				maxID = id
			}
		}
	}
	bindings := make(RoutingBindings, len(data.RoutingBindings))
	for key, ids := range data.RoutingBindings {
		for _, id := range ids {
			if group, ok := data.NodeGroupNames[id]; ok {
				for _, name := range group {
					maxID++
					names[maxID] = name
					bindings[key] = append(bindings[key], maxID)
				}
			} else {
				bindings[key] = append(bindings[key], id)
			}
		}
	}
	return names, bindings
}

func remoteRenderError(index int, cause error) error {
	return fmt.Errorf("第 %d 个远程节点无法转换：%w", index+1, cause)
}
