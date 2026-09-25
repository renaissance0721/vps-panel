package relay

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DesiredRelay struct {
	ID            int64  `json:"id"`
	ListenAddress string `json:"listen_address"`
	ListenPort    int    `json:"listen_port"`
	TargetHost    string `json:"target_host"`
	TargetPort    int    `json:"target_port"`
	Network       string `json:"network"`
}

func (s *Service) ListDesired(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID int64) ([]DesiredRelay, error) {
	values, err := list(ctx, query, `WHERE relays.server_id = ? AND relays.enabled = 1 AND source.archived_at IS NULL ORDER BY relays.id`, serverID)
	if err != nil {
		return nil, err
	}
	desired := make([]DesiredRelay, 0, len(values))
	for _, value := range values {
		if !value.TargetAddressReady {
			return nil, fmt.Errorf("%w: relay %d", ErrTargetUnavailable, value.ID)
		}
		desired = append(desired, DesiredRelay{
			ID: value.ID, ListenAddress: value.ListenAddress, ListenPort: value.ListenPort,
			TargetHost: value.TargetHost, TargetPort: value.TargetPort, Network: value.Network,
		})
	}
	return desired, nil
}

func (s *Service) BumpForProxyTarget(ctx context.Context, proxyID, excludeServerID int64) ([]Mutation, error) {
	return s.bumpDependencies(ctx,
		`SELECT DISTINCT relays.server_id FROM relays
		 JOIN servers ON servers.id = relays.server_id
		 WHERE relays.target_type = 'proxy' AND relays.target_proxy_id = ?
		   AND relays.server_id != ? AND servers.archived_at IS NULL AND servers.decommission_status = ''
		 ORDER BY relays.server_id`, proxyID, excludeServerID)
}

func (s *Service) BumpForLandingTarget(ctx context.Context, landingID int64) ([]Mutation, error) {
	return s.bumpDependencies(ctx,
		`SELECT DISTINCT relays.server_id FROM relays
		 JOIN servers ON servers.id = relays.server_id
		 WHERE relays.target_type = 'landing' AND relays.target_landing_id = ?
		   AND servers.archived_at IS NULL AND servers.decommission_status = ''
		 ORDER BY relays.server_id`, landingID)
}

func (s *Service) BumpForAutoTargetServer(ctx context.Context, targetServerID int64) ([]Mutation, error) {
	return s.bumpDependencies(ctx,
		`SELECT DISTINCT relays.server_id FROM relays
		 JOIN servers ON servers.id = relays.server_id
		 JOIN proxies ON proxies.id = relays.target_proxy_id
		 WHERE relays.target_type = 'proxy' AND proxies.server_id = ?
		   AND proxies.entry_host_mode = 'auto' AND servers.archived_at IS NULL AND servers.decommission_status = ''
		 ORDER BY relays.server_id`, targetServerID)
}

func (s *Service) bumpDependencies(ctx context.Context, statement string, arguments ...any) ([]Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin relay dependency update: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list relay dependencies: %w", err)
	}
	serverIDs := make([]int64, 0)
	for rows.Next() {
		var serverID int64
		if err := rows.Scan(&serverID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan relay dependency: %w", err)
		}
		serverIDs = append(serverIDs, serverID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close relay dependencies: %w", err)
	}
	mutations := make([]Mutation, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		version, err := bumpVersion(ctx, tx, serverID, now)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, Mutation{ServerID: serverID, Version: version})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit relay dependency update: %w", err)
	}
	return mutations, nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, serverID int64, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND decommission_status = ''`, now.Unix(), serverID,
	)
	if err != nil {
		return 0, fmt.Errorf("bump relay desired state version: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read relay desired state update: %w", err)
	}
	if count != 1 {
		var status string
		if err := tx.QueryRowContext(ctx,
			`SELECT decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
		).Scan(&status); err == nil && status != "" {
			return 0, ErrServerDecommissioning
		}
		return 0, ErrServerNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now.Unix(), serverID,
	); err != nil {
		return 0, fmt.Errorf("mark Agent config sync pending: %w", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version FROM servers WHERE id = ?`, serverID,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("read relay desired state version: %w", err)
	}
	return version, nil
}
