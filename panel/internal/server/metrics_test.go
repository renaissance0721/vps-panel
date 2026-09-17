package server

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestReportMetricsUpsertsAndFollowsServerLifecycle(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Metrics Server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	firstUpdatedAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return firstUpdatedAt }
	first := MetricsReport{
		CPUPercent:       32.4,
		MemoryUsedBytes:  128 << 20,
		MemoryTotalBytes: 512 << 20,
		DiskUsedBytes:    5 << 30,
		DiskTotalBytes:   10 << 30,
		UptimeSeconds:    86400,
	}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, first); err != nil {
		t.Fatalf("ReportMetrics() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), firstAgent.ID, firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.Status != StatusOffline || value.Metrics == nil || value.Metrics.CPUPercent != first.CPUPercent ||
		value.Metrics.MemoryUsedBytes != first.MemoryUsedBytes ||
		value.Metrics.MemoryTotalBytes != first.MemoryTotalBytes ||
		value.Metrics.DiskUsedBytes != first.DiskUsedBytes ||
		value.Metrics.DiskTotalBytes != first.DiskTotalBytes ||
		value.Metrics.UptimeSeconds != first.UptimeSeconds ||
		!value.Metrics.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("stored metrics = (%+v, %v)", value.Metrics, err)
	}

	secondUpdatedAt := firstUpdatedAt.Add(5 * time.Second)
	service.now = func() time.Time { return secondUpdatedAt }
	second := MetricsReport{CPUPercent: 100}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, second); err != nil {
		t.Fatalf("second ReportMetrics() error = %v", err)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_metrics WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count server metrics: %v", err)
	}
	updated, err := service.Get(context.Background(), created.ID)
	if err != nil || rowCount != 1 || updated.Metrics == nil || updated.Metrics.CPUPercent != 100 ||
		updated.Metrics.MemoryTotalBytes != 0 || !updated.Metrics.UpdatedAt.Equal(secondUpdatedAt) {
		t.Fatalf("updated metrics = (%+v, rows %d, %v)", updated.Metrics, rowCount, err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	preserved, err := service.Get(context.Background(), created.ID)
	if err != nil || preserved.Metrics == nil || preserved.Metrics.CPUPercent != 100 {
		t.Fatalf("metrics after rebind enrollment = (%+v, %v)", preserved.Metrics, err)
	}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, first); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("old Agent metrics error = %v, want ErrInvalidAgentToken", err)
	}
	secondAgent, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.8.0", true)
	if err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), secondAgent.ID, secondAgent.ServerID, first); err != nil {
		t.Fatalf("replacement ReportMetrics() error = %v", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].Metrics == nil || archived[0].Metrics.CPUPercent != first.CPUPercent {
		t.Fatalf("archived metrics = (%+v, %v)", archived, err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_metrics WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count deleted server metrics: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("server metrics rows after permanent delete = %d, want 0", rowCount)
	}
}

func TestReportMetricsRejectsInvalidValues(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Invalid Metrics")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	invalid := []MetricsReport{
		{CPUPercent: -0.1},
		{CPUPercent: 100.1},
		{CPUPercent: math.NaN()},
		{CPUPercent: math.Inf(1)},
		{MemoryUsedBytes: -1},
		{MemoryUsedBytes: 2, MemoryTotalBytes: 1},
		{DiskUsedBytes: -1},
		{DiskUsedBytes: 2, DiskTotalBytes: 1},
		{UptimeSeconds: -1},
		{HasNetworkUsage: true, NICRXBytes: -1},
		{HasNetworkUsage: true, NICTXBytes: -1},
	}
	for index, report := range invalid {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); !errors.Is(err, ErrInvalidMetrics) {
			t.Fatalf("invalid metrics %d error = %v, want ErrInvalidMetrics", index, err)
		}
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		CPUPercent: 100,
	}); err != nil {
		t.Fatalf("boundary metrics error = %v", err)
	}
}
