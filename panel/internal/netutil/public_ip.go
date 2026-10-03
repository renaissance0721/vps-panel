package netutil

import (
	"net"
	"strings"
)

// EffectivePublicIPv6 returns the explicitly detected public address, or the
// first usable address reported by an older Agent.
func EffectivePublicIPv6(publicIPv6 string, addresses []string) string {
	for _, value := range append([]string{publicIPv6}, addresses...) {
		ip := net.ParseIP(strings.TrimSpace(value))
		if UsablePublicIPv6(ip) {
			return ip.String()
		}
	}
	return ""
}

func UsablePublicIPv6(ip net.IP) bool {
	return ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() &&
		!ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}
