package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func (s *Service) GetDesiredState(ctx context.Context, agentID, serverID int64) (DesiredState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DesiredState{}, fmt.Errorf("begin desired state read: %w", err)
	}
	defer tx.Rollback()
	var state DesiredState
	if _, err := proxystore.ReconcileClientLifecycle(ctx, tx, serverID, s.now()); err != nil {
		return DesiredState{}, err
	}
	err = tx.QueryRowContext(ctx,
		`SELECT servers.desired_state_version
		 FROM agents JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ? AND agents.server_id = ? AND servers.archived_at IS NULL`,
		agentID, serverID,
	).Scan(&state.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return DesiredState{}, ErrInvalidAgentToken
	}
	if err != nil {
		return DesiredState{}, fmt.Errorf("read desired state: %w", err)
	}
	state.Proxies, err = proxystore.ListDesired(ctx, tx, serverID)
	if err != nil {
		return DesiredState{}, err
	}
	state.Relays, err = relaystore.NewService(s.db).ListDesired(ctx, tx, serverID)
	if err != nil {
		return DesiredState{}, err
	}
	if err := tx.Commit(); err != nil {
		return DesiredState{}, fmt.Errorf("commit desired state read: %w", err)
	}
	return state, nil
}
