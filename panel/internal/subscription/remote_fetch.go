package subscription

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	remoteSubscriptionMaxBytes  = 4 << 20
	remoteSubscriptionTimeout   = 20 * time.Second
	remoteSubscriptionRedirects = 3
)

var (
	errRemoteURL      = errors.New("远程订阅地址无效或不允许访问")
	errRemoteFetch    = errors.New("无法获取远程订阅")
	errRemoteStatus   = errors.New("远程订阅服务器返回错误")
	errRemoteTooLarge = errors.New("远程订阅内容超过大小限制")
)

// Block special-purpose networks even where IsGlobalUnicast reports true.
// This also rejects transition networks which can tunnel to private IPv4.
var remoteBlockedNetworks = func() []netip.Prefix {
	values := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32",
		"2002::/16", "3fff::/20", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}()

func remotePublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() {
		return false
	}
	for _, block := range remoteBlockedNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func validateRemoteURL(value *url.URL) error {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Opaque != "" ||
		value.Hostname() == "" || value.Fragment != "" || strings.Contains(value.Hostname(), "%") {
		return errRemoteURL
	}
	host := strings.TrimSuffix(strings.ToLower(value.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		host == "metadata.google.internal" {
		return errRemoteURL
	}
	if ip, err := netip.ParseAddr(host); err == nil && !remotePublicIP(ip) {
		return errRemoteURL
	}
	port := value.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errRemoteURL
		}
	}
	return nil
}

type remoteResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type remoteFetcher struct {
	resolver remoteResolver
	dial     func(context.Context, string, string) (net.Conn, error)
}

func newRemoteFetcher() *remoteFetcher {
	return &remoteFetcher{resolver: net.DefaultResolver, dial: (&net.Dialer{Timeout: 8 * time.Second}).DialContext}
}

// dialPublic resolves once, rejects mixed public/private answers, and connects
// directly to a checked IP. The transport still uses the original host for TLS.
func (f *remoteFetcher) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errRemoteURL
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = f.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > 32 {
			return nil, errRemoteFetch
		}
	}
	for _, ip := range addresses {
		if !remotePublicIP(ip) {
			return nil, errRemoteURL
		}
	}
	for _, ip := range addresses {
		conn, err := f.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err != nil {
			continue
		}
		remote, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			conn.Close()
			return nil, errRemoteURL
		}
		connected, ok := netip.AddrFromSlice(remote.IP)
		if !ok || !remotePublicIP(connected) || connected.Unmap() != ip.Unmap() {
			conn.Close()
			return nil, errRemoteURL
		}
		return conn, nil
	}
	return nil, errRemoteFetch
}

type remoteFetchResult struct {
	body             []byte
	subscriptionInfo string
}

func (f *remoteFetcher) fetch(ctx context.Context, rawURL, userAgent string) (remoteFetchResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || validateRemoteURL(parsed) != nil || len(rawURL) > 8192 || len(userAgent) > 512 || strings.ContainsAny(userAgent, "\r\n") {
		return remoteFetchResult{}, errRemoteURL
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSubscriptionTimeout)
	defer cancel()
	transport := &http.Transport{
		Proxy: nil, DialContext: f.dialPublic, DisableKeepAlives: true,
		TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > remoteSubscriptionRedirects || validateRemoteURL(req.URL) != nil || req.URL.User != nil ||
			(via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errRemoteURL
		}
		// The Referer would otherwise disclose a token in the original URL.
		req.Header.Del("Referer")
		if !strings.EqualFold(req.URL.Host, via[len(via)-1].URL.Host) {
			req.Header.Del("Authorization")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return remoteFetchResult{}, errRemoteURL
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		// net/http's error includes the complete URL, which can contain tokens.
		return remoteFetchResult{}, errRemoteFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return remoteFetchResult{}, errRemoteStatus
	}
	if resp.ContentLength > remoteSubscriptionMaxBytes {
		return remoteFetchResult{}, errRemoteTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, remoteSubscriptionMaxBytes+1))
	if err != nil {
		return remoteFetchResult{}, errRemoteFetch
	}
	if len(body) > remoteSubscriptionMaxBytes {
		return remoteFetchResult{}, errRemoteTooLarge
	}
	return remoteFetchResult{body: body, subscriptionInfo: resp.Header.Get("Subscription-Userinfo")}, nil
}
