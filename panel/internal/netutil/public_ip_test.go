package netutil

import "testing"

func TestIPv6StackAndPublicAddressAreIndependent(t *testing.T) {
	for _, test := range []struct {
		name      string
		addresses []string
		stack     bool
		public    string
	}{
		{name: "ula", addresses: []string{"fd00:33bc:9d0b::10"}, stack: true},
		{name: "public", addresses: []string{"2606:4700:4700::1111"}, stack: true, public: "2606:4700:4700::1111"},
		{name: "link local", addresses: []string{"fe80::1"}},
		{name: "loopback", addresses: []string{"::1"}},
		{name: "unspecified", addresses: []string{"::"}},
		{name: "ipv4", addresses: []string{"198.51.100.10"}},
		{name: "invalid", addresses: []string{"not-an-ip"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := HasIPv6Stack(test.addresses); got != test.stack {
				t.Fatalf("HasIPv6Stack() = %v, want %v", got, test.stack)
			}
			if got := EffectivePublicIPv6("", test.addresses); got != test.public {
				t.Fatalf("EffectivePublicIPv6() = %q, want %q", got, test.public)
			}
		})
	}
}
