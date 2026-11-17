package landing

import (
	"errors"
	"testing"
)

func TestSOCKS5ParsingAndValidation(t *testing.T) {
	for _, tc := range []struct{ raw, host string }{
		{"socks5://user%40example:p%3A%23%25%2F@203.0.113.10:9492#Japan%20Node", "203.0.113.10"},
		{"socks5://user%40example:p%3A%23%25%2F@[2001:db8::1]:9492#Japan%20Node", "2001:db8::1"},
		{"socks5://user%40example:p%3A%23%25%2F@Node.Example.com:9492#Japan%20Node", "node.example.com"},
	} {
		parsed, err := ParseURI(tc.raw)
		if err != nil || parsed.Protocol != ProtocolSOCKS5 || parsed.Host != tc.host || parsed.Port != 9492 || parsed.Username != "user@example" || parsed.Password != "p:#%/" || parsed.Fragment != "Japan Node" {
			t.Fatalf("unexpected parsed result or error: %v", err)
		}
		rewritten, err := RewriteLandingURI(tc.raw, ShareEndpoint{Address: "new.example.com", Port: 1234}, "")
		if err != nil {
			t.Fatal(err)
		}
		reparsed, err := ParseURI(rewritten)
		if err != nil || reparsed.Username != parsed.Username || reparsed.Password != parsed.Password || reparsed.Host != "new.example.com" {
			t.Fatalf("rewrite lost fields: %v", err)
		}
	}
	for _, raw := range []string{"socks5://example.com:80", "socks5://user@example.com:80", "socks5://:pass@example.com:80", "socks5://user:@example.com:80", "socks5://user:pass@:80", "socks5://user:pass@bad_host:80", "socks5://user:pass@example.com:0", "socks5://user:pass@example.com:65536", "socks5://user:pass@example.com:nope", "socks5://user:pass@[2001:db8::1:80", "socks5://user:%ZZ@example.com:80", "socks5://user:pass@example.com:80/path", "socks5://user:pass@example.com:80?udp=1", "socks5://user:%0A@example.com:80"} {
		if _, err := ParseURI(raw); !errors.Is(err, ErrInvalidURI) {
			t.Errorf("expected invalid URI for test case, got %v", err)
		}
	}
}

func TestSOCKS5ServicePrivateUpdateAndPermissions(t *testing.T) {
	s, owner, other := newLandingTestService(t)
	node, err := s.Create(t.Context(), owner, CreateInput{NodeRole: "landing", URI: "socks5://user:pass@203.0.113.10:9492"})
	if err != nil || node.Protocol != ProtocolSOCKS5 || node.Visibility != VisibilityPrivate || node.NodeRole != "landing" || node.Name != "SOCKS5 外部节点" {
		t.Fatalf("create: %+v %v", node, err)
	}
	if _, err := s.GetURI(t.Context(), node.ID, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private URI access: %v", err)
	}
	if values, err := s.List(t.Context(), other); err != nil || len(values) != 0 {
		t.Fatalf("private list: %v", err)
	}
	replacement := "socks5://other%40user:new%3Apass@[2001:db8::1]:1234#New"
	if _, _, err := s.Update(t.Context(), node.ID, other, UpdateInput{URI: &replacement}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized edit: %v", err)
	}
	updated, changed, err := s.Update(t.Context(), node.ID, owner, UpdateInput{URI: &replacement})
	if err != nil || !changed || updated.Host != "2001:db8::1" || updated.Port != 1234 {
		t.Fatalf("update: %v", err)
	}
	saved, err := s.GetURI(t.Context(), node.ID, owner)
	if err != nil || saved != replacement {
		t.Fatalf("URI retrieval: %v", err)
	}
}
