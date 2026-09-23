package agentcontrol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	CurrentAPIVersion      = 1
	OfficialImplementation = "vps-panel-agent"

	CapabilityProxyVLESSACME     = "proxy.vless.tls.acme"
	CapabilityProxyVLESSManual   = "proxy.vless.tls.manual"
	CapabilityProxyVLESSReality  = "proxy.vless.reality"
	CapabilityProxyShadowsocks   = "proxy.shadowsocks"
	CapabilityRelayRealm         = "relay.realm"
	CapabilityOutboundPreference = "outbound_preference"
	CapabilityMetrics            = "metrics"
	CapabilityClientTraffic      = "client_traffic"
	CapabilityDiagnosticsV1      = "diagnostics_v1"
	CapabilitySelfUpgrade        = "self_upgrade"

	maxImplementationLength = 128
	maxCapabilities         = 64
	maxCapabilityLength     = 64
)

// Metadata identifies an Agent implementation and the protocol features it declares.
type Metadata struct {
	Implementation string
	Version        string
	APIVersion     int
	Capabilities   []string
}

func NormalizeMetadata(value Metadata) (Metadata, error) {
	value.Implementation = strings.TrimSpace(value.Implementation)
	value.Version = strings.TrimSpace(value.Version)
	if utf8.RuneCountInString(value.Version) > 64 {
		return Metadata{}, ErrInvalidAgentVersion
	}
	if value.APIVersion != 0 && value.APIVersion != CurrentAPIVersion {
		return Metadata{}, fmt.Errorf("%w: %d", ErrUnsupportedAgentAPI, value.APIVersion)
	}
	if value.APIVersion == CurrentAPIVersion && value.Version == "" {
		return Metadata{}, ErrInvalidAgentVersion
	}
	if value.APIVersion == 0 && value.Implementation != "" {
		return Metadata{}, fmt.Errorf("%w: legacy Agent cannot declare an implementation", ErrInvalidAgentMetadata)
	}
	if value.APIVersion == CurrentAPIVersion && value.Implementation == "" {
		return Metadata{}, fmt.Errorf("%w: Agent API v1 requires an implementation", ErrInvalidAgentMetadata)
	}
	if value.Implementation != "" &&
		(len(value.Implementation) > maxImplementationLength || !validMetadataIdentifier(value.Implementation)) {
		return Metadata{}, fmt.Errorf("%w: invalid implementation", ErrInvalidAgentMetadata)
	}
	capabilities, err := NormalizeCapabilities(value.Capabilities)
	if err != nil {
		return Metadata{}, err
	}
	value.Capabilities = capabilities
	return value, nil
}

func NormalizeCapabilities(values []string) ([]string, error) {
	if len(values) > maxCapabilities {
		return nil, fmt.Errorf("%w: too many capabilities", ErrInvalidAgentMetadata)
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxCapabilityLength || !validMetadataIdentifier(value) {
			return nil, fmt.Errorf("%w: invalid capability", ErrInvalidAgentMetadata)
		}
		unique[value] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func ParseCapabilityHeader(value string) ([]string, error) {
	return NormalizeCapabilities(splitCapabilities(value))
}

func (value Metadata) CapabilitySet() map[string]bool {
	result := make(map[string]bool, len(value.Capabilities))
	for _, capability := range value.Capabilities {
		result[capability] = true
	}
	return result
}

func SupportsCapability(value Metadata, capability string) bool {
	if value.Implementation == "" && value.APIVersion == 0 {
		return true
	}
	if value.Implementation == "" || value.APIVersion != CurrentAPIVersion {
		return false
	}
	for _, declared := range value.Capabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

func EncodeCapabilities(values []string) (string, error) {
	normalized, err := NormalizeCapabilities(values)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode Agent capabilities: %w", err)
	}
	return string(encoded), nil
}

func DecodeCapabilities(value string) ([]string, error) {
	var capabilities []string
	if err := json.Unmarshal([]byte(value), &capabilities); err != nil {
		return nil, fmt.Errorf("decode Agent capabilities: %w", err)
	}
	return NormalizeCapabilities(capabilities)
}

func ValidateImplementationMatch(stored, connected string) error {
	if stored != "" && stored != connected {
		return ErrAgentImplementationMismatch
	}
	return nil
}

func CanSelfUpgrade(implementation string, apiVersion int, capabilities []string) bool {
	hasSelfUpgrade := false
	for _, capability := range capabilities {
		if capability == CapabilitySelfUpgrade {
			hasSelfUpgrade = true
			break
		}
	}
	return canSelfUpgrade(implementation, apiVersion, hasSelfUpgrade)
}

func CanSelfUpgradeConnection(implementation string, apiVersion int, capabilities map[string]bool) bool {
	return canSelfUpgrade(implementation, apiVersion, capabilities[CapabilitySelfUpgrade])
}

func canSelfUpgrade(implementation string, apiVersion int, hasSelfUpgrade bool) bool {
	// Legacy fallback only exists to preserve upgrades for Agents deployed before identity metadata.
	if implementation == "" && apiVersion == 0 {
		return true
	}
	if implementation != OfficialImplementation || apiVersion != CurrentAPIVersion {
		return false
	}
	return hasSelfUpgrade
}

func validMetadataIdentifier(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return value != ""
}
