package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func (s *Service) CreateEnrollment(ctx context.Context, id int64) (CreatedServer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin Agent enrollment creation: %w", err)
	}
	defer tx.Rollback()

	value, err := scanServer(tx.QueryRowContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.visibility, servers.outbound_preference, servers.block_china_inbound,
		 servers.desired_state_version,
		 COALESCE((SELECT group_concat(user_id) FROM server_access WHERE server_id = servers.id), ''),
		 servers.archived_at, servers.expires_at, servers.renewal_period_months, servers.auto_renew, servers.renewal_anchor_day,
		 servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time,
		 agent.last_seen_at, agent.implementation, agent.version, agent.api_version, agent.capabilities_json,
		 agent.upgrade_target_version, agent.upgrade_status, agent.upgrade_error,
		 agent.applied_config_version, agent.config_sync_status, agent.config_sync_error, agent.config_synced_at,
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
		 WHERE servers.id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return CreatedServer{}, ErrNotFound
	}
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server for Agent enrollment: %w", err)
	}

	enrollment, err := agentcontrol.RotateEnrollment(ctx, tx, id, s.now)
	if err != nil {
		return CreatedServer{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit Agent enrollment creation: %w", err)
	}
	value.Status = StatusPending
	value.LastSeenAt = nil
	value.UpdatedAt = enrollment.CreatedAt
	return CreatedServer{
		Server:              value,
		EnrollmentToken:     enrollment.Token,
		EnrollmentExpiresAt: enrollment.ExpiresAt,
	}, nil
}
