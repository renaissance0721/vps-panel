package listorder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

type Kind string

const (
	Servers Kind = "servers"
	Proxies Kind = "proxies"
	Relays  Kind = "relays"
)

var (
	ErrNotFound         = errors.New("resource not found")
	ErrInvalidDirection = errors.New("invalid reorder direction")
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func tableAndColumn(kind Kind) (string, string) {
	switch kind {
	case Servers:
		return "user_server_order", "server_id"
	case Proxies:
		return "user_proxy_order", "proxy_id"
	case Relays:
		return "user_relay_order", "relay_id"
	default:
		panic("unknown list order kind")
	}
}

func readPositions(rows *sql.Rows) (map[int64]int64, []int64, error) {
	defer rows.Close()
	positions := make(map[int64]int64)
	stored := make([]int64, 0)
	for rows.Next() {
		var id, position int64
		if err := rows.Scan(&id, &position); err != nil {
			return nil, nil, err
		}
		positions[id] = position
		stored = append(stored, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return positions, stored, nil
}

// sortVisible keeps the existing newest-first list until a user changes it.
// New resources without an order row appear above previously ordered resources.
func sortVisible(defaultIDs []int64, positions map[int64]int64) []int64 {
	ordered := append([]int64(nil), defaultIDs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, hasLeft := positions[ordered[i]]
		right, hasRight := positions[ordered[j]]
		if hasLeft != hasRight {
			return !hasLeft
		}
		return hasLeft && left < right
	})
	return ordered
}

func (s *Store) Sort(ctx context.Context, userID int64, kind Kind, defaultIDs []int64) ([]int64, error) {
	table, column := tableAndColumn(kind)
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+column+", position FROM "+table+" WHERE user_id = ? ORDER BY position, "+column, userID)
	if err != nil {
		return nil, fmt.Errorf("read list order: %w", err)
	}
	positions, _, err := readPositions(rows)
	if err != nil {
		return nil, fmt.Errorf("scan list order: %w", err)
	}
	return sortVisible(defaultIDs, positions), nil
}

func visibleQuery(kind Kind, archived bool) string {
	switch kind {
	case Servers:
		archive := "IS NULL"
		if archived {
			archive = "IS NOT NULL"
		}
		return `SELECT servers.id FROM servers
			WHERE servers.archived_at ` + archive + ` AND
			(servers.visibility = 'public' OR EXISTS (
				SELECT 1 FROM server_access WHERE server_id = servers.id AND user_id = ?
		)) ORDER BY servers.created_at DESC, servers.id DESC`
	case Proxies:
		return `SELECT proxies.id FROM proxies JOIN servers ON servers.id = proxies.server_id
			WHERE servers.archived_at IS NULL AND
			(servers.visibility = 'public' OR EXISTS (
				SELECT 1 FROM server_access WHERE server_id = servers.id AND user_id = ?
		)) ORDER BY proxies.created_at DESC, proxies.id DESC`
	case Relays:
		return `SELECT relays.id FROM relays
			JOIN servers AS source ON source.id = relays.server_id
			LEFT JOIN proxies AS target_proxy ON target_proxy.id = relays.target_proxy_id
			LEFT JOIN servers AS target ON target.id = target_proxy.server_id
			WHERE source.archived_at IS NULL AND
			(source.visibility = 'public' OR EXISTS (
				SELECT 1 FROM server_access WHERE server_id = source.id AND user_id = ?
			)) AND (relays.target_type != 'proxy' OR
				(target_proxy.id IS NOT NULL AND target.archived_at IS NULL AND
				(target.visibility = 'public' OR EXISTS (
					SELECT 1 FROM server_access WHERE server_id = target.id AND user_id = ?
				)))) ORDER BY relays.created_at DESC, relays.id DESC`
	default:
		panic("unknown list order kind")
	}
}

func (s *Store) Move(ctx context.Context, userID int64, kind Kind, archived bool, resourceID int64, direction string) error {
	if direction != "up" && direction != "down" {
		return ErrInvalidDirection
	}
	if kind != Servers && archived {
		return ErrNotFound
	}
	table, column := tableAndColumn(kind)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin list reorder: %w", err)
	}
	defer tx.Rollback()
	query := visibleQuery(kind, archived)
	arguments := []any{userID}
	if kind == Relays {
		arguments = append(arguments, userID)
	}
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("list visible resources for reorder: %w", err)
	}
	visible := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan visible resource: %w", err)
		}
		visible = append(visible, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate visible resources: %w", err)
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx,
		"SELECT "+column+", position FROM "+table+" WHERE user_id = ? ORDER BY position, "+column, userID)
	if err != nil {
		return fmt.Errorf("read order for reorder: %w", err)
	}
	positions, stored, err := readPositions(rows)
	if err != nil {
		return fmt.Errorf("scan order for reorder: %w", err)
	}
	visible = sortVisible(visible, positions)
	index := -1
	for i, id := range visible {
		if id == resourceID {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrNotFound
	}
	neighbor := index - 1
	if direction == "down" {
		neighbor = index + 1
	}
	if neighbor < 0 || neighbor >= len(visible) {
		return nil
	}
	visible[index], visible[neighbor] = visible[neighbor], visible[index]
	seen := make(map[int64]bool, len(visible))
	for _, id := range visible {
		seen[id] = true
	}
	// Preserve inaccessible and archived preferences, without letting them block
	// adjacent moves in the current visible list.
	for _, id := range stored {
		if !seen[id] {
			visible = append(visible, id)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("clear old list positions: %w", err)
	}
	for position, id := range visible {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO "+table+" (user_id, "+column+", position) VALUES (?, ?, ?)",
			userID, id, position+1); err != nil {
			return fmt.Errorf("save list position: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit list reorder: %w", err)
	}
	return nil
}
