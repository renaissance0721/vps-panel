package proxy

import (
	"net"
	"strings"
	"unicode/utf8"
)

func normalizeProtocol(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ProtocolVLESS, nil
	}
	if value != ProtocolVLESS && value != ProtocolShadowsocks {
		return "", ErrInvalidProtocol
	}
	return value, nil
}

func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxNameLength {
		return "", ErrInvalidName
	}
	return value, nil
}

func validatePort(value int) error {
	if value < 1 || value > 65535 {
		return ErrInvalidPort
	}
	return nil
}

func normalizeEntryHost(mode, host string) (string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case EntryHostAuto:
		return mode, "", nil
	case EntryHostManual:
		host, err := normalizeHost(host, false)
		if err != nil {
			return "", "", ErrInvalidEntryHost
		}
		return mode, host, nil
	default:
		return "", "", ErrInvalidEntryHostMode
	}
}

func normalizeHost(value string, optional bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return "", nil
	}
	if value == "" || strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return "", ErrInvalidEntryHost
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String(), nil
	}
	if strings.Contains(value, ":") || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", ErrInvalidEntryHost
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidEntryHost
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return "", ErrInvalidEntryHost
			}
		}
	}
	return strings.ToLower(value), nil
}
