package subscription

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const remoteMaxNodes = 10000

var errRemoteContent = errors.New("远程订阅格式无效")

// Payload contains credentials and must be encrypted before persistence; it
// must never be included in ordinary API responses, logs, or warnings.
type remoteParsedNode struct {
	name, protocol, format, payload, identity string
}

type remoteParseResult struct {
	nodes    []remoteParsedNode
	warnings []string
}

func remoteProtocol(value string) string {
	switch strings.ToLower(value) {
	case "vless", "ss", "trojan", "tuic":
		return strings.ToLower(value)
	case "hysteria2", "hy2":
		return "hysteria2"
	default:
		return ""
	}
}

func remoteDisplayName(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxNameRunes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func remoteDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func parseRemoteSubscription(body []byte) (remoteParseResult, error) {
	if len(body) == 0 || len(body) > remoteSubscriptionMaxBytes || !utf8.Valid(body) || bytes.ContainsRune(body, 0) {
		return remoteParseResult{}, errRemoteContent
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return remoteParseResult{}, errRemoteContent
	}
	if !remoteStartsWithURI(text) {
		if decoded, ok := decodeRemoteBase64(text); ok && remoteStartsWithURI(string(decoded)) {
			text = string(decoded)
		} else {
			return parseRemoteYAML([]byte(text))
		}
	}
	result := remoteParseResult{}
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(result.nodes)+len(result.warnings) >= remoteMaxNodes || len(line) > 32<<10 {
			return remoteParseResult{}, errRemoteContent
		}
		parsed, err := url.Parse(line)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 行节点格式无效", index+1))
			continue
		}
		protocol := remoteProtocol(parsed.Scheme)
		if protocol == "" {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 行节点协议不支持", index+1))
			continue
		}
		name := strings.TrimSpace(parsed.Fragment)
		if !remoteDisplayName(name) {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 行节点名称无效", index+1))
			continue
		}
		parsed.Fragment, parsed.RawFragment = "", ""
		result.nodes = append(result.nodes, remoteParsedNode{
			name: name, protocol: protocol, format: "uri", payload: line,
			identity: remoteDigest([]byte(parsed.String())),
		})
	}
	return finishRemoteParse(result)
}

func remoteStartsWithURI(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		scheme, _, found := strings.Cut(line, "://")
		if !found || scheme == "" {
			return false
		}
		for _, r := range scheme {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
				return false
			}
		}
		return true
	}
	return false
}

func decodeRemoteBase64(value string) ([]byte, bool) {
	value = strings.Join(strings.Fields(value), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(value)
		if err == nil && len(decoded) <= remoteSubscriptionMaxBytes && utf8.Valid(decoded) && !bytes.ContainsRune(decoded, 0) {
			return decoded, true
		}
	}
	return nil, false
}

func parseRemoteYAML(body []byte) (remoteParseResult, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !remoteSafeYAML(document.Content[0]) {
		return remoteParseResult{}, errRemoteContent
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return remoteParseResult{}, errRemoteContent
	}
	proxies := mappingValue(document.Content[0], "proxies")
	if proxies == nil || proxies.Kind != yaml.SequenceNode || len(proxies.Content) > remoteMaxNodes {
		return remoteParseResult{}, errRemoteContent
	}
	result := remoteParseResult{}
	for index, node := range proxies.Content {
		if node.Kind != yaml.MappingNode {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 个节点格式无效", index+1))
			continue
		}
		nameNode, typeNode := mappingValue(node, "name"), mappingValue(node, "type")
		if nameNode == nil || typeNode == nil || nameNode.Kind != yaml.ScalarNode || typeNode.Kind != yaml.ScalarNode || !remoteDisplayName(strings.TrimSpace(nameNode.Value)) {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 个节点名称或协议无效", index+1))
			continue
		}
		protocol := remoteProtocol(typeNode.Value)
		if protocol == "" {
			result.warnings = append(result.warnings, fmt.Sprintf("第 %d 个节点协议不支持", index+1))
			continue
		}
		payload, err := yaml.Marshal(node)
		if err != nil {
			return remoteParseResult{}, errRemoteContent
		}
		var canonical map[string]any
		if err := node.Decode(&canonical); err != nil {
			return remoteParseResult{}, errRemoteContent
		}
		delete(canonical, "name")
		identity, err := json.Marshal(canonical)
		if err != nil {
			return remoteParseResult{}, errRemoteContent
		}
		result.nodes = append(result.nodes, remoteParsedNode{
			name: strings.TrimSpace(nameNode.Value), protocol: protocol, format: "mihomo", payload: string(payload),
			identity: remoteDigest(identity),
		})
	}
	return finishRemoteParse(result)
}

func remoteSafeYAML(root *yaml.Node) bool {
	type entry struct {
		node  *yaml.Node
		depth int
	}
	stack := []entry{{node: root}}
	visited := 0
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		node := item.node
		if visited > 200000 || item.depth > 64 || node.Kind == yaml.AliasNode || node.Anchor != "" {
			return false
		}
		switch node.Tag {
		case "", "!!map", "!!seq", "!!str", "!!bool", "!!int", "!!float", "!!null":
		default:
			return false
		}
		if node.Kind == yaml.MappingNode {
			if len(node.Content)%2 != 0 {
				return false
			}
			seen := make(map[string]bool, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
					return false
				}
				seen[key.Value] = true
			}
		}
		for _, child := range node.Content {
			stack = append(stack, entry{node: child, depth: item.depth + 1})
		}
	}
	return true
}

func finishRemoteParse(result remoteParseResult) (remoteParseResult, error) {
	if len(result.nodes) == 0 {
		return result, errRemoteContent
	}
	identities := make(map[string]bool, len(result.nodes))
	unique := make([]remoteParsedNode, 0, len(result.nodes))
	for _, node := range result.nodes {
		if identities[node.identity] {
			result.warnings = append(result.warnings, "发现重复节点，已保留第一项")
			continue
		}
		identities[node.identity] = true
		unique = append(unique, node)
	}
	counts := make(map[string]int, len(unique))
	for _, node := range unique {
		counts[node.name]++
	}
	// Reserve non-duplicate names first, so generated names cannot take one.
	used := make(map[string]bool, len(unique))
	for _, node := range unique {
		if counts[node.name] == 1 {
			used[node.name] = true
		}
	}
	indices := make([]int, 0, len(unique))
	for index, node := range unique {
		if counts[node.name] > 1 {
			indices = append(indices, index)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := unique[indices[i]], unique[indices[j]]
		if left.name != right.name {
			return left.name < right.name
		}
		return left.identity < right.identity
	})
	for _, index := range indices {
		node := &unique[index]
		for ordinal := 1; ; ordinal++ {
			suffix := " (" + strconv.Itoa(ordinal) + ")"
			base := []rune(node.name)
			if len(base)+len(suffix) > maxNameRunes {
				base = base[:maxNameRunes-len(suffix)]
			}
			candidate := string(base) + suffix
			if !used[candidate] {
				node.name = candidate
				used[candidate] = true
				break
			}
		}
		result.warnings = append(result.warnings, "发现重名节点，已添加序号")
	}
	result.nodes = unique
	return result, nil
}
