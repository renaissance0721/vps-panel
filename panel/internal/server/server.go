package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func (s *Service) Create(ctx context.Context, name string) (CreatedServer, error) {
	return s.CreateForUser(ctx, name, VisibilityPublic, nil, 0)
}

func (s *Service) CreateForUser(
	ctx context.Context,
	name, visibility string,
	userIDs []int64,
	creatorID int64,
) (CreatedServer, error) {
	var err error
	name, err = normalizeServerName(name)
	if err != nil {
		return CreatedServer{}, err
	}
	visibility, err = normalizeVisibility(visibility)
	if err != nil {
		return CreatedServer{}, err
	}
	if visibility == VisibilityPrivate {
		userIDs, err = normalizeAccessUserIDs(userIDs, creatorID)
		if err != nil {
			return CreatedServer{}, err
		}
	}

	enrollment, err := agentcontrol.NewEnrollment(s.now)
	if err != nil {
		return CreatedServer{}, err
	}
	now := enrollment.CreatedAt

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin server creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO servers (name, status, visibility, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		name, StatusPending, visibility, now.Unix(), now.Unix(),
	)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("create server: %w", err)
	}
	serverID, err := result.LastInsertId()
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server id: %w", err)
	}
	if visibility == VisibilityPrivate {
		if err := validateAccessUsers(ctx, tx, userIDs); err != nil {
			return CreatedServer{}, err
		}
		if err := replaceServerAccess(ctx, tx, serverID, userIDs); err != nil {
			return CreatedServer{}, err
		}
	}
	if err := enrollment.Insert(ctx, tx, serverID, agentcontrol.PurposeInitial); err != nil {
		return CreatedServer{}, fmt.Errorf("create agent enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit server creation: %w", err)
	}

	return CreatedServer{
		Server: Server{
			ID:                 serverID,
			Name:               name,
			Status:             StatusPending,
			Visibility:         visibility,
			OutboundPreference: OutboundAuto,
			AccessUserIDs:      userIDs,
			TrafficCountMode:   TrafficSingle,
			TrafficResetDay:    defaultTrafficResetDay,
			TrafficResetTime:   defaultTrafficResetTime,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		EnrollmentToken:     enrollment.Token,
		EnrollmentExpiresAt: enrollment.ExpiresAt,
	}, nil
}

func normalizeServerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return "", ErrInvalidName
	}
	return name, nil
}

func (s *Service) List(ctx context.Context) ([]Server, error) {
	return s.list(ctx, false, 0)
}

func (s *Service) ListArchived(ctx context.Context) ([]Server, error) {
	return s.list(ctx, true, 0)
}

func (s *Service) ListForUser(ctx context.Context, userID int64) ([]Server, error) {
	return s.list(ctx, false, userID)
}

func (s *Service) ListArchivedForUser(ctx context.Context, userID int64) ([]Server, error) {
	return s.list(ctx, true, userID)
}

func (s *Service) list(ctx context.Context, archived bool, userID int64) ([]Server, error) {
	archiveCondition := "servers.archived_at IS NULL"
	if archived {
		archiveCondition = "servers.archived_at IS NOT NULL"
	}
	accessCondition := ""
	arguments := []any{}
	if userID > 0 {
		accessCondition = ` AND (servers.visibility = 'public' OR EXISTS (
			SELECT 1 FROM server_access WHERE server_access.server_id = servers.id AND server_access.user_id = ?
		))`
		arguments = append(arguments, userID)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.visibility, servers.outbound_preference, servers.block_china_inbound,
		 COALESCE((SELECT group_concat(user_id) FROM server_access WHERE server_id = servers.id), ''),
		 servers.archived_at, servers.expires_at,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 agent.last_seen_at, agent.implementation, agent.version, agent.api_version, agent.capabilities_json,
		 agent.upgrade_target_version, agent.upgrade_status, agent.upgrade_error,
		 system_info.hostname, system_info.os_name, system_info.os_version,
		 system_info.kernel, system_info.arch, system_info.ipv4, system_info.ipv6, system_info.public_ipv4,
		 system_info.agent_version, system_info.reported_at,
		 metrics.cpu_percent, metrics.memory_used_bytes, metrics.memory_total_bytes,
		 metrics.disk_used_bytes, metrics.disk_total_bytes, metrics.uptime_seconds,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes,
		 metrics.traffic_adjustment_bytes, metrics.cycle_started_at, metrics.updated_at,
		 servers.created_at, servers.updated_at
		 FROM servers
		 LEFT JOIN agents AS agent ON agent.server_id = servers.id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 LEFT JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE `+archiveCondition+accessCondition+` ORDER BY servers.created_at DESC, servers.id DESC`, arguments...,
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
		`SELECT servers.id, servers.name, servers.status, servers.visibility, servers.outbound_preference, servers.block_china_inbound,
		 COALESCE((SELECT group_concat(user_id) FROM server_access WHERE server_id = servers.id), ''),
		 servers.archived_at, servers.expires_at,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 agent.last_seen_at, agent.implementation, agent.version, agent.api_version, agent.capabilities_json,
		 agent.upgrade_target_version, agent.upgrade_status, agent.upgrade_error,
		 system_info.hostname, system_info.os_name, system_info.os_version,
		 system_info.kernel, system_info.arch, system_info.ipv4, system_info.ipv6, system_info.public_ipv4,
		 system_info.agent_version, system_info.reported_at,
		 metrics.cpu_percent, metrics.memory_used_bytes, metrics.memory_total_bytes,
		 metrics.disk_used_bytes, metrics.disk_total_bytes, metrics.uptime_seconds,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes,
		 metrics.traffic_adjustment_bytes, metrics.cycle_started_at, metrics.updated_at,
		 servers.created_at, servers.updated_at
		 FROM servers
		 LEFT JOIN agents AS agent ON agent.server_id = servers.id
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (Server, error) {
	var value Server
	var accessUserIDs string
	var archivedAt, expiresAt, monthlyTrafficLimit, lastSeenAt sql.NullInt64
	var implementation, storedAgentVersion, capabilitiesJSON sql.NullString
	var apiVersion sql.NullInt64
	var upgradeTarget, upgradeStatus, upgradeError sql.NullString
	var hostname, osName, osVersion, kernel, arch sql.NullString
	var ipv4JSON, ipv6JSON, publicIPv4, agentVersion sql.NullString
	var reportedAt sql.NullInt64
	var cpuPercent sql.NullFloat64
	var memoryUsed, memoryTotal, diskUsed, diskTotal, uptime sql.NullInt64
	var nicRX, nicTX, cycleRX, cycleTX, trafficAdjustment, cycleStartedAt, metricsUpdatedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&value.ID, &value.Name, &value.Status, &value.Visibility, &value.OutboundPreference, &value.BlockChinaInbound, &accessUserIDs, &archivedAt, &expiresAt,
		&monthlyTrafficLimit, &value.TrafficCountMode, &value.TrafficResetDay, &value.TrafficResetTime,
		&lastSeenAt, &implementation, &storedAgentVersion, &apiVersion, &capabilitiesJSON,
		&upgradeTarget, &upgradeStatus, &upgradeError,
		&hostname, &osName, &osVersion, &kernel, &arch, &ipv4JSON, &ipv6JSON, &publicIPv4, &agentVersion, &reportedAt,
		&cpuPercent, &memoryUsed, &memoryTotal, &diskUsed, &diskTotal, &uptime,
		&nicRX, &nicTX, &cycleRX, &cycleTX, &trafficAdjustment, &cycleStartedAt, &metricsUpdatedAt,
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
	value.AgentImplementation = implementation.String
	value.AgentVersion = storedAgentVersion.String
	value.AgentAPIVersion = int(apiVersion.Int64)
	value.AgentCapabilities = []string{}
	if capabilitiesJSON.Valid {
		if err := json.Unmarshal([]byte(capabilitiesJSON.String), &value.AgentCapabilities); err != nil {
			return Server{}, fmt.Errorf("decode Agent capabilities: %w", err)
		}
	}
	value.AgentUpgradeTarget = upgradeTarget.String
	value.AgentUpgradeStatus = upgradeStatus.String
	value.AgentUpgradeError = upgradeError.String
	if reportedAt.Valid {
		info := SystemInfo{
			Hostname:     hostname.String,
			OSName:       osName.String,
			OSVersion:    osVersion.String,
			Kernel:       kernel.String,
			Arch:         arch.String,
			PublicIPv4:   publicIPv4.String,
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
			CPUPercent:             cpuPercent.Float64,
			MemoryUsedBytes:        memoryUsed.Int64,
			MemoryTotalBytes:       memoryTotal.Int64,
			DiskUsedBytes:          diskUsed.Int64,
			DiskTotalBytes:         diskTotal.Int64,
			UptimeSeconds:          uptime.Int64,
			NICRXBytes:             nicRX.Int64,
			NICTXBytes:             nicTX.Int64,
			CycleRXBytes:           cycleRX.Int64,
			CycleTXBytes:           cycleTX.Int64,
			TrafficAdjustmentBytes: trafficAdjustment.Int64,
			UpdatedAt:              time.Unix(metricsUpdatedAt.Int64, 0).UTC(),
		}
		if cycleStartedAt.Valid {
			startedAt := time.Unix(cycleStartedAt.Int64, 0).UTC()
			metrics.CycleStartedAt = &startedAt
		}
		value.Metrics = metrics
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if accessUserIDs != "" {
		for _, rawID := range strings.Split(accessUserIDs, ",") {
			userID, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || userID <= 0 {
				return Server{}, fmt.Errorf("decode server access user ID: %q", rawID)
			}
			value.AccessUserIDs = append(value.AccessUserIDs, userID)
		}
		sort.Slice(value.AccessUserIDs, func(i, j int) bool {
			return value.AccessUserIDs[i] < value.AccessUserIDs[j]
		})
	}
	return value, nil
}
