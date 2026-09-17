package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

type SystemInfo struct {
	Hostname     string
	OSName       string
	OSVersion    string
	Kernel       string
	Arch         string
	IPv4         []string
	IPv6         []string
	PublicIPv4   string
	AgentVersion string
	ReportedAt   time.Time
}

type SystemInfoReport struct {
	Hostname   string
	OSName     string
	OSVersion  string
	Kernel     string
	Arch       string
	IPv4       []string
	IPv6       []string
	PublicIPv4 string
}

func (s *Service) ReportSystemInfo(ctx context.Context, agentID, serverID int64, report SystemInfoReport) (bool, error) {
	report.Hostname = strings.TrimSpace(report.Hostname)
	report.OSName = strings.TrimSpace(report.OSName)
	report.OSVersion = strings.TrimSpace(report.OSVersion)
	report.Kernel = strings.TrimSpace(report.Kernel)
	report.Arch = strings.TrimSpace(report.Arch)
	report.PublicIPv4 = strings.TrimSpace(report.PublicIPv4)
	if utf8.RuneCountInString(report.Hostname) > 255 ||
		utf8.RuneCountInString(report.OSName) > 128 ||
		utf8.RuneCountInString(report.OSVersion) > 128 ||
		utf8.RuneCountInString(report.Kernel) > 128 ||
		utf8.RuneCountInString(report.Arch) > 32 {
		return false, ErrInvalidSystemInfo
	}

	var err error
	report.IPv4, err = normalizeIPAddresses(report.IPv4, true)
	if err != nil {
		return false, err
	}
	report.IPv6, err = normalizeIPAddresses(report.IPv6, false)
	if err != nil {
		return false, err
	}
	if report.PublicIPv4 != "" {
		ip := net.ParseIP(report.PublicIPv4)
		if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return false, ErrInvalidSystemInfo
		}
		report.PublicIPv4 = ip.To4().String()
	}
	ipv4JSON, _ := json.Marshal(report.IPv4)
	ipv6JSON, _ := json.Marshal(report.IPv6)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin system information report: %w", err)
	}
	defer tx.Rollback()

	var agentVersion string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM agents WHERE id = ? AND server_id = ?`, agentID, serverID,
	).Scan(&agentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return false, agentcontrol.ErrInvalidAgentToken
	}
	if err != nil {
		return false, fmt.Errorf("read reporting Agent: %w", err)
	}
	var previousPublicIPv4 string
	err = tx.QueryRowContext(ctx,
		`SELECT public_ipv4 FROM server_system_info WHERE server_id = ?`, serverID,
	).Scan(&previousPublicIPv4)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read previous public IPv4: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO server_system_info
		 (server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		 hostname = excluded.hostname,
		 os_name = excluded.os_name,
		 os_version = excluded.os_version,
		 kernel = excluded.kernel,
		 arch = excluded.arch,
		 ipv4 = excluded.ipv4,
		 ipv6 = excluded.ipv6,
		 public_ipv4 = excluded.public_ipv4,
		 agent_version = excluded.agent_version,
		 reported_at = excluded.reported_at`,
		serverID, report.Hostname, report.OSName, report.OSVersion, report.Kernel, report.Arch,
		string(ipv4JSON), string(ipv6JSON), report.PublicIPv4, agentVersion, now,
	); err != nil {
		return false, fmt.Errorf("save system information: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit system information report: %w", err)
	}
	return previousPublicIPv4 != report.PublicIPv4, nil
}

func normalizeIPAddresses(values []string, ipv4 bool) ([]string, error) {
	if len(values) > maxIPAddresses {
		return nil, ErrInvalidSystemInfo
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed := net.ParseIP(strings.TrimSpace(value))
		if parsed == nil || (parsed.To4() != nil) != ipv4 {
			return nil, ErrInvalidSystemInfo
		}
		if ipv4 {
			parsed = parsed.To4()
		}
		unique[parsed.String()] = struct{}{}
	}
	normalized := make([]string, 0, len(unique))
	for value := range unique {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}
