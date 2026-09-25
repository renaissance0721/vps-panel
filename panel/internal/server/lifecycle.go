package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func (s *Service) EnsureMutable(ctx context.Context, id int64) error {
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read server lifecycle state: %w", err)
	}
	if status != "" {
		return ErrDecommissioning
	}
	return nil
}

func (s *Service) UpdateName(ctx context.Context, id int64, name string) (Server, error) {
	var err error
	name, err = normalizeServerName(name)
	if err != nil {
		return Server{}, err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		name, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server name: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server name count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) UpdateOutboundPreference(ctx context.Context, id int64, preference string) (Server, int64, error) {
	switch preference {
	case OutboundAuto, OutboundPreferIPv4, OutboundPreferIPv6:
	default:
		return Server{}, 0, ErrInvalidOutboundPreference
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, 0, fmt.Errorf("begin outbound preference update: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET outbound_preference = ?, desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`, preference, now, id,
	)
	if err != nil {
		return Server{}, 0, fmt.Errorf("update outbound preference: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, 0, fmt.Errorf("read updated outbound preference count: %w", err)
	}
	if count != 1 {
		return Server{}, 0, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now, id,
	); err != nil {
		return Server{}, 0, fmt.Errorf("mark Agent config pending: %w", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version FROM servers WHERE id = ?`, id,
	).Scan(&version); err != nil {
		return Server{}, 0, fmt.Errorf("read desired state version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, 0, fmt.Errorf("commit outbound preference update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, version, err
}

func (s *Service) UpdateBlockChinaInbound(ctx context.Context, id int64, enabled bool) (Server, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, 0, false, fmt.Errorf("begin China inbound block update: %w", err)
	}
	defer tx.Rollback()
	var current bool
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT block_china_inbound, desired_state_version FROM servers
		 WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&current, &version)
	if err != nil {
		if err == sql.ErrNoRows {
			return Server{}, 0, false, ErrNotFound
		}
		return Server{}, 0, false, fmt.Errorf("read China inbound block setting: %w", err)
	}
	if current == enabled {
		_ = tx.Rollback()
		updated, getErr := s.Get(ctx, id)
		return updated, version, false, getErr
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET block_china_inbound = ?, desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ?`, enabled, now, id,
	); err != nil {
		return Server{}, 0, false, fmt.Errorf("update China inbound block setting: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now, id,
	); err != nil {
		return Server{}, 0, false, fmt.Errorf("mark Agent config pending: %w", err)
	}
	version++
	if err := tx.Commit(); err != nil {
		return Server{}, 0, false, fmt.Errorf("commit China inbound block update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, version, true, err
}

func (s *Service) UpdateExpiration(ctx context.Context, id int64, expiresAt *time.Time) (Server, error) {
	return s.UpdateRenewalSettings(ctx, id, RenewalSettingsUpdate{
		ExpiresAtSet: true,
		ExpiresAt:    expiresAt,
	})
}

func (s *Service) RequestDecommission(ctx context.Context, id int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin server decommission: %w", err)
	}
	defer tx.Rollback()

	var currentStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read server decommission state: %w", err)
	}
	if currentStatus != "" {
		return 0, ErrDecommissioning
	}

	var implementation, version, capabilitiesJSON string
	var apiVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT implementation, version, api_version, capabilities_json FROM agents WHERE server_id = ?`, id,
	).Scan(&implementation, &version, &apiVersion, &capabilitiesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrAgentNotRegistered
	}
	if err != nil {
		return 0, fmt.Errorf("read server Agent metadata: %w", err)
	}
	capabilities, err := agentcontrol.DecodeCapabilities(capabilitiesJSON)
	if err != nil {
		return 0, err
	}
	metadata := agentcontrol.Metadata{
		Implementation: implementation,
		Version:        version,
		APIVersion:     apiVersion,
		Capabilities:   capabilities,
	}
	if !agentcontrol.DeclaresCapability(metadata, agentcontrol.CapabilityManagedRuntimePurge) ||
		!agentcontrol.DeclaresCapability(metadata, agentcontrol.CapabilitySelfDecommission) {
		return 0, ErrDecommissionUnsupported
	}

	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers
		 SET decommissioning_at = ?, decommission_status = ?, decommission_error = '',
		     desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND decommission_status = ''`,
		now, DecommissionPending, now, id,
	)
	if err != nil {
		return 0, fmt.Errorf("request server decommission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read requested server decommission count: %w", err)
	}
	if count != 1 {
		return 0, ErrDecommissioning
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now, id,
	); err != nil {
		return 0, fmt.Errorf("mark decommission config pending: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return 0, fmt.Errorf("remove unused Agent enrollments: %w", err)
	}
	var desiredVersion int64
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version FROM servers WHERE id = ?`, id,
	).Scan(&desiredVersion); err != nil {
		return 0, fmt.Errorf("read decommission desired state version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit server decommission: %w", err)
	}
	return desiredVersion, nil
}

func (s *Service) FinalizeDecommission(ctx context.Context, id, version int64, status, message string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin decommission result: %w", err)
	}
	defer tx.Rollback()

	var desiredVersion int64
	var decommissionStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT desired_state_version, decommission_status FROM servers
		 WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&desiredVersion, &decommissionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("read decommission result state: %w", err)
	}
	if version != desiredVersion || (decommissionStatus != DecommissionPending && decommissionStatus != DecommissionFailed) {
		return false, nil
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if status == agentcontrol.ConfigSyncFailed {
		message = decommissionPublicError(message)
		if _, err := tx.ExecContext(ctx,
			`UPDATE servers SET decommission_status = ?, decommission_error = ?, updated_at = ? WHERE id = ?`,
			DecommissionFailed, message, now, id,
		); err != nil {
			return false, fmt.Errorf("record decommission failure: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit decommission failure: %w", err)
		}
		return false, nil
	}
	if status != agentcontrol.ConfigSyncSuccess {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers
		 SET status = ?, archived_at = ?, decommissioning_at = NULL,
		     decommission_status = '', decommission_error = '', updated_at = ?
		 WHERE id = ?`,
		StatusOffline, now, now, id,
	); err != nil {
		return false, fmt.Errorf("archive decommissioned server: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return false, fmt.Errorf("revoke decommissioned server Agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return false, fmt.Errorf("remove decommissioned server enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit decommission completion: %w", err)
	}
	return true, nil
}

func decommissionPublicError(message string) string {
	switch strings.TrimSpace(message) {
	case "managed runtime purge failed":
		return "managed runtime purge failed"
	case "Agent self-uninstall launch failed":
		return "Agent self-uninstall launch failed"
	default:
		return "managed runtime purge failed"
	}
}

func (s *Service) Archive(ctx context.Context, id int64) error {
	return s.ForceArchive(ctx, id)
}

func (s *Service) ForceArchive(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server archive: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = ?, decommissioning_at = NULL,
		 decommission_status = '', decommission_error = '', updated_at = ?
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
