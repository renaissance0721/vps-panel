package landing

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type base64Style struct {
	encoding *base64.Encoding
}

func ParseURI(raw string) (ParsedURI, error) {
	raw, err := normalizeRawURI(raw)
	if err != nil {
		return ParsedURI{}, err
	}
	schemeEnd := strings.Index(raw, ":")
	if schemeEnd <= 0 {
		return ParsedURI{}, ErrUnsupportedProtocol
	}
	switch strings.ToLower(raw[:schemeEnd]) {
	case ProtocolVLESS:
		return parseVLESS(raw)
	case "ss":
		return parseShadowsocks(raw)
	default:
		return ParsedURI{}, ErrUnsupportedProtocol
	}
}

func RewriteLandingURI(raw string, endpoint ShareEndpoint, relayName string) (string, error) {
	parsed, err := ParseURI(raw)
	if err != nil {
		return "", err
	}
	host, err := normalizeHost(endpoint.Address)
	if err != nil || !validPort(endpoint.Port) {
		return "", ErrInvalidURI
	}
	raw = strings.TrimSpace(raw)
	if parsed.Protocol == ProtocolSS {
		if isLegacyShadowsocks(raw) {
			return rewriteLegacyShadowsocks(raw, host, endpoint.Port, relayName)
		}
		return rewriteStandardShadowsocks(raw, host, endpoint.Port, relayName)
	}
	value, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalidURI
	}
	value.Host = net.JoinHostPort(host, strconv.Itoa(endpoint.Port))
	value.Fragment = relayFragment(value.Fragment, relayName)
	value.RawFragment = ""
	return value.String(), nil
}

func normalizeRawURI(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > maxURIBytes || !utf8.ValidString(raw) {
		return "", ErrInvalidURI
	}
	for _, character := range raw {
		if character < 0x20 || character == 0x7f {
			return "", ErrInvalidURI
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURIBytes {
		return "", ErrInvalidURI
	}
	return raw, nil
}

func parseVLESS(raw string) (ParsedURI, error) {
	value, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(value.Scheme, ProtocolVLESS) || value.User == nil || strings.TrimSpace(value.User.Username()) == "" {
		return ParsedURI{}, ErrInvalidURI
	}
	host, port, err := parseHostPort(value)
	if err != nil {
		return ParsedURI{}, err
	}
	transport := strings.ToLower(strings.TrimSpace(value.Query().Get("type")))
	if transport != "" && transport != "tcp" {
		return ParsedURI{}, ErrUnsupportedVLESSTransport
	}
	return ParsedURI{Protocol: ProtocolVLESS, Host: host, Port: port, Fragment: value.Fragment}, nil
}

func parseShadowsocks(raw string) (ParsedURI, error) {
	if isLegacyShadowsocks(raw) {
		return parseLegacyShadowsocks(raw)
	}
	userinfo, endpoint, rawQuery, fragment, err := splitStandardShadowsocks(raw)
	if err != nil {
		return ParsedURI{}, ErrInvalidURI
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ParsedURI{}, ErrInvalidURI
	}
	if hasPlugin(query) {
		return ParsedURI{}, ErrUnsupportedSSPlugin
	}
	decodedUserinfo, err := url.PathUnescape(userinfo)
	if err != nil {
		return ParsedURI{}, ErrInvalidURI
	}
	if !validShadowsocksCredential(decodedUserinfo) {
		decoded, _, decodeErr := decodeBase64(decodedUserinfo)
		if decodeErr != nil || !validShadowsocksCredential(string(decoded)) {
			return ParsedURI{}, ErrInvalidURI
		}
	}
	host, port, err := parseEndpoint(endpoint)
	if err != nil {
		return ParsedURI{}, err
	}
	return ParsedURI{Protocol: ProtocolSS, Host: host, Port: port, Fragment: fragment}, nil
}

func parseLegacyShadowsocks(raw string) (ParsedURI, error) {
	token, rawQuery, fragment, _, err := splitLegacyShadowsocks(raw)
	if err != nil {
		return ParsedURI{}, err
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ParsedURI{}, ErrInvalidURI
	}
	if hasPlugin(query) {
		return ParsedURI{}, ErrUnsupportedSSPlugin
	}
	decoded, _, err := decodeBase64(token)
	if err != nil {
		return ParsedURI{}, ErrInvalidURI
	}
	separator := strings.LastIndexByte(string(decoded), '@')
	if separator <= 0 || separator == len(decoded)-1 {
		return ParsedURI{}, ErrInvalidURI
	}
	credential, endpoint := string(decoded[:separator]), string(decoded[separator+1:])
	if !validShadowsocksCredential(credential) {
		return ParsedURI{}, ErrInvalidURI
	}
	host, port, err := parseEndpoint(endpoint)
	if err != nil {
		return ParsedURI{}, err
	}
	return ParsedURI{Protocol: ProtocolSS, Host: host, Port: port, Fragment: fragment}, nil
}

func validShadowsocksCredential(value string) bool {
	method, password, ok := strings.Cut(value, ":")
	return ok && strings.TrimSpace(method) != "" && password != ""
}

func splitStandardShadowsocks(raw string) (userinfo, endpoint, rawQuery, fragment string, err error) {
	if len(raw) < len("ss://") || !strings.EqualFold(raw[:len("ss://")], "ss://") || isLegacyShadowsocks(raw) {
		return "", "", "", "", ErrInvalidURI
	}
	rest := raw[len("ss://"):]
	if index := strings.Index(rest, "#"); index >= 0 {
		fragment, err = url.PathUnescape(rest[index+1:])
		if err != nil {
			return "", "", "", "", ErrInvalidURI
		}
		rest = rest[:index]
	}
	if index := strings.Index(rest, "?"); index >= 0 {
		rawQuery = rest[index+1:]
		rest = rest[:index]
	}
	separator := strings.LastIndexByte(rest, '@')
	if separator <= 0 || separator == len(rest)-1 {
		return "", "", "", "", ErrInvalidURI
	}
	return rest[:separator], rest[separator+1:], rawQuery, fragment, nil
}

func parseHostPort(value *url.URL) (string, int, error) {
	if value.Hostname() == "" || value.Port() == "" {
		return "", 0, ErrInvalidURI
	}
	port, err := strconv.Atoi(value.Port())
	if err != nil || !validPort(port) {
		return "", 0, ErrInvalidURI
	}
	host, err := normalizeHost(value.Hostname())
	if err != nil {
		return "", 0, ErrInvalidURI
	}
	return host, port, nil
}

func parseEndpoint(endpoint string) (string, int, error) {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, ErrInvalidURI
	}
	port, err := strconv.Atoi(portText)
	if err != nil || !validPort(port) {
		return "", 0, ErrInvalidURI
	}
	host, err = normalizeHost(host)
	if err != nil {
		return "", 0, ErrInvalidURI
	}
	return host, port, nil
}

func normalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") || strings.HasSuffix(value, "]") {
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return "", ErrInvalidURI
		}
		value = value[1 : len(value)-1]
	}
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return "", ErrInvalidURI
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	if strings.Contains(value, ":") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", ErrInvalidURI
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidURI
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return "", ErrInvalidURI
			}
		}
	}
	return strings.ToLower(value), nil
}

func validPort(port int) bool { return port >= 1 && port <= 65535 }

func hasPlugin(query url.Values) bool {
	for key := range query {
		if strings.EqualFold(key, "plugin") {
			return true
		}
	}
	return false
}

func isLegacyShadowsocks(raw string) bool {
	if len(raw) < len("ss://") || !strings.EqualFold(raw[:len("ss://")], "ss://") {
		return false
	}
	authority := raw[len("ss://"):]
	if index := strings.IndexAny(authority, "?#"); index >= 0 {
		authority = authority[:index]
	}
	return !strings.Contains(authority, "@")
}

func splitLegacyShadowsocks(raw string) (token, rawQuery, fragment, rawFragment string, err error) {
	if !isLegacyShadowsocks(raw) {
		return "", "", "", "", ErrInvalidURI
	}
	rest := raw[len("ss://"):]
	if index := strings.Index(rest, "#"); index >= 0 {
		rawFragment = rest[index+1:]
		rest = rest[:index]
		fragment, err = url.PathUnescape(rawFragment)
		if err != nil {
			return "", "", "", "", ErrInvalidURI
		}
	}
	if index := strings.Index(rest, "?"); index >= 0 {
		rawQuery = rest[index+1:]
		rest = rest[:index]
	}
	if rest == "" {
		return "", "", "", "", ErrInvalidURI
	}
	return rest, rawQuery, fragment, rawFragment, nil
}

func decodeBase64(value string) ([]byte, base64Style, error) {
	var encodings []*base64.Encoding
	switch {
	case strings.ContainsAny(value, "-_"):
		if strings.Contains(value, "=") {
			encodings = []*base64.Encoding{base64.URLEncoding}
		} else {
			encodings = []*base64.Encoding{base64.RawURLEncoding}
		}
	case strings.ContainsAny(value, "+/"):
		if strings.Contains(value, "=") {
			encodings = []*base64.Encoding{base64.StdEncoding}
		} else {
			encodings = []*base64.Encoding{base64.RawStdEncoding}
		}
	case strings.Contains(value, "="):
		encodings = []*base64.Encoding{base64.StdEncoding, base64.URLEncoding}
	default:
		encodings = []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding}
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, base64Style{encoding: encoding}, nil
		}
	}
	return nil, base64Style{}, fmt.Errorf("decode base64: %w", ErrInvalidURI)
}

func rewriteLegacyShadowsocks(raw, host string, port int, relayName string) (string, error) {
	token, rawQuery, fragment, _, err := splitLegacyShadowsocks(raw)
	if err != nil {
		return "", err
	}
	decoded, style, err := decodeBase64(token)
	if err != nil {
		return "", ErrInvalidURI
	}
	separator := strings.LastIndexByte(string(decoded), '@')
	if separator <= 0 || separator == len(decoded)-1 {
		return "", ErrInvalidURI
	}
	credential := string(decoded[:separator])
	if !validShadowsocksCredential(credential) {
		return "", ErrInvalidURI
	}
	rewritten := credential + "@" + net.JoinHostPort(host, strconv.Itoa(port))
	result := "ss://" + style.encoding.EncodeToString([]byte(rewritten))
	if rawQuery != "" {
		result += "?" + rawQuery
	}
	fragment = relayFragment(fragment, relayName)
	if fragment != "" {
		fragmentURL := (&url.URL{Fragment: fragment}).String()
		result += fragmentURL
	}
	return result, nil
}

func rewriteStandardShadowsocks(raw, host string, port int, relayName string) (string, error) {
	userinfo, _, rawQuery, fragment, err := splitStandardShadowsocks(raw)
	if err != nil {
		return "", err
	}
	result := "ss://" + userinfo + "@" + net.JoinHostPort(host, strconv.Itoa(port))
	if rawQuery != "" {
		result += "?" + rawQuery
	}
	fragment = relayFragment(fragment, relayName)
	if fragment != "" {
		result += (&url.URL{Fragment: fragment}).String()
	}
	return result, nil
}

func relayFragment(original, relayName string) string {
	relayName = strings.TrimSpace(relayName)
	if relayName == "" {
		return original
	}
	if original == "" {
		return relayName
	}
	return original + " - " + relayName
}
