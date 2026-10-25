package api

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *server) deleteProxyWithPersonalReferences(ctx context.Context, id int64, allowManagedPurge bool) (proxystore.Mutation, error) {
	var mutation proxystore.Mutation
	err := s.deletePersonalSubscriptionSource(ctx, func(tx *sql.Tx, now time.Time) error {
		var err error
		mutation, err = s.proxies.DeleteWithManagedPurgeTx(ctx, tx, id, allowManagedPurge, now)
		return err
	})
	return mutation, err
}

func (s *server) deleteRelayWithPersonalReferences(ctx context.Context, id int64, allowManagedPurge bool) (relaystore.Mutation, error) {
	var mutation relaystore.Mutation
	err := s.deletePersonalSubscriptionSource(ctx, func(tx *sql.Tx, now time.Time) error {
		var err error
		mutation, err = s.relays.DeleteWithManagedPurgeTx(ctx, tx, id, allowManagedPurge, now)
		return err
	})
	return mutation, err
}

func (s *server) deleteLandingWithPersonalReferences(ctx context.Context, id, userID int64) error {
	return s.deletePersonalSubscriptionSource(ctx, func(tx *sql.Tx, _ time.Time) error {
		return s.landings.DeleteTx(ctx, tx, id, userID)
	})
}

func (s *server) deletePersonalSubscriptionSource(ctx context.Context, deleteSource func(*sql.Tx, time.Time) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin personal subscription source deletion: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Truncate(time.Second)
	if err := deleteSource(tx, now); err != nil {
		return err
	}
	if err := subscriptionstore.PruneDeletedPersonalSubscriptionSourcesTx(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit personal subscription source deletion: %w", err)
	}
	return nil
}
