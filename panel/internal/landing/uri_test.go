package landing

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseVLESSURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		host     string
		port     int
		fragment string
		wantErr  error
	}{
		{"IPv4", "vless://uuid@1.2.3.4:443?type=tcp#US", "1.2.3.4", 443, "US", nil},
		{"domain", "vless://uuid@Node.Example.com:8443?security=reality&pbk=abc", "node.example.com", 8443, "", nil},
		{"IPv6", "vless://uuid@[2001:db8::1]:443", "2001:db8::1", 443, "", nil},
		{"type absent", "vless://uuid@example.com:443", "example.com", 443, "", nil},
		{"unsupported transport", "vless://uuid@example.com:443?type=quic", "", 0, "", ErrUnsupportedVLESSTransport},
		{"missing user", "vless://example.com:443", "", 0, "", ErrInvalidURI},
		{"missing host", "vless://uuid@:443", "", 0, "", ErrInvalidURI},
		{"missing port", "vless://uuid@example.com", "", 0, "", ErrInvalidURI},
		{"unknown", "trojan://secret@example.com:443", "", 0, "", ErrUnsupportedProtocol},
		{"control", "vless://uuid@example.com:443\n", "", 0, "", ErrInvalidURI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseURI(test.uri)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseURI() error = %v, want %v", err, test.wantErr)
			}
			if err == nil && (parsed.Protocol != ProtocolVLESS || parsed.Host != test.host || parsed.Port != test.port || parsed.Fragment != test.fragment) {
				t.Fatalf("ParseURI() = %+v", parsed)
			}
		})
	}
}

func TestParseShadowsocksURI(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:p@ssword"))
	paddedStdUserinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:password>"))
	legacyStd := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secret@example.com:8388"))
	legacyRawURL := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret@[2001:db8::2]:8389"))
	tests := []struct {
		name     string
		uri      string
		host     string
		port     int
		fragment string
		wantErr  error
	}{
		{"plain SIP002", "ss://aes-256-gcm:password@1.2.3.4:8388#IPv4", "1.2.3.4", 8388, "IPv4", nil},
		{"base64 userinfo raw URL", "ss://" + userinfo + "@ss.example.com:8388#Domain", "ss.example.com", 8388, "Domain", nil},
		{"base64 userinfo padded std", "ss://" + paddedStdUserinfo + "@ss.example.com:8388", "ss.example.com", 8388, "", nil},
		{"IPv6", "ss://aes-128-gcm:password@[2001:db8::1]:8388", "2001:db8::1", 8388, "", nil},
		{"legacy padded std", "ss://" + legacyStd + "#Legacy", "example.com", 8388, "Legacy", nil},
		{"legacy raw URL IPv6", "ss://" + legacyRawURL, "2001:db8::2", 8389, "", nil},
		{"plugin", "ss://aes-256-gcm:password@example.com:8388?plugin=v2ray-plugin", "", 0, "", ErrUnsupportedSSPlugin},
		{"invalid base64", "ss://not_base64@example.com:8388", "", 0, "", ErrInvalidURI},
		{"missing port", "ss://aes-256-gcm:password@example.com", "", 0, "", ErrInvalidURI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseURI(test.uri)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseURI() error = %v, want %v", err, test.wantErr)
			}
			if err == nil && (parsed.Protocol != ProtocolSS || parsed.Host != test.host || parsed.Port != test.port || parsed.Fragment != test.fragment) {
				t.Fatalf("ParseURI() = %+v", parsed)
			}
		})
	}
}

func TestRewriteLandingURIPreservesProtocolMaterial(t *testing.T) {
	vless := "vless://uuid-value@landing.example.com:443?security=reality&type=tcp&sni=a.com&pbk=abc&sid=def&flow=xtls-rprx-vision#US%20Home"
	rewritten, err := RewriteLandingURI(vless, ShareEndpoint{Address: "2001:db8::10", Port: 9502}, "Tokyo Relay")
	if err != nil {
		t.Fatal(err)
	}
	originalURL, _ := url.Parse(vless)
	rewrittenURL, _ := url.Parse(rewritten)
	if rewrittenURL.Host != "[2001:db8::10]:9502" || rewrittenURL.User.String() != originalURL.User.String() ||
		rewrittenURL.RawQuery != originalURL.RawQuery || rewrittenURL.Fragment != "US Home - Tokyo Relay" {
		t.Fatalf("rewritten VLESS = %q", rewritten)
	}
	for _, key := range []string{"sni", "pbk", "sid", "flow"} {
		if rewrittenURL.Query().Get(key) != originalURL.Query().Get(key) {
			t.Fatalf("VLESS query %s changed", key)
		}
	}

	standard := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@landing.example.com:8388?mode=tcp#US"
	rewritten, err = RewriteLandingURI(standard, ShareEndpoint{Address: "relay.example.com", Port: 9503}, "Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	originalURL, _ = url.Parse(standard)
	rewrittenURL, _ = url.Parse(rewritten)
	if rewrittenURL.Host != "relay.example.com:9503" || rewrittenURL.User.String() != originalURL.User.String() ||
		rewrittenURL.RawQuery != originalURL.RawQuery || rewrittenURL.Fragment != "US - Tokyo" {
		t.Fatalf("rewritten SIP002 = %q", rewritten)
	}

	legacyToken := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:p@ssword@landing.example.com:8388"))
	rewritten, err = RewriteLandingURI("ss://"+legacyToken+"#Legacy", ShareEndpoint{Address: "2001:db8::20", Port: 9504}, "Relay")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(strings.TrimPrefix(rewritten, "ss://"), "#", 2)
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || string(decoded) != "aes-256-gcm:p@ssword@[2001:db8::20]:9504" || len(parts) != 2 || parts[1] != "Legacy%20-%20Relay" {
		t.Fatalf("rewritten legacy = %q, decoded %q, error %v", rewritten, decoded, err)
	}
	paddedLegacy := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:password1@1.2.3.4:8388"))
	rewritten, err = RewriteLandingURI("ss://"+paddedLegacy, ShareEndpoint{Address: "relay.example.com", Port: 9505}, "Relay")
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(rewritten, "ss://"), "#Relay")
	decoded, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "aes-128-gcm:password1@relay.example.com:9505" {
		t.Fatalf("rewritten padded legacy = %q, decoded %q, error %v", rewritten, decoded, err)
	}
}
