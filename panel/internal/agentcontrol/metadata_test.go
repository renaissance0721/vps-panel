package agentcontrol

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeMetadataValidatesIdentityAndNormalizesCapabilities(t *testing.T) {
	metadata, err := NormalizeMetadata(Metadata{
		Implementation: " io.github.example.agent ",
		Version:        " v1.2.3 ",
		APIVersion:     CurrentAPIVersion,
		Capabilities:   []string{"metrics", "diagnostics_v1", "metrics"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCapabilities := []string{"diagnostics_v1", "metrics"}
	if metadata.Implementation != "io.github.example.agent" || metadata.Version != "v1.2.3" ||
		!reflect.DeepEqual(metadata.Capabilities, wantCapabilities) {
		t.Fatalf("normalized metadata = %+v", metadata)
	}

	invalid := []Metadata{
		{Implementation: "UPPER", Version: "v1", APIVersion: 1},
		{Implementation: "valid", Version: "v1", APIVersion: 0},
		{Version: "v1", APIVersion: 1},
		{Implementation: "valid", Version: "v1", APIVersion: 2},
		{Implementation: "valid", Version: "v1", APIVersion: 1, Capabilities: []string{"bad capability"}},
	}
	for _, value := range invalid {
		if _, err := NormalizeMetadata(value); err == nil {
			t.Fatalf("NormalizeMetadata(%+v) succeeded", value)
		}
	}
}

func TestSelfUpgradeIdentityBoundary(t *testing.T) {
	if !CanSelfUpgrade("", 0, nil) {
		t.Fatal("Legacy Agent lost upgrade compatibility")
	}
	if !CanSelfUpgrade(OfficialImplementation, CurrentAPIVersion, []string{CapabilitySelfUpgrade}) {
		t.Fatal("official Agent with self_upgrade was rejected")
	}
	if CanSelfUpgrade("io.github.matthewlu070111.boardray", CurrentAPIVersion, []string{CapabilitySelfUpgrade}) ||
		CanSelfUpgrade(OfficialImplementation, CurrentAPIVersion, []string{CapabilityMetrics}) {
		t.Fatal("identified Agent crossed the self-upgrade boundary")
	}
	if err := ValidateImplementationMatch("third.party", OfficialImplementation); !errors.Is(err, ErrAgentImplementationMismatch) {
		t.Fatalf("implementation switch error = %v", err)
	}
}

func TestSupportsCapabilityKeepsLegacyCompatibilityAndEnforcesV1(t *testing.T) {
	if !SupportsCapability(Metadata{}, CapabilityRelayRealm) {
		t.Fatal("Legacy Agent capability should remain unknown and allowed")
	}
	identified := Metadata{
		Implementation: "third-party-agent",
		APIVersion:     CurrentAPIVersion,
		Capabilities:   []string{CapabilityProxyVLESSReality},
	}
	if !SupportsCapability(identified, CapabilityProxyVLESSReality) {
		t.Fatal("declared API v1 capability was rejected")
	}
	if SupportsCapability(identified, CapabilityProxyVLESSACME) {
		t.Fatal("undeclared API v1 capability was allowed")
	}
}

func TestDeclaresCapabilityHasNoLegacyFallback(t *testing.T) {
	if DeclaresCapability(Metadata{}, CapabilityFirewallCNBlock) {
		t.Fatal("Legacy Agent declared the China firewall capability")
	}
	identified := Metadata{
		Implementation: "third-party-agent",
		APIVersion:     CurrentAPIVersion,
		Capabilities:   []string{CapabilityFirewallCNBlock},
	}
	if !DeclaresCapability(identified, CapabilityFirewallCNBlock) {
		t.Fatal("explicit API v1 China firewall capability was rejected")
	}
	identified.Capabilities = nil
	if DeclaresCapability(identified, CapabilityFirewallCNBlock) {
		t.Fatal("undeclared China firewall capability was allowed")
	}
}
