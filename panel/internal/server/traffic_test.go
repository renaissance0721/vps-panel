package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReportMetricsAccumulatesTrafficAndHandlesCounterReset(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Delta")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC) }
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000,
	}); err != nil {
		t.Fatalf("first ReportMetrics() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1300, NICTXBytes: 2600,
	}); err != nil {
		t.Fatalf("growing ReportMetrics() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 100, NICTXBytes: 2700,
	}); err != nil {
		t.Fatalf("RX reset ReportMetrics() error = %v", err)
	}
	adjusted, err := service.UpdateTrafficAdjustment(context.Background(), created.ID, 1000)
	if err != nil || adjusted.Metrics == nil || adjusted.Metrics.TrafficAdjustmentBytes != 300 {
		t.Fatalf("set traffic adjustment before restart = (%+v, %v)", adjusted.Metrics, err)
	}

	restartedService := NewService(db)
	restartedService.now = service.now
	if err := restartedService.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 150, NICTXBytes: 50,
	}); err != nil {
		t.Fatalf("TX reset after Panel restart error = %v", err)
	}
	value, err := restartedService.Get(context.Background(), created.ID)
	if err != nil || value.Metrics == nil {
		t.Fatalf("Get() traffic = (%+v, %v)", value.Metrics, err)
	}
	if value.Metrics.NICRXBytes != 150 || value.Metrics.NICTXBytes != 50 ||
		value.Metrics.CycleRXBytes != 350 || value.Metrics.CycleTXBytes != 700 ||
		value.Metrics.TrafficAdjustmentBytes != 300 || value.TrafficUsedBytes() != 1000 {
		t.Fatalf("traffic after counter resets = %+v, used %d", value.Metrics, value.TrafficUsedBytes())
	}
}

func TestTrafficConfigurationControlsUsageAndUnlimitedLimit(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Config")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.MonthlyTrafficLimitBytes != nil || created.TrafficCountMode != TrafficSingle ||
		created.TrafficResetDay != 1 || created.TrafficResetTime != "00:00" {
		t.Fatalf("new server traffic defaults = %+v", created.Server)
	}
	limit := int64(500 << 30)
	updated, err := service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &limit,
		CountMode:         TrafficBidirectional,
		ResetDay:          15,
		ResetTime:         "08:30",
	})
	if err != nil || updated.MonthlyTrafficLimitBytes == nil || *updated.MonthlyTrafficLimitBytes != limit ||
		updated.TrafficCountMode != TrafficBidirectional || updated.TrafficResetDay != 15 || updated.TrafficResetTime != "08:30" {
		t.Fatalf("UpdateTrafficConfig() = (%+v, %v)", updated, err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC) }
	for _, report := range []MetricsReport{
		{HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000},
		{HasNetworkUsage: true, NICRXBytes: 1100, NICTXBytes: 2200},
	} {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); err != nil {
			t.Fatalf("ReportMetrics() error = %v", err)
		}
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.TrafficUsedBytes() != 300 {
		t.Fatalf("bidirectional traffic = (%d, %v), want 300", value.TrafficUsedBytes(), err)
	}
	zero := int64(0)
	value, err = service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &zero,
		CountMode:         TrafficSingle,
		ResetDay:          1,
		ResetTime:         "00:00",
	})
	if err != nil || value.MonthlyTrafficLimitBytes != nil || value.TrafficUsedBytes() != 200 {
		t.Fatalf("unlimited single traffic = (limit %v, used %d, error %v)",
			value.MonthlyTrafficLimitBytes, value.TrafficUsedBytes(), err)
	}
}

func TestTrafficAdjustmentCalibratesDisplayedUsageWithoutChangingCounters(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Adjustment")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, nic_rx_bytes, nic_tx_bytes,
		  cycle_rx_bytes, cycle_tx_bytes, cycle_started_at, updated_at)
		 VALUES (?, 1, 2, 3, 4, 5, 6, 1000, 2000, 20, 100, 10, 11)`,
		created.ID,
	); err != nil {
		t.Fatalf("insert traffic metrics: %v", err)
	}

	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.MeasuredTrafficUsedBytes() != 100 || value.TrafficUsedBytes() != 100 ||
		value.Metrics == nil || value.Metrics.TrafficAdjustmentBytes != 0 {
		t.Fatalf("initial measured traffic = (%+v, measured %d, used %d, %v)",
			value.Metrics, value.MeasuredTrafficUsedBytes(), value.TrafficUsedBytes(), err)
	}
	value, err = service.UpdateTrafficAdjustment(context.Background(), created.ID, 500)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 500 {
		t.Fatalf("positive adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	restarted := NewService(db)
	value, err = restarted.Get(context.Background(), created.ID)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 500 {
		t.Fatalf("persisted adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		CountMode: TrafficBidirectional, ResetDay: 1, ResetTime: "00:00",
	})
	if err != nil || value.MeasuredTrafficUsedBytes() != 120 ||
		value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 520 {
		t.Fatalf("adjustment after mode change = (measured %d, adjustment %d, used %d, %v)",
			value.MeasuredTrafficUsedBytes(), value.Metrics.TrafficAdjustmentBytes, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 50)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != -70 || value.TrafficUsedBytes() != 50 {
		t.Fatalf("negative adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 0)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != -120 || value.TrafficUsedBytes() != 0 {
		t.Fatalf("zero target = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 120)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 0 || value.TrafficUsedBytes() != 120 {
		t.Fatalf("target equal to measured usage = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	if _, err := db.Exec(
		`UPDATE server_metrics SET traffic_adjustment_bytes = -1000 WHERE server_id = ?`, created.ID,
	); err != nil {
		t.Fatalf("set oversized negative adjustment: %v", err)
	}
	value, err = restarted.Get(context.Background(), created.ID)
	if err != nil || value.TrafficUsedBytes() != 0 {
		t.Fatalf("clamped adjusted usage = (%d, %v), want 0", value.TrafficUsedBytes(), err)
	}
	value, err = restarted.ClearTrafficAdjustment(context.Background(), created.ID)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 0 || value.TrafficUsedBytes() != 120 {
		t.Fatalf("cleared adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	limit := int64(200)
	if _, err := restarted.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &limit, CountMode: TrafficBidirectional, ResetDay: 1, ResetTime: "00:00",
	}); err != nil {
		t.Fatalf("set traffic limit: %v", err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 500)
	if err != nil || value.TrafficUsedBytes() != 500 {
		t.Fatalf("over-limit adjustment = (%d, %v), want 500", value.TrafficUsedBytes(), err)
	}
	if _, err := restarted.UpdateTrafficAdjustment(context.Background(), created.ID, -1); !errors.Is(err, ErrInvalidTrafficTarget) {
		t.Fatalf("negative target error = %v, want ErrInvalidTrafficTarget", err)
	}
}

func assertTrafficCounters(
	t *testing.T,
	metrics *Metrics,
	nicRX, nicTX, cycleRX, cycleTX, cycleStarted int64,
) {
	t.Helper()
	if metrics == nil || metrics.NICRXBytes != nicRX || metrics.NICTXBytes != nicTX ||
		metrics.CycleRXBytes != cycleRX || metrics.CycleTXBytes != cycleTX ||
		metrics.CycleStartedAt == nil || metrics.CycleStartedAt.Unix() != cycleStarted {
		t.Fatalf("traffic counters changed = %+v", metrics)
	}
}

func TestReportMetricsStartsNewTrafficCycleAtConfiguredBoundary(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Cycle")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		CountMode: TrafficSingle, ResetDay: 15, ResetTime: "08:00",
	}); err != nil {
		t.Fatalf("UpdateTrafficConfig() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC) }
	for _, report := range []MetricsReport{
		{HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000},
		{HasNetworkUsage: true, NICRXBytes: 1100, NICTXBytes: 2200},
	} {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); err != nil {
			t.Fatalf("pre-boundary ReportMetrics() error = %v", err)
		}
	}
	adjusted, err := service.UpdateTrafficAdjustment(context.Background(), created.ID, 1000)
	if err != nil || adjusted.Metrics == nil || adjusted.Metrics.TrafficAdjustmentBytes != 800 {
		t.Fatalf("set pre-boundary adjustment = (%+v, %v)", adjusted.Metrics, err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 15, 0, 1, 0, 0, time.UTC) }
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1300, NICTXBytes: 2500,
	}); err != nil {
		t.Fatalf("new-cycle ReportMetrics() error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	wantStart := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if err != nil || value.Metrics == nil || value.Metrics.CycleRXBytes != 0 || value.Metrics.CycleTXBytes != 0 ||
		value.Metrics.TrafficAdjustmentBytes != 0 || value.Metrics.NICRXBytes != 1300 || value.Metrics.NICTXBytes != 2500 ||
		value.Metrics.CycleStartedAt == nil || !value.Metrics.CycleStartedAt.Equal(wantStart) {
		t.Fatalf("new traffic cycle = (%+v, %v), want zero at %v", value.Metrics, err, wantStart)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1350, NICTXBytes: 2580,
	}); err != nil {
		t.Fatalf("post-boundary ReportMetrics() error = %v", err)
	}
	value, err = service.Get(context.Background(), created.ID)
	if err != nil || value.Metrics.CycleRXBytes != 50 || value.Metrics.CycleTXBytes != 80 {
		t.Fatalf("post-boundary traffic = (%+v, %v)", value.Metrics, err)
	}
}

func TestTrafficCycleStartClampsMissingMonthDays(t *testing.T) {
	for _, test := range []struct {
		name string
		now  time.Time
		day  int
		want time.Time
	}{
		{
			name: "February 29 in non-leap year", day: 29,
			now:  time.Date(2027, 2, 28, 1, 0, 0, 0, time.UTC),
			want: time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "February 30 in leap year", day: 30,
			now:  time.Date(2028, 2, 29, 1, 0, 0, 0, time.UTC),
			want: time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "April 31", day: 31,
			now:  time.Date(2027, 4, 30, 1, 0, 0, 0, time.UTC),
			want: time.Date(2027, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "before March 31 boundary", day: 31,
			now:  time.Date(2027, 3, 30, 23, 0, 0, 0, time.UTC),
			want: time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := trafficCycleStart(test.now, test.day, "08:00")
			if err != nil || !got.Equal(test.want) {
				t.Fatalf("trafficCycleStart() = (%v, %v), want %v", got, err, test.want)
			}
		})
	}
}

func TestUpdateTrafficConfigRejectsInvalidValues(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Invalid Traffic Config")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	negative := int64(-1)
	for index, config := range []TrafficConfig{
		{MonthlyLimitBytes: &negative, CountMode: TrafficSingle, ResetDay: 1, ResetTime: "00:00"},
		{CountMode: "both", ResetDay: 1, ResetTime: "00:00"},
		{CountMode: TrafficSingle, ResetDay: 0, ResetTime: "00:00"},
		{CountMode: TrafficSingle, ResetDay: 1, ResetTime: "24:00"},
	} {
		if _, err := service.UpdateTrafficConfig(context.Background(), created.ID, config); !errors.Is(err, ErrInvalidTrafficConfig) {
			t.Fatalf("invalid traffic config %d error = %v", index, err)
		}
	}
}
