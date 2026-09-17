package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

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
		IPv6:       []string{"2001:db8::10"},
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
		strings.Join(value.SystemInfo.IPv6, ",") != "2001:db8::10" ||
		value.SystemInfo.PublicIPv4 != "198.51.100.20" {
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
