package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

type fakeGeoIPProvider struct {
	mu        sync.Mutex
	responses map[string]geoIPTestResult
	calls     []string
}

type geoIPTestResult struct {
	code string
	err  error
}

func (p *fakeGeoIPProvider) CountryCode(_ context.Context, ip string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, ip)
	result := p.responses[ip]
	return result.code, result.err
}

func (p *fakeGeoIPProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *fakeGeoIPProvider) calledIP(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[index]
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForCountry(t *testing.T, service *Service, serverID int64, want string) {
	t.Helper()
	waitFor(t, func() bool {
		value, err := service.Get(context.Background(), serverID)
		return err == nil && value.SystemInfo != nil && value.SystemInfo.CountryCode == want
	})
}

func createGeoIPTestServer(t *testing.T, provider GeoIPProvider) (*testService, int64, int64) {
	t.Helper()
	service, _ := newTestService(t, provider)
	created, err := service.Create(context.Background(), "GeoIP")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	return service, created.ID, agent.ID
}

func TestReportSystemInfoGeoIPLifecycle(t *testing.T) {
	provider := &fakeGeoIPProvider{responses: map[string]geoIPTestResult{
		"198.51.100.20": {code: "de"},
		"203.0.113.21":  {code: "JP"},
	}}
	service, serverID, agentID := createGeoIPTestServer(t, provider)
	report := SystemInfoReport{
		PublicIPv4: "198.51.100.20",
		PublicIPv6: "2606:4700:4700::1111",
		IPv6:       []string{"2606:4700:4700::1001"},
	}
	if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
		t.Fatalf("first ReportSystemInfo() error = %v", err)
	}
	waitForCountry(t, service.Service, serverID, "DE")
	if provider.callCount() != 1 || provider.calledIP(0) != report.PublicIPv4 {
		t.Fatalf("first GeoIP calls = %v, want IPv4 %s", provider.calls, report.PublicIPv4)
	}
	if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
		t.Fatalf("unchanged ReportSystemInfo() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if provider.callCount() != 1 {
		t.Fatalf("unchanged public IP caused %d GeoIP calls, want 1", provider.callCount())
	}
	restartedProvider := &fakeGeoIPProvider{responses: provider.responses}
	restarted := NewService(service.db, restartedProvider)
	if _, err := restarted.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
		t.Fatalf("restarted ReportSystemInfo() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if restartedProvider.callCount() != 0 {
		t.Fatalf("persisted country caused %d GeoIP calls after restart", restartedProvider.callCount())
	}

	report.PublicIPv4 = "203.0.113.21"
	if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
		t.Fatalf("changed ReportSystemInfo() error = %v", err)
	}
	waitForCountry(t, service.Service, serverID, "JP")
	if provider.callCount() != 2 || provider.calledIP(1) != report.PublicIPv4 {
		t.Fatalf("changed GeoIP calls = %v, want second IPv4 %s", provider.calls, report.PublicIPv4)
	}
}

func TestReportSystemInfoGeoIPPreservesTerritoryCodesAndUsesIPv6Fallback(t *testing.T) {
	for _, testCase := range []struct {
		name string
		ip   string
		code string
		want string
	}{
		{name: "Taiwan", ip: "2606:4700:4700::1101", code: "tw", want: "TW"},
		{name: "Hong Kong", ip: "2606:4700:4700::1102", code: "HK", want: "HK"},
		{name: "Macao", ip: "2606:4700:4700::1103", code: "MO", want: "MO"},
		{name: "China", ip: "2606:4700:4700::1104", code: "CN", want: "CN"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &fakeGeoIPProvider{responses: map[string]geoIPTestResult{testCase.ip: {code: testCase.code}}}
			service, serverID, agentID := createGeoIPTestServer(t, provider)
			if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, SystemInfoReport{
				IPv4: []string{"203.0.113.40"}, IPv6: []string{"2606:4700:4700::9999"}, PublicIPv6: testCase.ip,
			}); err != nil {
				t.Fatalf("ReportSystemInfo() error = %v", err)
			}
			waitForCountry(t, service.Service, serverID, testCase.want)
			if provider.callCount() != 1 || provider.calledIP(0) != testCase.ip {
				t.Fatalf("GeoIP calls = %v, want public IPv6 %s", provider.calls, testCase.ip)
			}
		})
	}
}

func TestReportSystemInfoClearsStaleCountryWhenChangedIPLookupFails(t *testing.T) {
	provider := &fakeGeoIPProvider{responses: map[string]geoIPTestResult{
		"198.51.100.80": {code: "DE"},
		"203.0.113.80":  {err: errors.New("unavailable")},
	}}
	service, serverID, agentID := createGeoIPTestServer(t, provider)
	if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, SystemInfoReport{PublicIPv4: "198.51.100.80"}); err != nil {
		t.Fatal(err)
	}
	waitForCountry(t, service.Service, serverID, "DE")
	if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, SystemInfoReport{PublicIPv4: "203.0.113.80"}); err != nil {
		t.Fatalf("changed ReportSystemInfo() error = %v", err)
	}
	waitFor(t, func() bool { return provider.callCount() == 2 })
	value, err := service.Get(context.Background(), serverID)
	if err != nil || value.SystemInfo == nil || value.SystemInfo.CountryCode != "" {
		t.Fatalf("country after changed-IP failure = (%+v, %v), want empty", value.SystemInfo, err)
	}
}

func TestReportSystemInfoGeoIPFailuresDoNotFailAndRetryAfterCooldown(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result geoIPTestResult
	}{
		{name: "provider error", result: geoIPTestResult{err: errors.New("unavailable")}},
		{name: "provider timeout", result: geoIPTestResult{err: context.DeadlineExceeded}},
		{name: "invalid code", result: geoIPTestResult{code: "DEU"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const ip = "198.51.100.50"
			provider := &fakeGeoIPProvider{responses: map[string]geoIPTestResult{ip: testCase.result}}
			service, serverID, agentID := createGeoIPTestServer(t, provider)
			now := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
			service.now = func() time.Time { return now }
			report := SystemInfoReport{PublicIPv4: ip}
			if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
				t.Fatalf("ReportSystemInfo() error = %v", err)
			}
			waitFor(t, func() bool { return provider.callCount() == 1 })
			value, err := service.Get(context.Background(), serverID)
			if err != nil || value.SystemInfo == nil || value.SystemInfo.CountryCode != "" {
				t.Fatalf("failed GeoIP result = (%+v, %v), want empty country", value.SystemInfo, err)
			}
			if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
				t.Fatalf("cooldown ReportSystemInfo() error = %v", err)
			}
			time.Sleep(20 * time.Millisecond)
			if provider.callCount() != 1 {
				t.Fatalf("GeoIP calls during cooldown = %d, want 1", provider.callCount())
			}
			restarted := NewService(service.db, provider)
			restarted.now = service.now
			if _, err := restarted.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
				t.Fatalf("restarted cooldown report: %v", err)
			}
			provider.mu.Lock()
			provider.responses[ip] = geoIPTestResult{code: "DE"}
			provider.mu.Unlock()
			now = now.Add(geoIPRetryCooldown + time.Second)
			if _, err := service.ReportSystemInfo(context.Background(), agentID, serverID, report); err != nil {
				t.Fatalf("retry ReportSystemInfo() error = %v", err)
			}
			waitFor(t, func() bool { return provider.callCount() == 2 })
			waitForCountry(t, service.Service, serverID, "DE")
		})
	}
}

type geoIPProviderFunc func(context.Context, string) (string, error)

func (f geoIPProviderFunc) CountryCode(ctx context.Context, ip string) (string, error) {
	return f(ctx, ip)
}

func TestGeoIPDoesNotBlockReportsOrHoldDatabaseTransaction(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := geoIPProviderFunc(func(ctx context.Context, _ string) (string, error) {
		close(started)
		select {
		case <-release:
			return "TW", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	service, serverID, agentID := createGeoIPTestServer(t, provider)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := service.ReportSystemInfo(ctx, agentID, serverID, SystemInfoReport{PublicIPv4: "198.51.100.90"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("GeoIP did not start")
	}
	// With SQLite's single connection, these writes cannot complete if GeoIP
	// is waiting on HTTP inside the system-info transaction.
	if err := service.ReportMetrics(ctx, agentID, serverID, MetricsReport{
		CPUPercent: 12, MemoryTotalBytes: 1024, DiskTotalBytes: 4096,
	}); err != nil {
		t.Fatalf("metrics while GeoIP is blocked: %v", err)
	}
	if _, err := service.ReportSystemInfo(ctx, agentID, serverID, SystemInfoReport{PublicIPv4: "198.51.100.90"}); err != nil {
		t.Fatalf("system info while GeoIP is blocked: %v", err)
	}
	close(release)
	waitForCountry(t, service.Service, serverID, "TW")
}

func TestGeoIPNeverUsesInterfaceAddresses(t *testing.T) {
	provider := &fakeGeoIPProvider{}
	service, serverID, agentID := createGeoIPTestServer(t, provider)
	if _, err := service.ReportSystemInfo(t.Context(), agentID, serverID, SystemInfoReport{
		IPv4: []string{"198.51.100.90", "172.17.0.1"}, IPv6: []string{"2606:4700:4700::1111"},
	}); err != nil {
		t.Fatal(err)
	}
	var selectedIP string
	if err := service.db.QueryRow(`SELECT country_ip FROM server_system_info WHERE server_id = ?`, serverID).Scan(&selectedIP); err != nil {
		t.Fatal(err)
	}
	if selectedIP != "" || provider.callCount() != 0 {
		t.Fatalf("GeoIP used interface addresses: selected=%q, calls=%d", selectedIP, provider.callCount())
	}
	value, err := service.Get(t.Context(), serverID)
	if err != nil || len(value.SystemInfo.IPv4) != 2 || len(value.SystemInfo.IPv6) != 1 {
		t.Fatalf("interface addresses were not preserved: %v", err)
	}
}

func TestGeoIPRejectsStaleAttemptAndReboundAgent(t *testing.T) {
	service, serverID, agentID := createGeoIPTestServer(t, nil)
	service.now = func() time.Time { return time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC) }
	var attempts []int64
	for _, ip := range []string{"198.51.100.10", "203.0.113.10", "198.51.100.10"} {
		if _, err := service.ReportSystemInfo(t.Context(), agentID, serverID, SystemInfoReport{PublicIPv4: ip}); err != nil {
			t.Fatal(err)
		}
		var attempt int64
		if err := service.db.QueryRow(`SELECT country_lookup_at FROM server_system_info WHERE server_id = ?`, serverID).Scan(&attempt); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, attempt)
	}
	service.geoIPProvider = geoIPProviderFunc(func(context.Context, string) (string, error) { return "DE", nil })
	service.lookupCountry(agentID, serverID, "198.51.100.10", attempts[0])
	value, err := service.Get(t.Context(), serverID)
	if err != nil || value.SystemInfo.CountryCode != "" {
		t.Fatalf("stale A result overwrote A -> B -> A: %v", err)
	}
	if _, err := service.CreateEnrollment(t.Context(), serverID); err != nil {
		t.Fatal(err)
	}
	service.lookupCountry(agentID, serverID, "198.51.100.10", attempts[2])
	value, err = service.Get(t.Context(), serverID)
	if err != nil || value.SystemInfo.CountryCode != "" {
		t.Fatalf("rebound Agent's lookup wrote country: %v", err)
	}
}

func TestReportSystemInfoUpsertsForCurrentAgent(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "System Information")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.7.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	reportedAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return reportedAt }
	report := SystemInfoReport{
		Hostname:   " jp-01 ",
		OSName:     "Debian GNU/Linux",
		OSVersion:  "12",
		Kernel:     "6.1.0-amd64",
		Arch:       "amd64",
		IPv4:       []string{"203.0.113.10", "10.0.0.2", "203.0.113.10"},
		IPv6:       []string{"2606:4700:4700::1111"},
		PublicIPv4: "198.51.100.20",
	}
	publicIPv4Changed, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, report)
	if err != nil {
		t.Fatalf("ReportSystemInfo() error = %v", err)
	}
	if !publicIPv4Changed {
		t.Fatal("first public IPv4 report was not marked changed")
	}
	publicIPv4Changed, err = service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, report)
	if err != nil || publicIPv4Changed {
		t.Fatalf("unchanged public IPv4 report = (%v, %v), want false", publicIPv4Changed, err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.SystemInfo == nil {
		t.Fatalf("Get() system information = (%+v, %v)", value.SystemInfo, err)
	}
	if value.SystemInfo.Hostname != "jp-01" || value.SystemInfo.OSName != report.OSName ||
		value.SystemInfo.OSVersion != report.OSVersion || value.SystemInfo.Kernel != report.Kernel ||
		value.SystemInfo.Arch != report.Arch || value.SystemInfo.AgentVersion != "v0.7.0" ||
		!value.SystemInfo.ReportedAt.Equal(reportedAt) ||
		strings.Join(value.SystemInfo.IPv4, ",") != "10.0.0.2,203.0.113.10" ||
		strings.Join(value.SystemInfo.IPv6, ",") != "2606:4700:4700::1111" ||
		value.SystemInfo.PublicIPv4 != "198.51.100.20" || value.SystemInfo.PublicIPv6 != "2606:4700:4700::1111" {
		t.Fatalf("stored system information = %+v", value.SystemInfo)
	}

	service.now = func() time.Time { return reportedAt.Add(time.Minute) }
	publicIPv4Changed, err = service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		Hostname: "jp-02", Arch: "arm64", IPv4: []string{}, IPv6: []string{},
	})
	if err != nil {
		t.Fatalf("second ReportSystemInfo() error = %v", err)
	}
	if !publicIPv4Changed {
		t.Fatal("cleared public IPv4 was not marked changed")
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count system information rows: %v", err)
	}
	updated, err := service.Get(context.Background(), created.ID)
	if err != nil || rowCount != 1 || updated.SystemInfo == nil || updated.SystemInfo.Hostname != "jp-02" ||
		updated.SystemInfo.Arch != "arm64" {
		t.Fatalf("updated system information = (%+v, rows %d, %v)", updated.SystemInfo, rowCount, err)
	}

	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		IPv4: []string{"not-an-ip"},
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("invalid IP error = %v, want ErrInvalidSystemInfo", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		PublicIPv4: "172.26.1.10",
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("private public IPv4 error = %v, want ErrInvalidSystemInfo", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		PublicIPv6: "fd00::1",
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("private public IPv6 error = %v, want ErrInvalidSystemInfo", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		Hostname: strings.Repeat("a", 256),
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("oversized hostname error = %v, want ErrInvalidSystemInfo", err)
	}
}

func TestSystemInfoSurvivesAgentReplacementAndArchiveThenCascadesOnDelete(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Lifecycle Information")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
		Hostname: "old-host", Arch: "amd64", IPv4: []string{}, IPv6: []string{},
	}); err != nil {
		t.Fatalf("initial ReportSystemInfo() error = %v", err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	preserved, err := service.Get(context.Background(), created.ID)
	if err != nil || preserved.SystemInfo == nil || preserved.SystemInfo.Hostname != "old-host" {
		t.Fatalf("system information after rebind = (%+v, %v)", preserved.SystemInfo, err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
		Hostname: "stale-host", IPv4: []string{}, IPv6: []string{},
	}); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("old Agent report error = %v, want ErrInvalidAgentToken", err)
	}
	secondAgent, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.7.0", true)
	if err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	stillPreserved, err := service.Get(context.Background(), created.ID)
	if err != nil || stillPreserved.SystemInfo == nil || stillPreserved.SystemInfo.Hostname != "old-host" {
		t.Fatalf("system information after Agent registration = (%+v, %v)", stillPreserved.SystemInfo, err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), secondAgent.ID, secondAgent.ServerID, SystemInfoReport{
		Hostname: "new-host", Arch: "arm64", IPv4: []string{}, IPv6: []string{},
	}); err != nil {
		t.Fatalf("replacement ReportSystemInfo() error = %v", err)
	}

	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].SystemInfo == nil || archived[0].SystemInfo.Hostname != "new-host" {
		t.Fatalf("archived system information = (%+v, %v)", archived, err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	var infoCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&infoCount); err != nil {
		t.Fatalf("count deleted system information: %v", err)
	}
	if infoCount != 0 {
		t.Fatalf("system information rows after permanent delete = %d, want 0", infoCount)
	}
}
