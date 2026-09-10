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
	StatusOnline       = "online"
	StatusOffline      = "offline"
	EnrollmentLifetime = 24 * time.Hour
	maxNameLength      = 100
)

var (
	ErrInvalidName         = errors.New("server name must be 1-100 characters")
	ErrNotFound            = errors.New("server not found")
	ErrInvalidEnrollment   = errors.New("invalid, used, or expired enrollment token")
	ErrInvalidAgentVersion = errors.New("agent version must be 1-64 characters")
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

type RegisteredAgent struct {
	ID       int64
	ServerID int64
	Token    string
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

func (s *Service) RegisterAgent(
	ctx context.Context,
	enrollmentToken string,
	agentVersion string,
) (RegisteredAgent, error) {
	if enrollmentToken == "" {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	agentVersion = strings.TrimSpace(agentVersion)
	if agentVersion == "" || utf8.RuneCountInString(agentVersion) > 64 {
		return RegisteredAgent{}, ErrInvalidAgentVersion
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("begin agent registration: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second)
	var enrollmentID, serverID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, server_id FROM agent_enrollments
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(enrollmentToken), now.Unix(),
	).Scan(&enrollmentID, &serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("find agent enrollment: %w", err)
	}

	agentToken, agentTokenHash, err := token.New()
	if err != nil {
		return RegisteredAgent{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO agents
		 (server_id, token_hash, version, registered_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, agentTokenHash, agentVersion, now.Unix(), now.Unix(), now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("create agent: %w", err)
	}
	agentID, err := result.LastInsertId()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read agent id: %w", err)
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET used_at = ?
		 WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), enrollmentID, now.Unix(),
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("use agent enrollment: %w", err)
	}
	usedCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read used enrollment count: %w", err)
	}
	if usedCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ?`,
		StatusOffline, now.Unix(), serverID,
	)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("update registered server: %w", err)
	}
	serverCount, err := result.RowsAffected()
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("read updated server count: %w", err)
	}
	if serverCount != 1 {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}

	if err := tx.Commit(); err != nil {
		return RegisteredAgent{}, fmt.Errorf("commit agent registration: %w", err)
	}
	return RegisteredAgent{
		ID:       agentID,
		ServerID: serverID,
		Token:    agentToken,
	}, nil
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
