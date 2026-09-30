package proxy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/listener"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

const MaxClientRelayPorts = 5

func validateClientRelayPortCount(count int) error {
	if count < 0 || count > MaxClientRelayPorts {
		return ErrInvalidClientRelayPortCount
	}
	return nil
}

func reserveClientRelayPorts(ctx context.Context, tx *sql.Tx, clientID, serverID int64, count int, now time.Time) error {
	if err := validateClientRelayPortCount(count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	reservations, err := listener.ListOccupiedListeners(ctx, tx, serverID, relaystore.UserRelayPortStart, relaystore.UserRelayPortEnd)
	if err != nil {
		return err
	}
	occupied := make(map[int]bool)
	for _, reservation := range reservations {
		occupied[reservation.Port] = true
	}

	starts := make([]int, 0)
	for start := relaystore.UserRelayPortStart; start <= relaystore.UserRelayPortEnd-count+1; start++ {
		available := true
		for port := start; port < start+count; port++ {
			if occupied[port] {
				available = false
				break
			}
		}
		if available {
			starts = append(starts, start)
		}
	}

	for len(starts) > 0 {
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(starts))))
		if err != nil {
			return fmt.Errorf("choose client relay port range: %w", err)
		}
		index := int(choice.Int64())
		start := starts[index]
		starts[index] = starts[len(starts)-1]
		starts = starts[:len(starts)-1]

		inserted := true
		for port := start; port < start+count; port++ {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO client_relay_ports (client_id, server_id, port, created_at) VALUES (?, ?, ?, ?)`,
				clientID, serverID, port, now.Unix(),
			); err != nil {
				if !isUniqueConstraint(err) && !listener.IsConflict(err) {
					return fmt.Errorf("reserve client relay port: %w", err)
				}
				inserted = false
				if _, deleteErr := tx.ExecContext(ctx, `DELETE FROM client_relay_ports WHERE client_id = ?`, clientID); deleteErr != nil {
					return fmt.Errorf("retry client relay port reservation: %w", deleteErr)
				}
				break
			}
		}
		if inserted {
			return nil
		}
	}
	return ErrClientRelayPortsUnavailable
}

func (s *Service) ListAvailableClientRelayPorts(ctx context.Context, clientID int64) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT reserved.port
		FROM client_relay_ports AS reserved
		WHERE reserved.client_id = ?
		AND NOT EXISTS (
			SELECT 1 FROM relays WHERE relays.server_id = reserved.server_id AND relays.listen_port = reserved.port
		)
		AND NOT EXISTS (
			SELECT 1 FROM proxies WHERE proxies.server_id = reserved.server_id AND proxies.listen_port = reserved.port
		)
		ORDER BY reserved.port`, clientID)
	if err != nil {
		return nil, fmt.Errorf("list available client relay ports: %w", err)
	}
	defer rows.Close()
	ports := make([]int, 0, MaxClientRelayPorts)
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, fmt.Errorf("scan available client relay port: %w", err)
		}
		ports = append(ports, port)
	}
	return ports, rows.Err()
}

func (s *Service) UpdateClientRelayPortCount(ctx context.Context, clientID int64, count int) (Client, error) {
	if err := validateClientRelayPortCount(count); err != nil {
		return Client{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, fmt.Errorf("begin client relay port update: %w", err)
	}
	defer tx.Rollback()

	var serverID int64
	var assignedUserID sql.NullInt64
	var currentCount int
	var subscriptionManaged bool
	err = tx.QueryRowContext(ctx, `SELECT proxies.server_id, clients.assigned_user_id,
		(SELECT COUNT(*) FROM client_relay_ports WHERE client_id = clients.id),
		EXISTS(SELECT 1 FROM subscriber_clients WHERE client_id = clients.id)
		FROM clients JOIN proxies ON proxies.id = clients.proxy_id WHERE clients.id = ?`, clientID,
	).Scan(&serverID, &assignedUserID, &currentCount, &subscriptionManaged)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrClientNotFound
	}
	if err != nil {
		return Client{}, fmt.Errorf("read client relay port allocation: %w", err)
	}
	if subscriptionManaged {
		return Client{}, ErrSubscriptionManagedClient
	}
	if !assignedUserID.Valid {
		return Client{}, ErrClientNotAssigned
	}
	if currentCount == count {
		if err := tx.Commit(); err != nil {
			return Client{}, fmt.Errorf("commit unchanged client relay ports: %w", err)
		}
		return s.GetClient(ctx, clientID)
	}

	var activeRelayCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM relays WHERE owner_user_id IS NOT NULL AND source_client_id = ?`, clientID,
	).Scan(&activeRelayCount); err != nil {
		return Client{}, fmt.Errorf("count active client user relays: %w", err)
	}
	if activeRelayCount != 0 {
		return Client{}, ErrClientRelayPortsActive
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM client_relay_ports WHERE client_id = ?`, clientID); err != nil {
		return Client{}, fmt.Errorf("release client relay ports: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second)
	if err := reserveClientRelayPorts(ctx, tx, clientID, serverID, count, now); err != nil {
		return Client{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE clients SET updated_at = ? WHERE id = ?`, now.Unix(), clientID); err != nil {
		return Client{}, fmt.Errorf("touch client relay port allocation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Client{}, fmt.Errorf("commit client relay port update: %w", err)
	}
	return s.GetClient(ctx, clientID)
}
