package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
	"golang.org/x/crypto/bcrypt"
)

const (
	InvitationLifetime = 24 * time.Hour
	SessionLifetime    = 7 * 24 * time.Hour
	RoleAdmin          = "admin"
	RoleVIP            = "vip"
)

var (
	ErrAlreadyInitialized = errors.New("panel is already initialized")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidInvitation  = errors.New("invalid or expired invitation")
	ErrInvitationNotFound = errors.New("invitation not found")
	ErrInvalidUsername    = errors.New("username must be 3-64 characters using letters, numbers, dot, underscore, or hyphen")
	ErrInvalidPassword    = errors.New("password must be 10-72 bytes")
	ErrUsernameTaken      = errors.New("username is already in use")
	ErrUnauthenticated    = errors.New("authentication required")

	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
)

type User struct {
	ID        int64
	Username  string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Invitation struct {
	ID                int64
	CreatedBy         int64
	CreatedByUsername string
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

type CreatedInvitation struct {
	Invitation
	Token string
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) NeedsInitialization(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count == 0, nil
}

func (s *Service) Initialize(ctx context.Context, username, password string) (User, error) {
	username, passwordHash, err := prepareCredentials(username, password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin initialization: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if count != 0 {
		return User{}, ErrAlreadyInitialized
	}

	now := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, passwordHash, RoleAdmin, now.Unix(), now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("create first user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read first user id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit initialization: %w", err)
	}

	return User{ID: id, Username: username, Role: RoleAdmin, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	var user User
	var passwordHash string
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &passwordHash, &user.Role, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}

	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Service) CreateSession(ctx context.Context, userID int64) (string, time.Time, error) {
	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(SessionLifetime)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin session: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("remove expired sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		userID, tokenHash, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit session: %w", err)
	}
	return tokenValue, expiresAt, nil
}

func (s *Service) Authenticate(ctx context.Context, tokenValue string) (User, error) {
	if tokenValue == "" {
		return User{}, ErrUnauthenticated
	}
	var user User
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.role, users.created_at, users.updated_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ?`,
		token.Hash(tokenValue), s.now().UTC().Unix(),
	).Scan(&user.ID, &user.Username, &user.Role, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate session: %w", err)
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Service) Logout(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, token.Hash(tokenValue)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Service) CreateInvitation(ctx context.Context, createdBy int64) (CreatedInvitation, error) {
	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedInvitation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(InvitationLifetime)
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_invitations (token_hash, created_by, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenHash, createdBy, expiresAt.Unix(), now.Unix(),
	)
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("create invitation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("read invitation id: %w", err)
	}
	return CreatedInvitation{
		Invitation: Invitation{ID: id, CreatedBy: createdBy, ExpiresAt: expiresAt, CreatedAt: now},
		Token:      tokenValue,
	}, nil
}

func (s *Service) ListActiveInvitations(ctx context.Context) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT invitations.id, invitations.created_by, users.username,
		       invitations.expires_at, invitations.created_at
		FROM admin_invitations AS invitations
		JOIN users ON users.id = invitations.created_by
		WHERE invitations.used_at IS NULL AND invitations.expires_at > ?
		ORDER BY invitations.created_at DESC`, s.now().UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()

	invitations := make([]Invitation, 0)
	for rows.Next() {
		var invitation Invitation
		var expiresAt, createdAt int64
		if err := rows.Scan(
			&invitation.ID,
			&invitation.CreatedBy,
			&invitation.CreatedByUsername,
			&expiresAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		invitation.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		invitation.CreatedAt = time.Unix(createdAt, 0).UTC()
		invitations = append(invitations, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}
	return invitations, nil
}

func (s *Service) RevokeInvitation(ctx context.Context, invitationID int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_invitations WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		invitationID, s.now().UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked invitation count: %w", err)
	}
	if count == 0 {
		return ErrInvitationNotFound
	}
	return nil
}

func (s *Service) RegisterWithInvitation(ctx context.Context, tokenValue, username, password string) (User, error) {
	if tokenValue == "" {
		return User{}, ErrInvalidInvitation
	}
	username, passwordHash, err := prepareCredentials(username, password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin invited registration: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second)
	var invitationID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM admin_invitations WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(tokenValue), now.Unix(),
	).Scan(&invitationID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidInvitation
	}
	if err != nil {
		return User{}, fmt.Errorf("find invitation: %w", err)
	}

	var existing int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE username = ?`, username).Scan(&existing)
	if err == nil {
		return User{}, ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("check username: %w", err)
	}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, passwordHash, RoleVIP, now.Unix(), now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("create invited user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read invited user id: %w", err)
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE admin_invitations SET used_at = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), invitationID, now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("use invitation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read used invitation count: %w", err)
	}
	if count != 1 {
		return User{}, ErrInvalidInvitation
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit invited registration: %w", err)
	}

	return User{ID: userID, Username: username, Role: RoleVIP, CreatedAt: now, UpdatedAt: now}, nil
}

func prepareCredentials(username, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return "", "", ErrInvalidUsername
	}
	if len(password) < 10 || len(password) > 72 {
		return "", "", ErrInvalidPassword
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("hash password: %w", err)
	}
	return username, string(passwordHash), nil
}
