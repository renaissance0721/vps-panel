package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

const (
	StatusPending      = "pending"
	EnrollmentLifetime = 24 * time.Hour
	maxNameLength      = 100
)

var (
	ErrInvalidName = errors.New("server name must be 1-100 characters")
	ErrNotFound    = errors.New("server not found")
)

type Server struct {
	ID        int64
	Name      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreatedServer struct {
	Server
	EnrollmentToken     string
	EnrollmentExpiresAt time.Time
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) Create(ctx context.Context, name string) (CreatedServer, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return CreatedServer{}, ErrInvalidName
	}

	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedServer{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(EnrollmentLifetime)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin server creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO servers (name, status, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		name, StatusPending, now.Unix(), now.Unix(),
	)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("create server: %w", err)
	}
	serverID, err := result.LastInsertId()
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_enrollments (server_id, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?)`,
		serverID, tokenHash, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return CreatedServer{}, fmt.Errorf("create agent enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit server creation: %w", err)
	}

	return CreatedServer{
		Server: Server{
			ID:        serverID,
			Name:      name,
			Status:    StatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		},
		EnrollmentToken:     tokenValue,
		EnrollmentExpiresAt: expiresAt,
	}, nil
}

func (s *Service) List(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, status, created_at, updated_at
		 FROM servers ORDER BY created_at DESC, id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	servers := make([]Server, 0)
	for rows.Next() {
		value, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		servers = append(servers, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate servers: %w", err)
	}
	return servers, nil
}

func (s *Service) Get(ctx context.Context, id int64) (Server, error) {
	value, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT id, name, status, created_at, updated_at FROM servers WHERE id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("get server: %w", err)
	}
	return value, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (Server, error) {
	var value Server
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &value.Status, &createdAt, &updatedAt); err != nil {
		return Server{}, err
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
