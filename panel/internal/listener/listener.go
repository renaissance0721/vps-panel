package listener

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	NetworkTCP  = "tcp"
	NetworkUDP  = "udp"
	NetworkBoth = "both"

	ResourceProxy       = "proxy"
	ResourceRelay       = "relay"
	ResourceClientRelay = "client_relay"
)

var ErrConflict = errors.New("listener reservation conflict")

type Reservation struct {
	ID           int64
	ServerID     int64
	ResourceType string
	ResourceID   int64
	Port         int
	Network      string
}

type AvailabilityOptions struct {
	ExcludeResourceType string
	ExcludeResourceID   int64
	AllowedClientID     *int64
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ListenerAvailable(ctx context.Context, query queryer, serverID int64, port int, network string, options AvailabilityOptions) error {
	network, err := NormalizeNetwork(network)
	if err != nil {
		return err
	}
	rows, err := query.QueryContext(ctx, `SELECT reservations.id, reservations.resource_type,
		reservations.resource_id, reservations.network,
		CASE WHEN reservations.resource_type = 'client_relay' THEN reservations.resource_id END
		FROM server_listener_reservations AS reservations
		WHERE reservations.server_id = ? AND reservations.port = ?`, serverID, port)
	if err != nil {
		return fmt.Errorf("list listener reservations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var reservation Reservation
		var clientID sql.NullInt64
		if err := rows.Scan(&reservation.ID, &reservation.ResourceType, &reservation.ResourceID, &reservation.Network, &clientID); err != nil {
			return fmt.Errorf("scan listener reservation: %w", err)
		}
		if reservation.ResourceType == options.ExcludeResourceType && reservation.ResourceID == options.ExcludeResourceID {
			continue
		}
		if options.AllowedClientID != nil && reservation.ResourceType == ResourceClientRelay &&
			clientID.Valid && clientID.Int64 == *options.AllowedClientID {
			continue
		}
		if NetworksOverlap(network, reservation.Network) {
			return ErrConflict
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate listener reservations: %w", err)
	}
	return nil
}

func ListOccupiedListeners(ctx context.Context, query queryer, serverID int64, start, end int) ([]Reservation, error) {
	rows, err := query.QueryContext(ctx, `SELECT id, server_id, resource_type, resource_id, port, network
		FROM server_listener_reservations
		WHERE server_id = ? AND port BETWEEN ? AND ? ORDER BY port, id`, serverID, start, end)
	if err != nil {
		return nil, fmt.Errorf("list occupied listeners: %w", err)
	}
	defer rows.Close()
	values := make([]Reservation, 0)
	for rows.Next() {
		var value Reservation
		if err := rows.Scan(&value.ID, &value.ServerID, &value.ResourceType, &value.ResourceID, &value.Port, &value.Network); err != nil {
			return nil, fmt.Errorf("scan occupied listener: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate occupied listeners: %w", err)
	}
	return values, nil
}

func ReserveListener(ctx context.Context, query executor, value Reservation, createdAt int64) error {
	network, err := NormalizeNetwork(value.Network)
	if err != nil {
		return err
	}
	_, err = query.ExecContext(ctx, `INSERT INTO server_listener_reservations
		(server_id, resource_type, resource_id, port, network, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource_type, resource_id, port) DO UPDATE SET
			server_id = excluded.server_id, port = excluded.port, network = excluded.network`,
		value.ServerID, value.ResourceType, value.ResourceID, value.Port, network, createdAt)
	if IsConflict(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("reserve listener: %w", err)
	}
	return nil
}

func ReleaseListener(ctx context.Context, query executor, resourceType string, resourceID int64) error {
	if _, err := query.ExecContext(ctx,
		`DELETE FROM server_listener_reservations WHERE resource_type = ? AND resource_id = ?`,
		resourceType, resourceID); err != nil {
		return fmt.Errorf("release listener: %w", err)
	}
	return nil
}

func NormalizeNetwork(value string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "")) {
	case NetworkTCP:
		return NetworkTCP, nil
	case NetworkUDP:
		return NetworkUDP, nil
	case NetworkBoth, "tcp,udp", "udp,tcp":
		return NetworkBoth, nil
	default:
		return "", fmt.Errorf("invalid listener network %q", value)
	}
}

func NetworksOverlap(left, right string) bool {
	left, leftErr := NormalizeNetwork(left)
	right, rightErr := NormalizeNetwork(right)
	return leftErr == nil && rightErr == nil && (left == NetworkBoth || right == NetworkBoth || left == right)
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict) || err != nil && strings.Contains(err.Error(), ErrConflict.Error())
}
