package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/netutil"
	"github.com/renaissance0721/vps-panel/panel/internal/operation"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
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

func (s *Service) UpdateBoundDomains(ctx context.Context, id int64, boundDomainIPv4, boundDomainIPv6 *string) (Server, error) {
	var ipv4Value, ipv6Value any
	if boundDomainIPv4 != nil {
		normalized, err := normalizeBoundDomain(*boundDomainIPv4)
		if err != nil {
			return Server{}, err
		}
		ipv4Value = normalized
	}
	if boundDomainIPv6 != nil {
		normalized, err := normalizeBoundDomain(*boundDomainIPv6)
		if err != nil {
			return Server{}, err
		}
		if normalized != "" {
			current, err := s.Get(ctx, id)
			if err != nil {
				return Server{}, err
			}
			if normalized != current.BoundDomainIPv6 && current.SystemInfo != nil &&
				!netutil.HasIPv6Stack(current.SystemInfo.IPv6) &&
				netutil.EffectivePublicIPv6(current.SystemInfo.PublicIPv6, current.SystemInfo.IPv6) == "" {
				return Server{}, ErrIPv6Unavailable
			}
		}
		ipv6Value = normalized
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET bound_domain_ipv4 = COALESCE(?, bound_domain_ipv4),
		 bound_domain_ipv6 = COALESCE(?, bound_domain_ipv6), updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		ipv4Value, ipv6Value, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server bound domains: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server bound domain count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) UpdateOwner(ctx context.Context, id int64, ownerUserID *int64) (Server, error) {
	if ownerUserID != nil && *ownerUserID <= 0 {
		return Server{}, ErrInvalidServerOwner
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("begin server owner update: %w", err)
	}
	defer tx.Rollback()

	var ownerValue any
	if ownerUserID != nil {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, *ownerUserID).Scan(&exists); err != nil {
			return Server{}, fmt.Errorf("validate server owner: %w", err)
		}
		if !exists {
			return Server{}, ErrInvalidServerOwner
		}
		ownerValue = *ownerUserID
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET owner_user_id = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		ownerValue, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server owner: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server owner count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("commit server owner update: %w", err)
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
	if err := operation.RecordTx(ctx, tx, id, "server", id, "update", version, time.Unix(now, 0)); err != nil {
		return Server{}, 0, err
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
	if err := operation.RecordTx(ctx, tx, id, "server", id, "update", version, time.Unix(now, 0)); err != nil {
		return Server{}, 0, false, err
	}
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
	if err := operation.RecordTx(ctx, tx, id, "server", id, "update", desiredVersion, time.Unix(now, 0)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit server decommission: %w", err)
	}
	return desiredVersion, nil
}

func (s *Service) FinalizeDecommission(ctx context.Context, id, version int64, status, message string) (bool, error) {
	finalized, _, err := s.FinalizeDecommissionWithMutations(ctx, id, version, status, message)
	return finalized, err
}

func (s *Service) FinalizeDecommissionWithMutations(ctx context.Context, id, version int64, status, message string) (bool, []ConfigMutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, fmt.Errorf("begin decommission result: %w", err)
	}
	defer tx.Rollback()

	var desiredVersion int64
	var decommissionStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT desired_state_version, decommission_status FROM servers
		 WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&desiredVersion, &decommissionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, ErrNotFound
	}
	if err != nil {
		return false, nil, fmt.Errorf("read decommission result state: %w", err)
	}
	if version != desiredVersion || (decommissionStatus != DecommissionPending && decommissionStatus != DecommissionFailed) {
		return false, nil, nil
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if status == agentcontrol.ConfigSyncFailed {
		message = decommissionPublicError(message)
		if _, err := tx.ExecContext(ctx,
			`UPDATE servers SET decommission_status = ?, decommission_error = ?, updated_at = ? WHERE id = ?`,
			DecommissionFailed, message, now, id,
		); err != nil {
			return false, nil, fmt.Errorf("record decommission failure: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, nil, fmt.Errorf("commit decommission failure: %w", err)
		}
		return false, nil, nil
	}
	if status != agentcontrol.ConfigSyncSuccess {
		return false, nil, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers
		 SET status = ?, archived_at = ?, decommissioning_at = NULL,
		     decommission_status = '', decommission_error = '', updated_at = ?
		 WHERE id = ?`,
		StatusOffline, now, now, id,
	); err != nil {
		return false, nil, fmt.Errorf("archive decommissioned server: %w", err)
	}
	mutations, err := s.cleanupArchivedDependenciesTx(ctx, tx, id, time.Unix(now, 0))
	if err != nil {
		return false, nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return false, nil, fmt.Errorf("revoke decommissioned server Agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return false, nil, fmt.Errorf("remove decommissioned server enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, nil, fmt.Errorf("commit decommission completion: %w", err)
	}
	return true, mutations, nil
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
	_, err := s.ForceArchiveWithMutations(ctx, id)
	return err
}

func (s *Service) ForceArchiveWithMutations(ctx context.Context, id int64) ([]ConfigMutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin server archive: %w", err)
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
		return nil, fmt.Errorf("archive server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read archived server count: %w", err)
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	mutations, err := s.cleanupArchivedDependenciesTx(ctx, tx, id, time.Unix(now, 0))
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return nil, fmt.Errorf("revoke archived server agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return nil, fmt.Errorf("remove unused agent enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit server archive: %w", err)
	}
	return mutations, nil
}

func (s *Service) PermanentlyDelete(ctx context.Context, id int64) error {
	_, err := s.PermanentlyDeleteWithMutations(ctx, id)
	return err
}

func (s *Service) PermanentlyDeleteWithMutations(ctx context.Context, id int64) ([]ConfigMutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin permanent server deletion: %w", err)
	}
	defer tx.Rollback()
	var archivedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT archived_at FROM servers WHERE id = ?`, id).Scan(&archivedAt); errors.Is(err, sql.ErrNoRows) || !archivedAt.Valid {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read permanently deleted server: %w", err)
	}
	mutations, err := s.cleanupArchivedDependenciesTx(ctx, tx, id, s.now().UTC().Truncate(time.Second))
	if err != nil {
		return nil, err
	}
	if err := deleteArchivedDependenciesTx(ctx, tx, id); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM servers WHERE id = ? AND archived_at IS NOT NULL`, id)
	if err != nil {
		return nil, fmt.Errorf("delete archived server after unlinking dependencies: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read permanently deleted server count: %w", err)
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit permanent server deletion: %w", err)
	}
	return mutations, nil
}

func (s *Service) GetDependencySummary(ctx context.Context, id int64) (DependencySummary, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM servers WHERE id = ?)`, id).Scan(&exists); err != nil {
		return DependencySummary{}, fmt.Errorf("check server dependency summary target: %w", err)
	}
	if !exists {
		return DependencySummary{}, ErrNotFound
	}
	var summary DependencySummary
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM proxies WHERE server_id = ?),
		(SELECT COUNT(*) FROM clients WHERE proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)),
		(SELECT COUNT(*) FROM relays WHERE server_id = ?),
		(SELECT COUNT(*) FROM subscription_published_nodes WHERE
			target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_server_id = ?
			OR relay_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))),
		(SELECT COUNT(*) FROM personal_subscription_nodes WHERE
			(source_type = 'proxy' AND source_id IN (SELECT id FROM proxies WHERE server_id = ?))
			OR (source_type = 'relay' AND source_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?)))),
		(SELECT COUNT(*) FROM subscriber_clients WHERE proxy_id IN (SELECT id FROM proxies WHERE server_id = ?))`,
		id, id, id, id, id, id, id, id, id, id, id, id, id,
	).Scan(&summary.ProxyCount, &summary.ClientCount, &summary.OwnedRelayCount,
		&summary.PublishedNodeCount, &summary.PersonalNodeCount, &summary.SubscriberClientCount)
	if err != nil {
		return DependencySummary{}, fmt.Errorf("read server dependency summary: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT relays.name, source.name
		FROM relays
		JOIN servers AS source ON source.id = relays.server_id
		WHERE relays.server_id != ? AND relays.target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
		ORDER BY source.name, relays.name, relays.id`, id, id)
	if err != nil {
		return DependencySummary{}, fmt.Errorf("list server relay dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value DependencyRelay
		if err := rows.Scan(&value.Name, &value.SourceServerName); err != nil {
			return DependencySummary{}, fmt.Errorf("scan server relay dependency: %w", err)
		}
		summary.ReferencingRelays = append(summary.ReferencingRelays, value)
	}
	if err := rows.Err(); err != nil {
		return DependencySummary{}, fmt.Errorf("iterate server relay dependencies: %w", err)
	}
	return summary, nil
}

func (s *Service) cleanupArchivedDependenciesTx(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) ([]ConfigMutation, error) {
	now = now.UTC().Truncate(time.Second)
	affectedServers := make(map[int64]struct{})
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT relays.server_id
		FROM relays
		JOIN servers AS source ON source.id = relays.server_id
		WHERE relays.enabled = 1 AND relays.server_id != ?
		  AND source.archived_at IS NULL AND source.decommission_status = ''
		  AND (relays.target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
		       OR relays.source_client_id IN (
		          SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))
		ORDER BY relays.server_id`, serverID, serverID, serverID)
	if err != nil {
		return nil, fmt.Errorf("list cross-server relay dependencies: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan cross-server relay dependency: %w", err)
		}
		affectedServers[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate cross-server relay dependencies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close cross-server relay dependencies: %w", err)
	}

	publishedNodeIDs, err := queryInt64s(ctx, tx, `SELECT id FROM subscription_published_nodes
		WHERE enabled = 1 AND (
			target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_server_id = ?
			OR relay_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?)))
		ORDER BY id`, serverID, serverID, serverID, serverID, serverID)
	if err != nil {
		return nil, fmt.Errorf("list affected subscription nodes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE proxies SET enabled = 0, updated_at = ? WHERE server_id = ? AND enabled = 1`, now.Unix(), serverID); err != nil {
		return nil, fmt.Errorf("disable archived server proxies: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE relays SET enabled = 0, updated_at = ?
		WHERE enabled = 1 AND (server_id = ?
			OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))`,
		now.Unix(), serverID, serverID, serverID); err != nil {
		return nil, fmt.Errorf("disable archived server relay dependencies: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscription_published_nodes SET enabled = 0, updated_at = ?
		WHERE enabled = 1 AND (
			target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_server_id = ?
			OR relay_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?)))`,
		now.Unix(), serverID, serverID, serverID, serverID, serverID); err != nil {
		return nil, fmt.Errorf("disable archived server subscription nodes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personal_subscription_nodes SET enabled = 0, updated_at = ?
		WHERE enabled = 1 AND (
			(source_type = 'proxy' AND source_id IN (SELECT id FROM proxies WHERE server_id = ?))
			OR (source_type = 'relay' AND source_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))))`,
		now.Unix(), serverID, serverID, serverID, serverID); err != nil {
		return nil, fmt.Errorf("disable archived server personal subscription nodes: %w", err)
	}

	subscriptions := subscriptionstore.NewService(s.db, relaystore.NewService(s.db))
	serverIDs, err := subscriptions.ReconcilePublishedNodesSubscribersTx(ctx, tx, publishedNodeIDs, now)
	if err != nil {
		return nil, fmt.Errorf("reconcile affected published-node subscribers: %w", err)
	}
	for _, id := range serverIDs {
		affectedServers[id] = struct{}{}
	}
	serverIDs, err = subscriptions.ReconcileServerSubscribersTx(ctx, tx, serverID, now)
	if err != nil {
		return nil, fmt.Errorf("reconcile archived server subscribers: %w", err)
	}
	for _, id := range serverIDs {
		affectedServers[id] = struct{}{}
	}
	delete(affectedServers, serverID)
	return bumpDependencyServersTx(ctx, tx, affectedServers, serverID, now)
}

func bumpDependencyServersTx(ctx context.Context, tx *sql.Tx, serverIDs map[int64]struct{}, resourceID int64, now time.Time) ([]ConfigMutation, error) {
	ordered := make([]int64, 0, len(serverIDs))
	for serverID := range serverIDs {
		ordered = append(ordered, serverID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	mutations := make([]ConfigMutation, 0, len(ordered))
	for _, serverID := range ordered {
		result, err := tx.ExecContext(ctx, `UPDATE servers
			SET desired_state_version = desired_state_version + 1, updated_at = ?
			WHERE id = ? AND archived_at IS NULL AND decommission_status = ''`, now.Unix(), serverID)
		if err != nil {
			return nil, fmt.Errorf("bump dependency source server version: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read dependency source server update: %w", err)
		}
		if count == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agents
			SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
			now.Unix(), serverID); err != nil {
			return nil, fmt.Errorf("mark dependency source Agent pending: %w", err)
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&version); err != nil {
			return nil, fmt.Errorf("read dependency source version: %w", err)
		}
		if err := operation.RecordTx(ctx, tx, serverID, "server", resourceID, "reconcile", version, now); err != nil {
			return nil, err
		}
		mutations = append(mutations, ConfigMutation{ServerID: serverID, Version: version})
	}
	return mutations, nil
}

func queryInt64s(ctx context.Context, tx *sql.Tx, statement string, arguments ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func deleteArchivedDependenciesTx(ctx context.Context, tx *sql.Tx, serverID int64) error {
	statements := []struct {
		name string
		sql  string
	}{
		{"unlink subscription plans", `DELETE FROM subscription_plan_nodes WHERE published_node_id IN (
			SELECT id FROM subscription_published_nodes WHERE target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_server_id = ? OR relay_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?)))`},
		{"remove personal subscription references", `DELETE FROM personal_subscription_nodes WHERE
			(source_type = 'proxy' AND source_id IN (SELECT id FROM proxies WHERE server_id = ?))
			OR (source_type = 'relay' AND source_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?)))`},
		{"remove published subscription references", `DELETE FROM subscription_published_nodes WHERE
			target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?) OR source_server_id = ?
			OR relay_id IN (SELECT id FROM relays WHERE server_id = ?
				OR target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
				OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))`},
		{"remove subscriber client references", `DELETE FROM subscriber_clients WHERE proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)`},
		{"remove cross-server relay references", `DELETE FROM relays WHERE server_id != ? AND (
			target_proxy_id IN (SELECT id FROM proxies WHERE server_id = ?)
			OR source_client_id IN (SELECT clients.id FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE proxies.server_id = ?))`},
	}
	for _, statement := range statements {
		var args []any
		switch statement.name {
		case "unlink subscription plans", "remove published subscription references":
			args = []any{serverID, serverID, serverID, serverID, serverID}
		case "remove personal subscription references":
			args = []any{serverID, serverID, serverID, serverID}
		case "remove subscriber client references":
			args = []any{serverID}
		case "remove cross-server relay references":
			args = []any{serverID, serverID, serverID}
		}
		if _, err := tx.ExecContext(ctx, statement.sql, args...); err != nil {
			return fmt.Errorf("%s before permanent server deletion: %w", statement.name, err)
		}
	}
	return nil
}
