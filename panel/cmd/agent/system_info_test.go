package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseOSRelease(t *testing.T) {
	name, version := parseOSRelease([]byte(`# comment
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION="12 (bookworm)"
VERSION_ID='12'
`))
	if name != "Debian GNU/Linux" || version != "12" {
		t.Fatalf("parseOSRelease() = (%q, %q), want Debian GNU/Linux 12", name, version)
	}

	name, version = parseOSRelease([]byte("PRETTY_NAME=Ubuntu\nVERSION=24.04 LTS\n"))
	if name != "Ubuntu" || version != "24.04 LTS" {
		t.Fatalf("fallback parseOSRelease() = (%q, %q)", name, version)
	}

	name, version = parseOSRelease([]byte("NAME=\"Alpine Linux\"\nVERSION_ID=3.22.1\n"))
	if name != "Alpine Linux" || version != "3.22.1" {
		t.Fatalf("Alpine parseOSRelease() = (%q, %q)", name, version)
	}
}

func TestDetectPublicIPv4(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(" 198.51.100.24\n"))
	}))
	defer server.Close()
	if value := detectPublicIPv4(context.Background(), server.Client(), server.URL); value != "198.51.100.24" {
		t.Fatalf("detected public IPv4 = %q", value)
	}

	for _, response := range []string{"not-an-ip", "2001:db8::1", "10.0.0.1", strings.Repeat("1", publicIPv4ResponseLimit+1)} {
		invalidServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(response))
		}))
		if value := detectPublicIPv4(context.Background(), invalidServer.Client(), invalidServer.URL); value != "" {
			invalidServer.Close()
			t.Fatalf("invalid response %q produced %q", response, value)
		}
		invalidServer.Close()
	}
}

func TestDetectPublicIPv4TimeoutReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("198.51.100.25"))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Millisecond
	if value := detectPublicIPv4(context.Background(), client, server.URL); value != "" {
		t.Fatalf("timed out detection produced %q", value)
	}
}

func TestCollectIPAddressesFiltersClassifiesAndDeduplicates(t *testing.T) {
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
		&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("224.0.0.1"), Mask: net.CIDRMask(4, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("203.0.113.10"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("ff02::1"), Mask: net.CIDRMask(16, 128)},
		&net.IPNet{IP: net.ParseIP("2001:db8::10"), Mask: net.CIDRMask(64, 128)},
	}
	ipv4, ipv6 := collectIPAddresses(addresses)
	if len(ipv4) != 2 || ipv4[0] != "192.168.1.20" || ipv4[1] != "203.0.113.10" {
		t.Fatalf("IPv4 addresses = %v, want sorted private and public addresses", ipv4)
	}
	if len(ipv6) != 1 || ipv6[0] != "2001:db8::10" {
		t.Fatalf("IPv6 addresses = %v, want [2001:db8::10]", ipv6)
	}
}

func TestCollectIPAddressesLimitsEachFamily(t *testing.T) {
	addresses := make([]net.Addr, 0, 40)
	for value := 1; value <= 20; value++ {
		addresses = append(addresses,
			&net.IPNet{IP: net.IPv4(10, 0, 0, byte(value)), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.IP{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(value)}, Mask: net.CIDRMask(64, 128)},
		)
	}
	ipv4, ipv6 := collectIPAddresses(addresses)
	if len(ipv4) != maxReportedIPAddresses || len(ipv6) != maxReportedIPAddresses {
		t.Fatalf("limited address counts = (%d, %d), want (%d, %d)",
			len(ipv4), len(ipv6), maxReportedIPAddresses, maxReportedIPAddresses)
	}
}

func TestCollectSystemInfoUsesBestEffortFallbacks(t *testing.T) {
	message := collectSystemInfoWith(
		func() (string, error) { return "", errors.New("hostname unavailable") },
		func(string) ([]byte, error) { return nil, errors.New("file unavailable") },
		func() ([]net.Addr, error) { return nil, errors.New("network unavailable") },
	)
	if message.Type != "system_info" || message.Hostname != "" || message.OSName != "Linux" ||
		message.OSVersion != "" || message.Kernel != "" || message.Arch != runtime.GOARCH {
		t.Fatalf("best-effort system information = %+v", message)
	}
	if message.IPv4 == nil || len(message.IPv4) != 0 || message.IPv6 == nil || len(message.IPv6) != 0 {
		t.Fatalf("missing addresses = (%v, %v), want empty arrays", message.IPv4, message.IPv6)
	}
}
