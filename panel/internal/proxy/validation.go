package proxy

import (
	"net"
	"strings"
	"unicode/utf8"
)

// These are the protocol implementations actually managed by this Panel, not
// every inbound that an upstream kernel may support.
var protocolKernels = map[string][]string{
	ProtocolVLESS:       {KernelXray},
	ProtocolShadowsocks: {KernelXray},
	ProtocolHysteria2:   {KernelSingbox},
}

func supportsKernel(protocol, kernel string) bool {
	for _, supported := range protocolKernels[protocol] {
		if supported == kernel {
			return true
		}
	}
	return false
}

func validatePortHopping(settings *storedHysteria2, listenPort int) error {
	if err := validateLegacyPortHopping(settings, listenPort); err != nil {
		return err
	}
	if settings == nil || !settings.PortHoppingEnabled {
		return nil
	}
	if settings.PortHoppingStart < 1024 || settings.PortHoppingEnd-settings.PortHoppingStart+1 > 10000 {
		return ErrInvalidPortHopping
	}
	return nil
}

// Existing databases can contain ranges accepted before schema v52. Keep
// reading and returning them so they can be viewed and corrected safely.
func validateLegacyPortHopping(settings *storedHysteria2, listenPort int) error {
	if settings == nil || !settings.PortHoppingEnabled {
		return nil
	}
	if settings.PortHoppingStart < 1 || settings.PortHoppingEnd > 65535 || settings.PortHoppingStart > settings.PortHoppingEnd ||
		(listenPort >= settings.PortHoppingStart && listenPort <= settings.PortHoppingEnd) {
		return ErrInvalidPortHopping
	}
	return nil
}

func normalizeProtocol(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ProtocolVLESS, nil
	}
	if _, ok := protocolKernels[value]; !ok {
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

func normalizeListenFamily(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ListenFamilyIPv4, nil
	}
	if value != ListenFamilyIPv4 && value != ListenFamilyIPv6 {
		return "", ErrInvalidListenFamily
	}
	return value, nil
}

func validateEntryHostFamily(family, host string) error {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return nil
	}
	if (family == ListenFamilyIPv4) != (ip.To4() != nil) {
		return ErrInvalidEntryHost
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
