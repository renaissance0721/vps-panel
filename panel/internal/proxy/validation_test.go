package proxy

import (
	"errors"
	"testing"
)

func TestManualEntryHostMatchesListenFamily(t *testing.T) {
	for _, test := range []struct {
		family string
		host   string
		valid  bool
	}{
		{ListenFamilyIPv4, "198.51.100.10", true},
		{ListenFamilyIPv4, "2606:4700:4700::1111", false},
		{ListenFamilyIPv6, "2606:4700:4700::1111", true},
		{ListenFamilyIPv6, "198.51.100.10", false},
		{ListenFamilyIPv4, "node.example.com", true},
		{ListenFamilyIPv6, "node.example.com", true},
	} {
		err := validateEntryHostFamily(test.family, test.host)
		if test.valid && err != nil {
			t.Fatalf("validateEntryHostFamily(%q, %q) error = %v", test.family, test.host, err)
		}
		if !test.valid && !errors.Is(err, ErrInvalidEntryHost) {
			t.Fatalf("validateEntryHostFamily(%q, %q) error = %v, want ErrInvalidEntryHost", test.family, test.host, err)
		}
	}
}
