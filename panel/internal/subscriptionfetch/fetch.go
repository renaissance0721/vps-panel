package subscriptionfetch

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
	MaxBytes                    = 4 << 20
	Timeout                     = 20 * time.Second
	remoteSubscriptionRedirects = 3
)

var (
	ErrURL      = errors.New("远程订阅地址无效或不允许访问")
	ErrFetch    = errors.New("无法获取远程订阅")
	ErrStatus   = errors.New("远程订阅服务器返回错误")
	ErrTimeout  = errors.New("远程订阅下载超时")
	ErrTooLarge = errors.New("远程订阅内容超过大小限制")
)

// Block special-purpose networks even where IsGlobalUnicast reports true.
// This also rejects transition networks which can tunnel to private IPv4.
var blockedNetworks = func() []netip.Prefix {
	values := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"168.63.129.16/32",
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

var globalIPv6 = netip.MustParsePrefix("2000::/3")

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || (ip.Is6() && !globalIPv6.Contains(ip)) {
		return false
	}
	for _, block := range blockedNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func ValidateURL(value *url.URL) error {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Opaque != "" ||
		value.Hostname() == "" || value.Fragment != "" || strings.Contains(value.Hostname(), "%") {
		return ErrURL
	}
	host := strings.TrimSuffix(strings.ToLower(value.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		host == "metadata.google.internal" {
		return ErrURL
	}
	if ip, err := netip.ParseAddr(host); err == nil && !PublicIP(ip) {
		return ErrURL
	}
	port := value.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ErrURL
		}
	}
	return nil
}

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Fetcher struct {
	Resolver Resolver
	Dial     func(context.Context, string, string) (net.Conn, error)
}

func New() *Fetcher {
	return &Fetcher{Resolver: net.DefaultResolver, Dial: (&net.Dialer{Timeout: 8 * time.Second}).DialContext}
}

// dialPublic resolves once, rejects mixed public/private answers, and connects
// directly to a checked IP. The transport still uses the original host for TLS.
func (f *Fetcher) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrURL
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = f.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > 32 {
			return nil, ErrFetch
		}
	}
	for _, ip := range addresses {
		if !PublicIP(ip) {
			return nil, ErrURL
		}
	}
	for _, ip := range addresses {
		conn, err := f.Dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err != nil {
			continue
		}
		remote, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			conn.Close()
			return nil, ErrURL
		}
		connected, ok := netip.AddrFromSlice(remote.IP)
		if !ok || !PublicIP(connected) || connected.Unmap() != ip.Unmap() {
			conn.Close()
			return nil, ErrURL
		}
		return conn, nil
	}
	return nil, ErrFetch
}

type Result struct {
	Status           int
	Body             []byte
	SubscriptionInfo string
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL, userAgent string) (Result, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || ValidateURL(parsed) != nil || len(rawURL) > 8192 || len(userAgent) > 512 || strings.ContainsAny(userAgent, "\r\n") {
		return Result{}, ErrURL
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	transport := &http.Transport{
		Proxy: nil, DialContext: f.dialPublic, DisableKeepAlives: true,
		TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > remoteSubscriptionRedirects || ValidateURL(req.URL) != nil || req.URL.User != nil ||
			(via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return ErrURL
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
		return Result{}, ErrURL
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		// net/http's error includes the complete URL, which can contain tokens.
		return Result{}, safeFetchError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Status: resp.StatusCode}, ErrStatus
	}
	if resp.ContentLength > MaxBytes {
		return Result{}, ErrTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return Result{}, safeFetchError(ctx, err)
	}
	if len(body) > MaxBytes {
		return Result{}, ErrTooLarge
	}
	return Result{Status: resp.StatusCode, Body: body, SubscriptionInfo: resp.Header.Get("Subscription-Userinfo")}, nil
}

func safeFetchError(ctx context.Context, err error) error {
	if errors.Is(err, ErrURL) {
		return ErrURL
	}
	var timeout net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return ErrTimeout
	}
	return ErrFetch
}

func Metadata(value string) (upload, download, total, expire *int64) {
	for _, item := range strings.Split(value, ";") {
		key, raw, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			upload = &n
		case "download":
			download = &n
		case "total":
			total = &n
		case "expire":
			if n <= 253402300799 {
				expire = &n
			}
		}
	}
	return
}
