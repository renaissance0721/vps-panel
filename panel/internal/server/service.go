package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

const (
	StatusPending           = "pending"
	StatusOnline            = "online"
	StatusOffline           = "offline"
	PurposeInitial          = "initial"
	PurposeRebind           = "rebind"
	TrafficSingle           = "single"
	TrafficBidirectional    = "bidirectional"
	EnrollmentLifetime      = 24 * time.Hour
	maxNameLength           = 100
	maxIPAddresses          = 16
	defaultTrafficResetDay  = 1
	defaultTrafficResetTime = "00:00"
)

var (
	ErrInvalidName          = errors.New("server name must be 1-100 characters")
	ErrNotFound             = errors.New("server not found")
	ErrInvalidEnrollment    = errors.New("invalid, used, or expired enrollment token")
	ErrInvalidAgentVersion  = errors.New("agent version must be 1-64 characters")
	ErrInvalidAgentToken    = errors.New("invalid agent token")
	ErrArchived             = errors.New("server is archived")
	ErrInitialConfigExists  = errors.New("initial enrollment cannot replace an existing Agent config")
	ErrInvalidSystemInfo    = errors.New("invalid system information")
	ErrInvalidMetrics       = errors.New("invalid server metrics")
	ErrInvalidTrafficConfig = errors.New("invalid server traffic configuration")
)

type Server struct {
	ID                       int64
	Name                     string
	Status                   string
	ArchivedAt               *time.Time
	ExpiresAt                *time.Time
	MonthlyTrafficLimitBytes *int64
	TrafficCountMode         string
	TrafficResetDay          int
	TrafficResetTime         string
	LastSeenAt               *time.Time
	SystemInfo               *SystemInfo
	Metrics                  *Metrics
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

func (server Server) TrafficUsedBytes() int64 {
	if server.Metrics == nil {
		return 0
	}
	if server.TrafficCountMode == TrafficBidirectional {
		if server.Metrics.CycleRXBytes > math.MaxInt64-server.Metrics.CycleTXBytes {
			return math.MaxInt64
		}
		return server.Metrics.CycleRXBytes + server.Metrics.CycleTXBytes
	}
	return server.Metrics.CycleTXBytes
}

type SystemInfo struct {
	Hostname     string
	OSName       string
	OSVersion    string
	Kernel       string
	Arch         string
	IPv4         []string
	IPv6         []string
	AgentVersion string
	ReportedAt   time.Time
}

type SystemInfoReport struct {
	Hostname  string
	OSName    string
	OSVersion string
	Kernel    string
	Arch      string
	IPv4      []string
	IPv6      []string
}

type Metrics struct {
	CPUPercent       float64
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	DiskUsedBytes    int64
	DiskTotalBytes   int64
	UptimeSeconds    int64
	NICRXBytes       int64
	NICTXBytes       int64
	CycleRXBytes     int64
	CycleTXBytes     int64
	CycleStartedAt   *time.Time
	UpdatedAt        time.Time
}

type MetricsReport struct {
	CPUPercent       float64
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	DiskUsedBytes    int64
	DiskTotalBytes   int64
	UptimeSeconds    int64
	HasNetworkUsage  bool
	NICRXBytes       int64
	NICTXBytes       int64
}

type TrafficConfig struct {
	MonthlyLimitBytes *int64
	CountMode         string
	ResetDay          int
	ResetTime         string
}

type CreatedServer struct {
	Server
	EnrollmentToken     string
	EnrollmentExpiresAt time.Time
}

type RegisteredAgent struct {
	ID       int64
	ServerID int64
	Token    string
}

type Agent struct {
	ID       int64
	ServerID int64
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) Create(ctx context.Context, name string) (CreatedServer, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return CreatedServer{}, ErrInvalidName
	}

	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedServer{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(EnrollmentLifetime)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin server creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO servers (name, status, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		name, StatusPending, now.Unix(), now.Unix(),
	)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("create server: %w", err)
	}
	serverID, err := result.LastInsertId()
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		serverID, tokenHash, PurposeInitial, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return CreatedServer{}, fmt.Errorf("create agent enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit server creation: %w", err)
	}

	return CreatedServer{
		Server: Server{
			ID:               serverID,
			Name:             name,
			Status:           StatusPending,
			TrafficCountMode: TrafficSingle,
			TrafficResetDay:  defaultTrafficResetDay,
			TrafficResetTime: defaultTrafficResetTime,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		EnrollmentToken:     tokenValue,
		EnrollmentExpiresAt: expiresAt,
	}, nil
}

func (s *Service) CreateEnrollment(ctx context.Context, id int64) (CreatedServer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin Agent enrollment creation: %w", err)
	}
	defer tx.Rollback()

	value, err := scanServer(tx.QueryRowContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.archived_at, servers.expires_at,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 system_info.hostname, system_info.os_name, system_info.os_version,
		 system_info.kernel, system_info.arch, system_info.ipv4, system_info.ipv6,
		 system_info.agent_version, system_info.reported_at,
		 metrics.cpu_percent, metrics.memory_used_bytes, metrics.memory_total_bytes,
		 metrics.disk_used_bytes, metrics.disk_total_bytes, metrics.uptime_seconds,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes,
		 metrics.cycle_started_at, metrics.updated_at,
		 servers.created_at, servers.updated_at
		 FROM servers
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 LEFT JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE servers.id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return CreatedServer{}, ErrNotFound
	}
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server for Agent enrollment: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return CreatedServer{}, fmt.Errorf("remove previous Agent enrollment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return CreatedServer{}, fmt.Errorf("revoke previous Agent: %w", err)
	}

	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedServer{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(EnrollmentLifetime)
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ?`,
		StatusPending, now.Unix(), id,
	); err != nil {
		return CreatedServer{}, fmt.Errorf("prepare server Agent enrollment: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, tokenHash, PurposeRebind, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return CreatedServer{}, fmt.Errorf("create Agent enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit Agent enrollment creation: %w", err)
	}
	value.Status = StatusPending
	value.LastSeenAt = nil
	value.UpdatedAt = now
	return CreatedServer{
		Server:              value,
		EnrollmentToken:     tokenValue,
		EnrollmentExpiresAt: expiresAt,
	}, nil
}

func (s *Service) List(ctx context.Context) ([]Server, error) {
	return s.list(ctx, false)
}

func (s *Service) ListArchived(ctx context.Context) ([]Server, error) {
	return s.list(ctx, true)
}

func (s *Service) list(ctx context.Context, archived bool) ([]Server, error) {
	archiveCondition := "servers.archived_at IS NULL"
	if archived {
		archiveCondition = "servers.archived_at IS NOT NULL"
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.archived_at, servers.expires_at,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 system_info.hostname, system_info.os_name, system_info.os_version,
		 system_info.kernel, system_info.arch, system_info.ipv4, system_info.ipv6,
		 system_info.agent_version, system_info.reported_at,
		 metrics.cpu_percent, metrics.memory_used_bytes, metrics.memory_total_bytes,
		 metrics.disk_used_bytes, metrics.disk_total_bytes, metrics.uptime_seconds,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes,
		 metrics.cycle_started_at, metrics.updated_at,
		 servers.created_at, servers.updated_at
		 FROM servers
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 LEFT JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE `+archiveCondition+` ORDER BY servers.created_at DESC, servers.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	servers := make([]Server, 0)
	for rows.Next() {
		value, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		servers = append(servers, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate servers: %w", err)
	}
	return servers, nil
}

func (s *Service) Get(ctx context.Context, id int64) (Server, error) {
	value, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.archived_at, servers.expires_at,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 system_info.hostname, system_info.os_name, system_info.os_version,
		 system_info.kernel, system_info.arch, system_info.ipv4, system_info.ipv6,
		 system_info.agent_version, system_info.reported_at,
		 metrics.cpu_percent, metrics.memory_used_bytes, metrics.memory_total_bytes,
		 metrics.disk_used_bytes, metrics.disk_total_bytes, metrics.uptime_seconds,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes,
		 metrics.cycle_started_at, metrics.updated_at,
		 servers.created_at, servers.updated_at
		 FROM servers
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 LEFT JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE servers.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("get server: %w", err)
	}
	return value, nil
}

func (s *Service) UpdateExpiration(ctx context.Context, id int64, expiresAt *time.Time) (Server, error) {
	var expiresAtValue any
	if expiresAt != nil {
		expiresAtValue = expiresAt.UTC().Truncate(time.Second).Unix()
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET expires_at = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		expiresAtValue, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server expiration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server expiration count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) UpdateTrafficConfig(ctx context.Context, id int64, config TrafficConfig) (Server, error) {
	if config.MonthlyLimitBytes != nil && *config.MonthlyLimitBytes < 0 ||
		(config.CountMode != TrafficSingle && config.CountMode != TrafficBidirectional) ||
		config.ResetDay < 1 || config.ResetDay > 31 ||
		!validTrafficResetTime(config.ResetTime) {
		return Server{}, ErrInvalidTrafficConfig
	}

	var monthlyLimit any
	if config.MonthlyLimitBytes != nil && *config.MonthlyLimitBytes > 0 {
		monthlyLimit = *config.MonthlyLimitBytes
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers
		 SET monthly_traffic_limit_bytes = ?, traffic_count_mode = ?,
		     traffic_reset_day = ?, traffic_reset_time = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		monthlyLimit, config.CountMode, config.ResetDay, config.ResetTime,
		s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server traffic configuration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server traffic configuration count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) Archive(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server archive: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		StatusOffline, now, now, id,
	)
	if err != nil {
		return fmt.Errorf("archive server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read archived server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return fmt.Errorf("revoke archived server agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return fmt.Errorf("remove unused agent enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server archive: %w", err)
	}
	return nil
}

func (s *Service) PermanentlyDelete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM servers WHERE id = ? AND archived_at IS NOT NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("permanently delete server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read permanently deleted server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) RegisterAgent(
	ctx context.Context,
	enrollmentToken string,
	agentVersion string,
	existingConfig bool,
) (RegisteredAgent, error) {
	if enrollmentToken == "" {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	agentVersion = strings.TrimSpace(agentVersion)
	if agentVersion == "" || utf8.RuneCountInString(agentVersion) > 64 {
		return RegisteredAgent{}, ErrInvalidAgentVersion
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("begin agent registration: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second)
	var enrollmentID, serverID int64
	var purpose string
	err = tx.QueryRowContext(ctx,
		`SELECT id, server_id, purpose FROM agent_enrollments
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(enrollmentToken), now.Unix(),
	).Scan(&enrollmentID, &serverID, &purpose)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("find agent enrollment: %w", err)
	}
	if purpose == PurposeInitial && existingConfig {
		return RegisteredAgent{}, ErrInitialConfigExists
	}

	agentToken, agentTokenHash, err := token.New()
	if err != nil {
		return RegisteredAgent{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, serverID); err != nil {
		return RegisteredAgent{}, fmt.Errorf("replace previous Agent: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO agents
		 (server_id, token_hash, version, registered_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, agentTokenHash, agentVersion, now.Unix(), now.Unix(), now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("create agent: %w", err)
	}
	agentID, err := result.LastInsertId()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read agent id: %w", err)
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET used_at = ?
		 WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), enrollmentID, now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("use agent enrollment: %w", err)
	}
	usedCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read used enrollment count: %w", err)
	}
	if usedCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = NULL, updated_at = ? WHERE id = ?`,
		StatusOffline, now.Unix(), serverID,
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("update registered server: %w", err)
	}
	serverCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read updated server count: %w", err)
	}
	if serverCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	if err := tx.Commit(); err != nil {
		return RegisteredAgent{}, fmt.Errorf("commit agent registration: %w", err)
	}
	return RegisteredAgent{
		ID:       agentID,
		ServerID: serverID,
		Token:    agentToken,
	}, nil
}

func (s *Service) AuthenticateAgent(ctx context.Context, agentToken string) (Agent, error) {
	if agentToken == "" {
		return Agent{}, ErrInvalidAgentToken
	}

	var agent Agent
	err := s.db.QueryRowContext(ctx,
		`SELECT agents.id, agents.server_id FROM agents
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.token_hash = ? AND servers.archived_at IS NULL`, token.Hash(agentToken),
	).Scan(&agent.ID, &agent.ServerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrInvalidAgentToken
	}
	if err != nil {
		return Agent{}, fmt.Errorf("authenticate agent: %w", err)
	}
	return agent, nil
}

func (s *Service) SetAgentOnline(ctx context.Context, serverID int64) error {
	return s.setStatus(ctx, serverID, StatusOnline)
}

func (s *Service) SetAgentConnected(ctx context.Context, agentID, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent connection update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, updated_at = ? WHERE id = ? AND server_id = ?`,
		now, now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected Agent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		StatusOnline, now, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected server: %w", err)
	}
	count, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected server count: %w", err)
	}
	if count != 1 {
		return ErrArchived
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent connection update: %w", err)
	}
	return nil
}

func (s *Service) TouchAgent(ctx context.Context, agentID, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, updated_at = ? WHERE id = ? AND server_id = ?`,
		now, now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update Agent last seen: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}
	return nil
}

func (s *Service) ReportSystemInfo(ctx context.Context, agentID, serverID int64, report SystemInfoReport) error {
	report.Hostname = strings.TrimSpace(report.Hostname)
	report.OSName = strings.TrimSpace(report.OSName)
	report.OSVersion = strings.TrimSpace(report.OSVersion)
	report.Kernel = strings.TrimSpace(report.Kernel)
	report.Arch = strings.TrimSpace(report.Arch)
	if utf8.RuneCountInString(report.Hostname) > 255 ||
		utf8.RuneCountInString(report.OSName) > 128 ||
		utf8.RuneCountInString(report.OSVersion) > 128 ||
		utf8.RuneCountInString(report.Kernel) > 128 ||
		utf8.RuneCountInString(report.Arch) > 32 {
		return ErrInvalidSystemInfo
	}

	var err error
	report.IPv4, err = normalizeIPAddresses(report.IPv4, true)
	if err != nil {
		return err
	}
	report.IPv6, err = normalizeIPAddresses(report.IPv6, false)
	if err != nil {
		return err
	}
	ipv4JSON, _ := json.Marshal(report.IPv4)
	ipv6JSON, _ := json.Marshal(report.IPv6)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system information report: %w", err)
	}
	defer tx.Rollback()

	var agentVersion string
	err = tx.QueryRowContext(ctx,
		`SELECT version FROM agents WHERE id = ? AND server_id = ?`, agentID, serverID,
	).Scan(&agentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidAgentToken
	}
	if err != nil {
		return fmt.Errorf("read reporting Agent: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO server_system_info
		 (server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, agent_version, reported_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		 hostname = excluded.hostname,
		 os_name = excluded.os_name,
		 os_version = excluded.os_version,
		 kernel = excluded.kernel,
		 arch = excluded.arch,
		 ipv4 = excluded.ipv4,
		 ipv6 = excluded.ipv6,
		 agent_version = excluded.agent_version,
		 reported_at = excluded.reported_at`,
		serverID, report.Hostname, report.OSName, report.OSVersion, report.Kernel, report.Arch,
		string(ipv4JSON), string(ipv6JSON), agentVersion, now,
	); err != nil {
		return fmt.Errorf("save system information: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit system information report: %w", err)
	}
	return nil
}

func (s *Service) ReportMetrics(ctx context.Context, agentID, serverID int64, report MetricsReport) error {
	if math.IsNaN(report.CPUPercent) || math.IsInf(report.CPUPercent, 0) ||
		report.CPUPercent < 0 || report.CPUPercent > 100 ||
		report.MemoryUsedBytes < 0 || report.MemoryTotalBytes < 0 ||
		report.MemoryUsedBytes > report.MemoryTotalBytes ||
		report.DiskUsedBytes < 0 || report.DiskTotalBytes < 0 ||
		report.DiskUsedBytes > report.DiskTotalBytes ||
		report.UptimeSeconds < 0 || report.NICRXBytes < 0 || report.NICTXBytes < 0 {
		return ErrInvalidMetrics
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metrics report: %w", err)
	}
	defer tx.Rollback()

	var currentAgentID int64
	var resetDay int
	var resetTime string
	err = tx.QueryRowContext(ctx,
		`SELECT agents.id, servers.traffic_reset_day, servers.traffic_reset_time
		 FROM agents JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ? AND agents.server_id = ?`, agentID, serverID,
	).Scan(&currentAgentID, &resetDay, &resetTime)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidAgentToken
	}
	if err != nil {
		return fmt.Errorf("read reporting Agent for metrics: %w", err)
	}
	nowTime := s.now().UTC().Truncate(time.Second)
	var nicRX, nicTX, cycleRX, cycleTX int64
	var storedCycleStart sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT nic_rx_bytes, nic_tx_bytes, cycle_rx_bytes, cycle_tx_bytes, cycle_started_at
		 FROM server_metrics WHERE server_id = ?`, serverID,
	).Scan(&nicRX, &nicTX, &cycleRX, &cycleTX, &storedCycleStart)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read traffic baseline: %w", err)
	}
	var cycleStartValue any
	if storedCycleStart.Valid {
		cycleStartValue = storedCycleStart.Int64
	}
	if report.HasNetworkUsage {
		cycleStart, err := trafficCycleStart(nowTime, resetDay, resetTime)
		if err != nil {
			return fmt.Errorf("calculate traffic cycle: %w", err)
		}
		switch {
		case !storedCycleStart.Valid:
			cycleRX = 0
			cycleTX = 0
		case time.Unix(storedCycleStart.Int64, 0).UTC().Before(cycleStart):
			cycleRX = 0
			cycleTX = 0
		default:
			deltaRX := trafficDelta(report.NICRXBytes, nicRX)
			deltaTX := trafficDelta(report.NICTXBytes, nicTX)
			if cycleRX > math.MaxInt64-deltaRX || cycleTX > math.MaxInt64-deltaTX {
				return ErrInvalidMetrics
			}
			cycleRX += deltaRX
			cycleTX += deltaTX
			cycleStart = time.Unix(storedCycleStart.Int64, 0).UTC()
		}
		nicRX = report.NICRXBytes
		nicTX = report.NICTXBytes
		cycleStartValue = cycleStart.Unix()
	}

	now := nowTime.Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, nic_rx_bytes, nic_tx_bytes,
		  cycle_rx_bytes, cycle_tx_bytes, cycle_started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		 cpu_percent = excluded.cpu_percent,
		 memory_used_bytes = excluded.memory_used_bytes,
		 memory_total_bytes = excluded.memory_total_bytes,
		 disk_used_bytes = excluded.disk_used_bytes,
		 disk_total_bytes = excluded.disk_total_bytes,
		 uptime_seconds = excluded.uptime_seconds,
		 nic_rx_bytes = excluded.nic_rx_bytes,
		 nic_tx_bytes = excluded.nic_tx_bytes,
		 cycle_rx_bytes = excluded.cycle_rx_bytes,
		 cycle_tx_bytes = excluded.cycle_tx_bytes,
		 cycle_started_at = excluded.cycle_started_at,
		 updated_at = excluded.updated_at`,
		serverID, report.CPUPercent, report.MemoryUsedBytes, report.MemoryTotalBytes,
		report.DiskUsedBytes, report.DiskTotalBytes, report.UptimeSeconds,
		nicRX, nicTX, cycleRX, cycleTX, cycleStartValue, now,
	); err != nil {
		return fmt.Errorf("save server metrics: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metrics report: %w", err)
	}
	return nil
}

func trafficDelta(current, previous int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func trafficCycleStart(now time.Time, resetDay int, resetTime string) (time.Time, error) {
	parsedTime, err := time.Parse("15:04", resetTime)
	if err != nil || parsedTime.Format("15:04") != resetTime || resetDay < 1 || resetDay > 31 {
		return time.Time{}, ErrInvalidTrafficConfig
	}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	localNow := now.In(location)
	boundary := monthlyTrafficBoundary(
		localNow.Year(), localNow.Month(), resetDay, parsedTime.Hour(), parsedTime.Minute(), location,
	)
	if localNow.Before(boundary) {
		previousMonth := time.Date(localNow.Year(), localNow.Month()-1, 1, 0, 0, 0, 0, location)
		boundary = monthlyTrafficBoundary(
			previousMonth.Year(), previousMonth.Month(), resetDay,
			parsedTime.Hour(), parsedTime.Minute(), location,
		)
	}
	return boundary.UTC(), nil
}

func monthlyTrafficBoundary(year int, month time.Month, resetDay, hour, minute int, location *time.Location) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day()
	if resetDay > lastDay {
		resetDay = lastDay
	}
	return time.Date(year, month, resetDay, hour, minute, 0, 0, location)
}

func validTrafficResetTime(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
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

func (s *Service) SetAgentOffline(ctx context.Context, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND status = ?`,
		StatusOffline, now, serverID, StatusOnline,
	)
	if err != nil {
		return fmt.Errorf("set server offline: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read offline server count: %w", err)
	}
	if count == 1 {
		return nil
	}
	var existingID int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE id = ?`, serverID,
	).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read disconnected server state: %w", err)
	}
	return nil
}

func (s *Service) ResetOnline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE status = ? AND archived_at IS NULL`,
		StatusOffline, s.now().UTC().Truncate(time.Second).Unix(), StatusOnline,
	)
	if err != nil {
		return fmt.Errorf("reset online servers: %w", err)
	}
	return nil
}

func (s *Service) setStatus(ctx context.Context, serverID int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		status, s.now().UTC().Truncate(time.Second).Unix(), serverID,
	)
	if err != nil {
		return fmt.Errorf("update server status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated server count: %w", err)
	}
	if count != 1 {
		var archivedAt sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			`SELECT archived_at FROM servers WHERE id = ?`, serverID,
		).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read server lifecycle state: %w", err)
		}
		if archivedAt.Valid {
			return ErrArchived
		}
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (Server, error) {
	var value Server
	var archivedAt, expiresAt, monthlyTrafficLimit, lastSeenAt sql.NullInt64
	var hostname, osName, osVersion, kernel, arch sql.NullString
	var ipv4JSON, ipv6JSON, agentVersion sql.NullString
	var reportedAt sql.NullInt64
	var cpuPercent sql.NullFloat64
	var memoryUsed, memoryTotal, diskUsed, diskTotal, uptime sql.NullInt64
	var nicRX, nicTX, cycleRX, cycleTX, cycleStartedAt, metricsUpdatedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&value.ID, &value.Name, &value.Status, &archivedAt, &expiresAt,
		&monthlyTrafficLimit, &value.TrafficCountMode, &value.TrafficResetDay, &value.TrafficResetTime,
		&lastSeenAt,
		&hostname, &osName, &osVersion, &kernel, &arch, &ipv4JSON, &ipv6JSON, &agentVersion, &reportedAt,
		&cpuPercent, &memoryUsed, &memoryTotal, &diskUsed, &diskTotal, &uptime,
		&nicRX, &nicTX, &cycleRX, &cycleTX, &cycleStartedAt, &metricsUpdatedAt,
		&createdAt, &updatedAt,
	); err != nil {
		return Server{}, err
	}
	if archivedAt.Valid {
		archivedTime := time.Unix(archivedAt.Int64, 0).UTC()
		value.ArchivedAt = &archivedTime
	}
	if expiresAt.Valid {
		expiresTime := time.Unix(expiresAt.Int64, 0).UTC()
		value.ExpiresAt = &expiresTime
	}
	if monthlyTrafficLimit.Valid && monthlyTrafficLimit.Int64 > 0 {
		limit := monthlyTrafficLimit.Int64
		value.MonthlyTrafficLimitBytes = &limit
	}
	if lastSeenAt.Valid {
		lastSeenTime := time.Unix(lastSeenAt.Int64, 0).UTC()
		value.LastSeenAt = &lastSeenTime
	}
	if reportedAt.Valid {
		info := SystemInfo{
			Hostname:     hostname.String,
			OSName:       osName.String,
			OSVersion:    osVersion.String,
			Kernel:       kernel.String,
			Arch:         arch.String,
			AgentVersion: agentVersion.String,
			ReportedAt:   time.Unix(reportedAt.Int64, 0).UTC(),
		}
		if err := json.Unmarshal([]byte(ipv4JSON.String), &info.IPv4); err != nil {
			return Server{}, fmt.Errorf("decode server IPv4 addresses: %w", err)
		}
		if err := json.Unmarshal([]byte(ipv6JSON.String), &info.IPv6); err != nil {
			return Server{}, fmt.Errorf("decode server IPv6 addresses: %w", err)
		}
		value.SystemInfo = &info
	}
	if metricsUpdatedAt.Valid {
		metrics := &Metrics{
			CPUPercent:       cpuPercent.Float64,
			MemoryUsedBytes:  memoryUsed.Int64,
			MemoryTotalBytes: memoryTotal.Int64,
			DiskUsedBytes:    diskUsed.Int64,
			DiskTotalBytes:   diskTotal.Int64,
			UptimeSeconds:    uptime.Int64,
			NICRXBytes:       nicRX.Int64,
			NICTXBytes:       nicTX.Int64,
			CycleRXBytes:     cycleRX.Int64,
			CycleTXBytes:     cycleTX.Int64,
			UpdatedAt:        time.Unix(metricsUpdatedAt.Int64, 0).UTC(),
		}
		if cycleStartedAt.Valid {
			startedAt := time.Unix(cycleStartedAt.Int64, 0).UTC()
			metrics.CycleStartedAt = &startedAt
		}
		value.Metrics = metrics
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
