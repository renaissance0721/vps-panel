package server

import (
	"errors"
	"reflect"
	"testing"
)

func TestServerNetworkMetadata(t *testing.T) {
	s, db := newTestService(t)
	ctx := t.Context()
	created, err := s.Create(ctx, "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if created.BandwidthMbps != nil || created.NetworkStack != nil {
		t.Fatal("new server metadata must be unset")
	}
	agent, err := s.RegisterAgent(ctx, created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportSystemInfo(ctx, agent.ID, created.ID, SystemInfoReport{
		Hostname: "host", OSName: "Linux", IPv4: []string{"198.51.100.1"}, IPv6: []string{"2606:4700::1111"},
		PublicIPv4: "198.51.100.1", PublicIPv6: "2606:4700::1111",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET applied_config_version = 1, config_sync_status = 'success', config_sync_error = '', config_synced_at = 123 WHERE id = ?`, agent.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	bandwidth := int64(500)
	for _, stack := range []string{NetworkStackIPv4, NetworkStackIPv6, NetworkStackDual} {
		updated, err := s.UpdateNetworkMetadata(ctx, created.ID, UpdateNetworkMetadataInput{
			BandwidthMbps: &bandwidth, NetworkStack: &stack, SetBandwidthMbps: true, SetNetworkStack: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		listed, err := s.List(ctx)
		if err != nil || len(listed) != 1 {
			t.Fatalf("list: %v", err)
		}
		got, err := s.Get(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []Server{updated, got, listed[0]} {
			if value.BandwidthMbps == nil || *value.BandwidthMbps != 500 || value.NetworkStack == nil || *value.NetworkStack != stack {
				t.Fatal("metadata did not round trip")
			}
			if value.DesiredStateVersion != before.DesiredStateVersion || value.OutboundPreference != before.OutboundPreference ||
				value.BlockChinaInbound != before.BlockChinaInbound || !reflect.DeepEqual(value.SystemInfo, before.SystemInfo) ||
				value.AgentConfigSyncStatus != before.AgentConfigSyncStatus || value.AgentConfigSyncError != before.AgentConfigSyncError ||
				value.AgentAppliedConfigVersion != before.AgentAppliedConfigVersion || !reflect.DeepEqual(value.AgentConfigSyncedAt, before.AgentConfigSyncedAt) {
				t.Fatal("metadata update changed runtime state or observed addresses")
			}
		}
	}
	cleared, err := s.UpdateNetworkMetadata(ctx, created.ID, UpdateNetworkMetadataInput{SetBandwidthMbps: true})
	if err != nil || cleared.BandwidthMbps != nil || cleared.NetworkStack == nil || *cleared.NetworkStack != NetworkStackDual {
		t.Fatalf("clear bandwidth independently: %v", err)
	}
	cleared, err = s.UpdateNetworkMetadata(ctx, created.ID, UpdateNetworkMetadataInput{SetNetworkStack: true})
	if err != nil || cleared.NetworkStack != nil {
		t.Fatalf("clear stack: %v", err)
	}
	var operations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM configuration_operations WHERE server_id = ?`, created.ID).Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("metadata created configuration operations: %d, %v", operations, err)
	}
	rotated, err := s.CreateEnrollment(ctx, created.ID)
	if err != nil || rotated.BandwidthMbps != nil || rotated.NetworkStack != nil {
		t.Fatalf("enrollment metadata: %v", err)
	}
	for _, status := range []string{"pending", "failed"} {
		if _, err := db.Exec(`UPDATE servers SET decommission_status = ? WHERE id = ?`, status, created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateNetworkMetadata(ctx, created.ID, UpdateNetworkMetadataInput{SetBandwidthMbps: true}); !errors.Is(err, ErrDecommissioning) {
			t.Fatalf("decommission update: %v", err)
		}
	}
	if _, err := db.Exec(`UPDATE servers SET archived_at = 123 WHERE id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNetworkMetadata(ctx, created.ID, UpdateNetworkMetadataInput{SetBandwidthMbps: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived update: %v", err)
	}
}

func TestServerNetworkMetadataValidation(t *testing.T) {
	s, _ := newTestService(t)
	created, err := s.Create(t.Context(), "validation")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int64{0, -1, MaxBandwidthMbps + 1} {
		if _, err := s.CreateWithSettings(t.Context(), CreateServerInput{Name: "invalid", BandwidthMbps: &value}); !errors.Is(err, ErrInvalidBandwidth) {
			t.Fatalf("create bandwidth %d: %v", value, err)
		}
		if _, err := s.UpdateNetworkMetadata(t.Context(), created.ID, UpdateNetworkMetadataInput{BandwidthMbps: &value, SetBandwidthMbps: true}); !errors.Is(err, ErrInvalidBandwidth) {
			t.Fatalf("update bandwidth %d: %v", value, err)
		}
	}
	for _, value := range []string{"", "unknown", "IPv4", "ipv4_only", " dual "} {
		if _, err := s.CreateWithSettings(t.Context(), CreateServerInput{Name: "invalid", NetworkStack: &value}); !errors.Is(err, ErrInvalidNetworkStack) {
			t.Fatalf("create stack %q: %v", value, err)
		}
		if _, err := s.UpdateNetworkMetadata(t.Context(), created.ID, UpdateNetworkMetadataInput{NetworkStack: &value, SetNetworkStack: true}); !errors.Is(err, ErrInvalidNetworkStack) {
			t.Fatalf("update stack %q: %v", value, err)
		}
	}
	for _, value := range []int64{1, 500, MaxBandwidthMbps} {
		stack := NetworkStackDual
		got, err := s.CreateWithSettings(t.Context(), CreateServerInput{Name: "valid", BandwidthMbps: &value, NetworkStack: &stack})
		if err != nil || got.BandwidthMbps == nil || *got.BandwidthMbps != value || got.NetworkStack == nil || *got.NetworkStack != stack {
			t.Fatalf("create valid metadata %d: %v", value, err)
		}
	}
}
