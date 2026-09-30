package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/renaissance0721/vps-panel/panel/internal/operation"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *Service) GetDesiredState(ctx context.Context, agentID, serverID int64) (DesiredState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DesiredState{}, fmt.Errorf("begin desired state read: %w", err)
	}
	defer tx.Rollback()
	var state DesiredState
	var decommissionStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT servers.desired_state_version, servers.decommission_status,
		        servers.block_china_inbound, servers.outbound_preference
		 FROM agents JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ? AND agents.server_id = ? AND servers.archived_at IS NULL`,
		agentID, serverID,
	).Scan(&state.Version, &decommissionStatus, &state.BlockChinaInbound, &state.OutboundPreference)
	if errors.Is(err, sql.ErrNoRows) {
		return DesiredState{}, ErrInvalidAgentToken
	}
	if err != nil {
		return DesiredState{}, fmt.Errorf("read desired state: %w", err)
	}
	if decommissionStatus != "" {
		state.Decommission = true
		state.BlockChinaInbound = false
		state.XrayPurge = true
		state.RealmPurge = true
		state.Proxies = []proxystore.DesiredProxy{}
		state.Relays = []relaystore.DesiredRelay{}
		if err := operation.MarkSentTx(ctx, tx, serverID, state.Version, s.now()); err != nil {
			return DesiredState{}, err
		}
		if err := tx.Commit(); err != nil {
			return DesiredState{}, fmt.Errorf("commit decommission desired state read: %w", err)
		}
		return state, nil
	}
	now := s.now().UTC()
	affectedServerIDs, err := subscriptionstore.NewService(s.db, relaystore.NewService(s.db)).
		ReconcileServerSubscribersTx(ctx, tx, serverID, now)
	if err != nil {
		return DesiredState{}, err
	}
	ordinaryChanged, err := proxystore.ReconcileClientLifecycleStateTx(ctx, tx, serverID, now)
	if err != nil {
		return DesiredState{}, err
	}
	if ordinaryChanged {
		affectedServerIDs = append(affectedServerIDs, serverID)
	}
	mutations, err := proxystore.BumpServerVersionsTx(ctx, tx, affectedServerIDs, now)
	if err != nil {
		return DesiredState{}, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version, block_china_inbound, outbound_preference
		 FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
	).Scan(&state.Version, &state.BlockChinaInbound, &state.OutboundPreference); err != nil {
		return DesiredState{}, fmt.Errorf("refresh desired state after client lifecycle reconciliation: %w", err)
	}
	state.Proxies, err = proxystore.ListDesired(ctx, tx, serverID)
	if err != nil {
		return DesiredState{}, err
	}
	var hasProxies, hasRelays bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM proxies WHERE server_id = ?),
		        EXISTS(SELECT 1 FROM relays WHERE server_id = ?)`, serverID, serverID,
	).Scan(&hasProxies, &hasRelays); err != nil {
		return DesiredState{}, fmt.Errorf("read managed runtime presence: %w", err)
	}
	state.XrayPurge = !hasProxies
	state.RealmPurge = !hasRelays
	state.Relays, err = relaystore.NewService(s.db).ListDesired(ctx, tx, serverID)
	if err != nil {
		return DesiredState{}, err
	}
	if err := operation.MarkSentTx(ctx, tx, serverID, state.Version, s.now()); err != nil {
		return DesiredState{}, err
	}
	if err := tx.Commit(); err != nil {
		return DesiredState{}, fmt.Errorf("commit desired state read: %w", err)
	}
	for _, mutation := range mutations {
		if mutation.ServerID == serverID {
			continue
		}
		if err := s.NotifyConfigChanged(mutation.ServerID, mutation.Version); err != nil {
			log.Printf("notify Agent for server %d config version %d: %v", mutation.ServerID, mutation.Version, err)
		}
	}
	return state, nil
}
