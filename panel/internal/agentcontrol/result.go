package agentcontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/operation"
)

func (s *Service) RecordConfigResult(ctx context.Context, agentID, serverID int64, result ConfigResult) error {
	if result.Version <= 0 ||
		(result.Status != ConfigSyncSuccess && result.Status != ConfigSyncFailed) ||
		len(result.Message) > maxConfigSyncErrorBytes {
		return ErrInvalidConfigResult
	}
	for name, status := range result.Components {
		if name == "firewall_environment" {
			if status != "supported" && status != "unsupported" && status != "unknown" && status != "temporarily_unavailable" {
				return ErrInvalidConfigResult
			}
			continue
		}
		if (name != "xray" && name != "singbox" && name != "realm" && name != "china_firewall") ||
			(status != "success" && status != "failed" && status != "unsupported" && status != "unknown") {
			return ErrInvalidConfigResult
		}
	}
	if result.Components != nil {
		for _, name := range []string{"xray", "realm", "china_firewall"} {
			if result.Components[name] == "" {
				return ErrInvalidConfigResult
			}
		}
		if result.Status == ConfigSyncSuccess && (result.Components["xray"] != "success" || result.Components["realm"] != "success" || result.Components["singbox"] != "" && result.Components["singbox"] != "success" || result.Components["china_firewall"] == "failed" || result.Components["china_firewall"] == "unsupported") {
			return ErrInvalidConfigResult
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent config result: %w", err)
	}
	defer tx.Rollback()

	var desiredVersion, appliedVersion int64
	err = tx.QueryRowContext(ctx,
		`SELECT servers.desired_state_version, agents.applied_config_version
		 FROM agents JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ? AND agents.server_id = ? AND servers.archived_at IS NULL`,
		agentID, serverID,
	).Scan(&desiredVersion, &appliedVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidAgentToken
	}
	if err != nil {
		return fmt.Errorf("read Agent config state: %w", err)
	}
	if result.Version > desiredVersion {
		return ErrConfigVersionAhead
	}
	if result.Version < appliedVersion {
		return nil
	}
	if result.Version < desiredVersion {
		return nil
	}
	if result.Components != nil && result.Status == ConfigSyncSuccess && result.Components["china_firewall"] != "success" {
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT block_china_inbound FROM servers WHERE id = ?`, serverID).Scan(&enabled); err != nil {
			return err
		}
		if enabled {
			return ErrInvalidConfigResult
		}
	}
	// An older Agent may ignore an additive desired-state field and claim success.
	// Never record that claim for an enabled, nonempty whitelist.
	if result.Status == ConfigSyncSuccess && result.Version == desiredVersion {
		var needsWhitelist bool
		var implementation, version, capabilitiesJSON string
		var apiVersion int
		if err := tx.QueryRowContext(ctx, `SELECT servers.block_china_inbound AND servers.decommission_status = '' AND EXISTS(SELECT 1 FROM server_china_inbound_whitelist WHERE server_id = servers.id), agents.implementation, agents.version, agents.api_version, agents.capabilities_json FROM agents JOIN servers ON servers.id = agents.server_id WHERE agents.id = ? AND servers.id = ?`, agentID, serverID).Scan(&needsWhitelist, &implementation, &version, &apiVersion, &capabilitiesJSON); err != nil {
			return err
		}
		if needsWhitelist {
			capabilities, err := DecodeCapabilities(capabilitiesJSON)
			if err != nil {
				return err
			}
			if !DeclaresCapability(Metadata{Implementation: implementation, Version: version, APIVersion: apiVersion, Capabilities: capabilities}, CapabilityFirewallCNWhitelist) {
				result.Status = ConfigSyncFailed
				result.Message = "当前 Agent 不支持中国 IP 入站白名单，请升级 Agent"
			}
		}
	}

	now := s.now().UTC().Truncate(time.Second).Unix()
	if result.Status == ConfigSyncSuccess {
		_, err = tx.ExecContext(ctx,
			`UPDATE agents
			 SET applied_config_version = ?, config_sync_status = ?,
			     config_sync_error = '', config_synced_at = ?, updated_at = ?
			 WHERE id = ? AND server_id = ?`,
			max(appliedVersion, result.Version), ConfigSyncSuccess, now, now, agentID, serverID,
		)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE agents
			 SET config_sync_status = ?, config_sync_error = ?,
			     config_synced_at = ?, updated_at = ?
			 WHERE id = ? AND server_id = ?`,
			ConfigSyncFailed, result.Message, now, now, agentID, serverID,
		)
	}
	if err != nil {
		return fmt.Errorf("save Agent config result: %w", err)
	}
	var componentResult *ComponentResults
	if result.Components != nil {
		componentResult = &ComponentResults{Version: result.Version, Statuses: result.Components}
	}
	components, err := json.Marshal(componentResult)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET config_components_json = ? WHERE id = ? AND server_id = ?`, string(components), agentID, serverID); err != nil {
		return fmt.Errorf("save Agent component results: %w", err)
	}
	if err := operation.RecordReceiptTx(ctx, tx, serverID, result.Version, result.Status, result.Message, time.Unix(now, 0)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent config result: %w", err)
	}
	return nil
}

func (s *Service) RecordDesiredStateFailure(ctx context.Context, agentID, serverID int64, message string) error {
	if message == "" || len(message) > maxConfigSyncErrorBytes {
		return ErrInvalidConfigResult
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin desired state failure: %w", err)
	}
	defer tx.Rollback()
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT servers.desired_state_version
		FROM agents JOIN servers ON servers.id = agents.server_id
		WHERE agents.id = ? AND agents.server_id = ? AND servers.archived_at IS NULL`, agentID, serverID).Scan(&version); errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidAgentToken
	} else if err != nil {
		return fmt.Errorf("read desired state failure version: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `UPDATE agents
		SET config_sync_status = ?, config_sync_error = ?, config_synced_at = ?, updated_at = ?
		WHERE id = ? AND server_id = ?`, ConfigSyncFailed, message, now.Unix(), now.Unix(), agentID, serverID); err != nil {
		return fmt.Errorf("save desired state failure: %w", err)
	}
	if err := operation.RecordReceiptTx(ctx, tx, serverID, version, ConfigSyncFailed, message, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit desired state failure: %w", err)
	}
	return nil
}
