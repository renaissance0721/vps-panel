package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/version"
)

func (s *Service) PrepareAgentUpgrade(ctx context.Context, serverID int64, targetVersion string) (AgentUpgrade, error) {
	targetVersion = strings.TrimSpace(targetVersion)
	if !version.IsFormal(targetVersion) {
		return AgentUpgrade{}, ErrInvalidUpgrade
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentUpgrade{}, fmt.Errorf("begin Agent upgrade: %w", err)
	}
	defer tx.Rollback()
	var status string
	var archivedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT status, archived_at FROM servers WHERE id = ?`, serverID,
	).Scan(&status, &archivedAt); errors.Is(err, sql.ErrNoRows) {
		return AgentUpgrade{}, ErrServerNotFound
	} else if err != nil {
		return AgentUpgrade{}, fmt.Errorf("read Agent upgrade server: %w", err)
	}
	if archivedAt.Valid || status != statusOnline {
		return AgentUpgrade{}, ErrAgentOffline
	}
	var agentID int64
	var implementation, currentVersion, capabilitiesJSON string
	var apiVersion int
	if err := tx.QueryRowContext(ctx,
		`SELECT id, implementation, version, api_version, capabilities_json
		 FROM agents WHERE server_id = ?`, serverID,
	).Scan(&agentID, &implementation, &currentVersion, &apiVersion, &capabilitiesJSON); errors.Is(err, sql.ErrNoRows) {
		return AgentUpgrade{}, ErrAgentNotRegistered
	} else if err != nil {
		return AgentUpgrade{}, fmt.Errorf("read Agent for upgrade: %w", err)
	}
	capabilities, err := DecodeCapabilities(capabilitiesJSON)
	if err != nil {
		return AgentUpgrade{}, err
	}
	if !CanSelfUpgrade(implementation, apiVersion, capabilities) {
		return AgentUpgrade{}, ErrAgentUpgradeUnsupported
	}
	comparison, ok := version.Compare(currentVersion, targetVersion)
	if !ok {
		return AgentUpgrade{}, ErrUnknownAgentVersion
	}
	if comparison == 0 {
		return AgentUpgrade{AlreadyCurrent: true}, nil
	}
	if comparison > 0 {
		return AgentUpgrade{}, fmt.Errorf("%w: Agent %s is newer than Panel %s", ErrAgentNewer, currentVersion, targetVersion)
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET upgrade_target_version = ?, upgrade_status = ?, upgrade_error = '', updated_at = ?
		 WHERE id = ? AND server_id = ?`,
		targetVersion, AgentUpgradeUpgrading, now, agentID, serverID,
	); err != nil {
		return AgentUpgrade{}, fmt.Errorf("prepare Agent upgrade: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentUpgrade{}, fmt.Errorf("commit Agent upgrade: %w", err)
	}
	return AgentUpgrade{}, nil
}

func (s *Service) RecordAgentUpgradeFailure(
	ctx context.Context,
	agentID, serverID int64,
	targetVersion, message string,
) error {
	targetVersion = strings.TrimSpace(targetVersion)
	message = strings.TrimSpace(message)
	if targetVersion == "" || utf8.RuneCountInString(targetVersion) > 64 || message == "" {
		return ErrInvalidUpgrade
	}
	if len(message) > maxConfigSyncErrorBytes {
		message = message[:maxConfigSyncErrorBytes]
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET upgrade_status = ?, upgrade_error = ?, updated_at = ?
		 WHERE id = ? AND server_id = ? AND upgrade_target_version = ? AND upgrade_status = ?`,
		AgentUpgradeFailed, message, s.now().UTC().Truncate(time.Second).Unix(),
		agentID, serverID, targetVersion, AgentUpgradeUpgrading,
	)
	if err != nil {
		return fmt.Errorf("record Agent upgrade failure: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read Agent upgrade failure count: %w", err)
	}
	if count != 1 {
		return ErrInvalidUpgrade
	}
	return nil
}

func (s *Service) MarkAgentUpgradeFailed(
	ctx context.Context,
	serverID int64,
	targetVersion, message string,
) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return ErrInvalidUpgrade
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET upgrade_status = ?, upgrade_error = ?, updated_at = ?
		 WHERE server_id = ? AND upgrade_target_version = ? AND upgrade_status = ?`,
		AgentUpgradeFailed, message, s.now().UTC().Truncate(time.Second).Unix(),
		serverID, targetVersion, AgentUpgradeUpgrading,
	)
	if err != nil {
		return fmt.Errorf("mark Agent upgrade failed: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read failed Agent upgrade count: %w", err)
	}
	if count != 1 {
		return ErrInvalidUpgrade
	}
	return nil
}
