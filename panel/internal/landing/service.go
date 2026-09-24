package landing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

type storedLanding struct {
	Landing
	URI string
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) List(ctx context.Context, userID int64) ([]Landing, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_user_id, name, visibility, protocol, host, port, created_at, updated_at
		 FROM landing_nodes WHERE owner_user_id = ? OR visibility = 'public'
		 ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list landings: %w", err)
	}
	defer rows.Close()
	values := make([]Landing, 0)
	for rows.Next() {
		value, err := scanLanding(rows, userID)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate landings: %w", err)
	}
	return values, nil
}

func (s *Service) Get(ctx context.Context, id, userID int64) (Landing, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_user_id, name, visibility, protocol, host, port, created_at, updated_at
		 FROM landing_nodes WHERE id = ? AND (owner_user_id = ? OR visibility = 'public')`, id, userID)
	value, err := scanLanding(row, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Landing{}, ErrNotFound
	}
	return value, err
}

func (s *Service) GetURI(ctx context.Context, id, userID int64) (string, error) {
	var uri string
	err := s.db.QueryRowContext(ctx,
		`SELECT uri FROM landing_nodes WHERE id = ? AND (owner_user_id = ? OR visibility = 'public')`, id, userID,
	).Scan(&uri)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read landing URI: %w", err)
	}
	return uri, nil
}

func (s *Service) Create(ctx context.Context, userID int64, input CreateInput) (Landing, error) {
	parsed, err := ParseURI(input.URI)
	if err != nil {
		return Landing{}, err
	}
	visibility, err := normalizeVisibility(input.Visibility, true)
	if err != nil {
		return Landing{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.TrimSpace(parsed.Fragment)
		if name == "" {
			if parsed.Protocol == ProtocolVLESS {
				name = "VLESS 落地"
			} else {
				name = "Shadowsocks 落地"
			}
		}
	}
	if err := validateName(name); err != nil {
		return Landing{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO landing_nodes
		 (owner_user_id, name, visibility, protocol, host, port, uri, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, name, visibility, parsed.Protocol, parsed.Host, parsed.Port, strings.TrimSpace(input.URI), now.Unix(), now.Unix())
	if err != nil {
		return Landing{}, fmt.Errorf("create landing: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Landing{}, fmt.Errorf("read landing id: %w", err)
	}
	return s.Get(ctx, id, userID)
}

func (s *Service) Update(ctx context.Context, id, userID int64, input UpdateInput) (Landing, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Landing{}, false, fmt.Errorf("begin landing update: %w", err)
	}
	defer tx.Rollback()
	current, err := getOwned(ctx, tx, id, userID)
	if err != nil {
		return Landing{}, false, err
	}
	name, visibility, uri := current.Name, current.Visibility, current.URI
	protocol, host, port := current.Protocol, current.Host, current.Port
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if err := validateName(name); err != nil {
			return Landing{}, false, err
		}
	}
	if input.Visibility != nil {
		visibility, err = normalizeVisibility(*input.Visibility, false)
		if err != nil {
			return Landing{}, false, err
		}
		if current.Visibility == VisibilityPublic && visibility == VisibilityPrivate {
			referenced, err := isReferenced(ctx, tx, id)
			if err != nil {
				return Landing{}, false, err
			}
			if referenced {
				return Landing{}, false, ErrReferencedByRelay
			}
		}
	}
	if input.URI != nil {
		parsed, err := ParseURI(*input.URI)
		if err != nil {
			return Landing{}, false, err
		}
		if parsed.Protocol != current.Protocol {
			return Landing{}, false, ErrImmutableProtocol
		}
		uri, protocol, host, port = strings.TrimSpace(*input.URI), parsed.Protocol, parsed.Host, parsed.Port
	}
	endpointChanged := host != current.Host || port != current.Port
	now := s.now().UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx,
		`UPDATE landing_nodes SET name = ?, visibility = ?, protocol = ?, host = ?, port = ?, uri = ?, updated_at = ?
		 WHERE id = ? AND owner_user_id = ?`,
		name, visibility, protocol, host, port, uri, now.Unix(), id, userID,
	); err != nil {
		return Landing{}, false, fmt.Errorf("update landing: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Landing{}, false, fmt.Errorf("commit landing update: %w", err)
	}
	updated, err := s.Get(ctx, id, userID)
	return updated, endpointChanged, err
}

func (s *Service) Delete(ctx context.Context, id, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin landing deletion: %w", err)
	}
	defer tx.Rollback()
	if _, err := getOwned(ctx, tx, id, userID); err != nil {
		return err
	}
	referenced, err := isReferenced(ctx, tx, id)
	if err != nil {
		return err
	}
	if referenced {
		return ErrReferencedByRelay
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM landing_nodes WHERE id = ? AND owner_user_id = ?`, id, userID); err != nil {
		return fmt.Errorf("delete landing: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit landing deletion: %w", err)
	}
	return nil
}

func getOwned(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id, userID int64) (storedLanding, error) {
	var value storedLanding
	var createdAt, updatedAt int64
	err := query.QueryRowContext(ctx,
		`SELECT id, owner_user_id, name, visibility, protocol, host, port, uri, created_at, updated_at
		 FROM landing_nodes WHERE id = ? AND owner_user_id = ?`, id, userID,
	).Scan(&value.ID, &value.OwnerUserID, &value.Name, &value.Visibility, &value.Protocol,
		&value.Host, &value.Port, &value.URI, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedLanding{}, ErrNotFound
	}
	if err != nil {
		return storedLanding{}, fmt.Errorf("read owned landing: %w", err)
	}
	value.OwnedByMe = true
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func isReferenced(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (bool, error) {
	var count int
	if err := query.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM relays WHERE target_type = 'landing' AND target_landing_id = ?`, id,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check landing relay references: %w", err)
	}
	return count != 0, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanLanding(row rowScanner, userID int64) (Landing, error) {
	var value Landing
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.OwnerUserID, &value.Name, &value.Visibility, &value.Protocol,
		&value.Host, &value.Port, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Landing{}, err
		}
		return Landing{}, fmt.Errorf("scan landing: %w", err)
	}
	value.OwnedByMe = value.OwnerUserID == userID
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func validateName(value string) error {
	if value == "" || utf8.RuneCountInString(value) > maxNameRunes {
		return ErrInvalidName
	}
	return nil
}

func normalizeVisibility(value string, allowDefault bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" && allowDefault {
		return VisibilityPrivate, nil
	}
	if value != VisibilityPrivate && value != VisibilityPublic {
		return "", ErrInvalidVisibility
	}
	return value, nil
}
