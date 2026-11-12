package subscription

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type remoteTestResolver map[string][]netip.Addr

func (r remoteTestResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

type remoteTestResolverFunc func(string) []netip.Addr

func (r remoteTestResolverFunc) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r(host), nil
}

type remoteTestConn struct {
	net.Conn
	address net.Addr
}

func (c remoteTestConn) RemoteAddr() net.Addr { return c.address }

// All network traffic stays on the local httptest server. The wrapper simulates
// the public remote address that a real direct connection would report.
func remoteTestFetcher(server *httptest.Server) *remoteFetcher {
	return &remoteFetcher{
		resolver: remoteTestResolver{
			"public.example": {netip.MustParseAddr("8.8.8.8")},
			"other.example":  {netip.MustParseAddr("8.8.4.4")},
			"mixed.example":  {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
		},
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			endpoint, _ := url.Parse(server.URL)
			conn, err := (&net.Dialer{}).DialContext(ctx, network, endpoint.Host)
			if err != nil {
				return nil, err
			}
			ip, port, _ := net.SplitHostPort(address)
			remote, _ := net.ResolveTCPAddr("tcp", net.JoinHostPort(ip, port))
			return remoteTestConn{Conn: conn, address: remote}, nil
		},
	}
}

func TestRemotePublicIPRejectsSpecialNetworks(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "100.64.0.1", "169.254.169.254", "192.0.2.1", "198.18.0.1", "224.0.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "2002:0808:0808::1", "64:ff9b::a00:1"} {
		if remotePublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("unsafe IP %s accepted", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !remotePublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("public IP %s rejected", raw)
		}
	}
}

func TestRemoteFetchProtectsAddressAndRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect-local":
			http.Redirect(w, r, "http://127.0.0.1/metadata", http.StatusFound)
		case "/redirect-mixed":
			http.Redirect(w, r, "http://mixed.example/path", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/cross-host":
			http.Redirect(w, r, "http://other.example/check", http.StatusFound)
		case "/check":
			if r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
				t.Error("redirect leaked original credentials or Referer")
			}
			_, _ = io.WriteString(w, "ok")
		case "/too-large":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, strings.Repeat("x", remoteSubscriptionMaxBytes+1))
		case "/ok":
			if r.UserAgent() != "panel-test" {
				t.Errorf("unexpected user agent %q", r.UserAgent())
			}
			w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=30")
			_, _ = io.WriteString(w, "synthetic subscription")
		default:
			http.Error(w, "private response", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	fetcher := remoteTestFetcher(server)
	result, err := fetcher.fetch(t.Context(), "http://public.example/ok?token=synthetic-secret", "panel-test")
	if err != nil || string(result.body) != "synthetic subscription" || result.subscriptionInfo == "" {
		t.Fatalf("fetch result = %#v, error = %v", result, err)
	}
	if _, err := fetcher.fetch(t.Context(), "http://synthetic:secret@public.example/cross-host?token=synthetic-secret", ""); err != nil {
		t.Fatalf("cross-host redirect: %v", err)
	}
	for _, raw := range []string{
		"file:///etc/passwd", "http://127.0.0.1/", "http://169.254.169.254/latest/meta-data/",
		"http://mixed.example/", "http://public.example/redirect-local", "http://public.example/redirect-mixed", "http://public.example/loop",
	} {
		if _, err := fetcher.fetch(t.Context(), raw, ""); err == nil {
			t.Errorf("unexpectedly allowed %s", raw)
		}
	}
	if _, err := fetcher.fetch(t.Context(), "http://public.example/too-large", ""); !errors.Is(err, errRemoteTooLarge) {
		t.Fatalf("oversized response error = %v", err)
	}
	if _, err := fetcher.fetch(t.Context(), "http://public.example/fail?token=synthetic-secret", ""); err == nil || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "private response") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestRemoteFetchVerifiesActualConnectedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	fetcher := remoteTestFetcher(server)
	fetcher.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		endpoint, _ := url.Parse(server.URL)
		return (&net.Dialer{}).DialContext(ctx, network, endpoint.Host)
	}
	if _, err := fetcher.fetch(t.Context(), "http://public.example/?token=synthetic-secret", ""); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("actual local address accepted or leaked: %v", err)
	}
}

func TestRemoteFetchRechecksDNSAfterRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/secret", http.StatusFound)
			return
		}
		t.Error("redirect reached forbidden endpoint")
	}))
	defer server.Close()
	fetcher := remoteTestFetcher(server)
	lookups := 0
	fetcher.resolver = remoteTestResolverFunc(func(_ string) []netip.Addr {
		lookups++
		if lookups == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	})
	if _, err := fetcher.fetch(t.Context(), "http://public.example/start", ""); err == nil || lookups != 2 {
		t.Fatalf("DNS rebinding not rejected: lookups=%d, error=%v", lookups, err)
	}
}
