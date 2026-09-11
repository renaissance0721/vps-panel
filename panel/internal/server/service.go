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
	PurposeInitial     = "initial"
	PurposeRebind      = "rebind"
	EnrollmentLifetime = 24 * time.Hour
	maxNameLength      = 100
)

var (
	ErrInvalidName         = errors.New("server name must be 1-100 characters")
	ErrNotFound            = errors.New("server not found")
	ErrInvalidEnrollment   = errors.New("invalid, used, or expired enrollment token")
	ErrInvalidAgentVersion = errors.New("agent version must be 1-64 characters")
	ErrInvalidAgentToken   = errors.New("invalid agent token")
	ErrArchived            = errors.New("server is archived")
	ErrInitialConfigExists = errors.New("initial enrollment cannot replace an existing Agent config")
)

type Server struct {
	ID         int64
	Name       string
	Status     string
	ArchivedAt *time.Time
	LastSeenAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
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

type Agent struct {
	ID       int64
	ServerID int64
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
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		serverID, tokenHash, PurposeInitial, expiresAt.Unix(), now.Unix(),
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

func (s *Service) CreateEnrollment(ctx context.Context, id int64) (CreatedServer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedServer{}, fmt.Errorf("begin Agent enrollment creation: %w", err)
	}
	defer tx.Rollback()

	value, err := scanServer(tx.QueryRowContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.archived_at,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 servers.created_at, servers.updated_at FROM servers WHERE servers.id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return CreatedServer{}, ErrNotFound
	}
	if err != nil {
		return CreatedServer{}, fmt.Errorf("read server for Agent enrollment: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return CreatedServer{}, fmt.Errorf("remove previous Agent enrollment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return CreatedServer{}, fmt.Errorf("revoke previous Agent: %w", err)
	}

	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedServer{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(EnrollmentLifetime)
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ?`,
		StatusPending, now.Unix(), id,
	); err != nil {
		return CreatedServer{}, fmt.Errorf("prepare server Agent enrollment: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, tokenHash, PurposeRebind, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return CreatedServer{}, fmt.Errorf("create Agent enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreatedServer{}, fmt.Errorf("commit Agent enrollment creation: %w", err)
	}
	value.Status = StatusPending
	value.LastSeenAt = nil
	value.UpdatedAt = now
	return CreatedServer{
		Server:              value,
		EnrollmentToken:     tokenValue,
		EnrollmentExpiresAt: expiresAt,
	}, nil
}

func (s *Service) List(ctx context.Context) ([]Server, error) {
	return s.list(ctx, false)
}

func (s *Service) ListArchived(ctx context.Context) ([]Server, error) {
	return s.list(ctx, true)
}

func (s *Service) list(ctx context.Context, archived bool) ([]Server, error) {
	archiveCondition := "archived_at IS NULL"
	if archived {
		archiveCondition = "archived_at IS NOT NULL"
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT servers.id, servers.name, servers.status, servers.archived_at,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 servers.created_at, servers.updated_at
		 FROM servers WHERE `+archiveCondition+` ORDER BY servers.created_at DESC, servers.id DESC`,
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
		`SELECT servers.id, servers.name, servers.status, servers.archived_at,
		 (SELECT last_seen_at FROM agents WHERE agents.server_id = servers.id),
		 servers.created_at, servers.updated_at
		 FROM servers WHERE servers.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("get server: %w", err)
	}
	return value, nil
}

func (s *Service) Archive(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server archive: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		StatusOffline, now, now, id,
	)
	if err != nil {
		return fmt.Errorf("archive server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read archived server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return fmt.Errorf("revoke archived server agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return fmt.Errorf("remove unused agent enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server archive: %w", err)
	}
	return nil
}

func (s *Service) PermanentlyDelete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM servers WHERE id = ? AND archived_at IS NOT NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("permanently delete server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read permanently deleted server count: %w", err)
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
	existingConfig bool,
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
	var purpose string
	err = tx.QueryRowContext(ctx,
		`SELECT id, server_id, purpose FROM agent_enrollments
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(enrollmentToken), now.Unix(),
	).Scan(&enrollmentID, &serverID, &purpose)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredAgent{}, ErrInvalidEnrollment
	}
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("find agent enrollment: %w", err)
	}
	if purpose == PurposeInitial && existingConfig {
		return RegisteredAgent{}, ErrInitialConfigExists
	}

	agentToken, agentTokenHash, err := token.New()
	if err != nil {
		return RegisteredAgent{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, serverID); err != nil {
		return RegisteredAgent{}, fmt.Errorf("replace previous Agent: %w", err)
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
		`UPDATE servers SET status = ?, archived_at = NULL, updated_at = ? WHERE id = ?`,
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

func (s *Service) AuthenticateAgent(ctx context.Context, agentToken string) (Agent, error) {
	if agentToken == "" {
		return Agent{}, ErrInvalidAgentToken
	}

	var agent Agent
	err := s.db.QueryRowContext(ctx,
		`SELECT agents.id, agents.server_id FROM agents
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.token_hash = ? AND servers.archived_at IS NULL`, token.Hash(agentToken),
	).Scan(&agent.ID, &agent.ServerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrInvalidAgentToken
	}
	if err != nil {
		return Agent{}, fmt.Errorf("authenticate agent: %w", err)
	}
	return agent, nil
}

func (s *Service) SetAgentOnline(ctx context.Context, serverID int64) error {
	return s.setStatus(ctx, serverID, StatusOnline)
}

func (s *Service) SetAgentConnected(ctx context.Context, agentID, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent connection update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, updated_at = ? WHERE id = ? AND server_id = ?`,
		now, now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected Agent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		StatusOnline, now, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected server: %w", err)
	}
	count, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected server count: %w", err)
	}
	if count != 1 {
		return ErrArchived
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent connection update: %w", err)
	}
	return nil
}

func (s *Service) TouchAgent(ctx context.Context, agentID, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, updated_at = ? WHERE id = ? AND server_id = ?`,
		now, now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update Agent last seen: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}
	return nil
}

func (s *Service) SetAgentOffline(ctx context.Context, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND status = ?`,
		StatusOffline, now, serverID, StatusOnline,
	)
	if err != nil {
		return fmt.Errorf("set server offline: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read offline server count: %w", err)
	}
	if count == 1 {
		return nil
	}
	var existingID int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE id = ?`, serverID,
	).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read disconnected server state: %w", err)
	}
	return nil
}

func (s *Service) ResetOnline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE status = ? AND archived_at IS NULL`,
		StatusOffline, s.now().UTC().Truncate(time.Second).Unix(), StatusOnline,
	)
	if err != nil {
		return fmt.Errorf("reset online servers: %w", err)
	}
	return nil
}

func (s *Service) setStatus(ctx context.Context, serverID int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		status, s.now().UTC().Truncate(time.Second).Unix(), serverID,
	)
	if err != nil {
		return fmt.Errorf("update server status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated server count: %w", err)
	}
	if count != 1 {
		var archivedAt sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			`SELECT archived_at FROM servers WHERE id = ?`, serverID,
		).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read server lifecycle state: %w", err)
		}
		if archivedAt.Valid {
			return ErrArchived
		}
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (Server, error) {
	var value Server
	var archivedAt, lastSeenAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &value.Status, &archivedAt, &lastSeenAt, &createdAt, &updatedAt); err != nil {
		return Server{}, err
	}
	if archivedAt.Valid {
		archivedTime := time.Unix(archivedAt.Int64, 0).UTC()
		value.ArchivedAt = &archivedTime
	}
	if lastSeenAt.Valid {
		lastSeenTime := time.Unix(lastSeenAt.Int64, 0).UTC()
		value.LastSeenAt = &lastSeenTime
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
