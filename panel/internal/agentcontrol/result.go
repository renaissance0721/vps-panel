package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Service) RecordConfigResult(ctx context.Context, agentID, serverID int64, result ConfigResult) error {
	if result.Version <= 0 ||
		(result.Status != ConfigSyncSuccess && result.Status != ConfigSyncFailed) ||
		len(result.Message) > maxConfigSyncErrorBytes {
		return ErrInvalidConfigResult
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent config result: %w", err)
	}
	return nil
}
