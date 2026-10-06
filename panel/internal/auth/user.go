package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"golang.org/x/crypto/bcrypt"
)

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

// NormalizeLoginIdentifier preserves username case and canonicalizes email keys.
func NormalizeLoginIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if strings.Contains(identifier, "@") {
		return strings.ToLower(identifier)
	}
	return identifier
}

func loginIdentifierLookup(identifier string) (string, string, bool) {
	identifier = NormalizeLoginIdentifier(identifier)
	if strings.Contains(identifier, "@") {
		address, err := mailservice.NormalizeMailbox(identifier)
		return "lower(email) = ? AND email_verified_at IS NOT NULL", address, err == nil
	}
	return "username = ?", identifier, true
}

func (s *Service) Login(ctx context.Context, identifier, password string) (User, error) {
	condition, identifier, valid := loginIdentifierLookup(identifier)
	if !valid {
		return User{}, ErrInvalidCredentials
	}
	var user User
	var passwordHash string
	var email sql.NullString
	var emailVerifiedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, email, email_verified_at, created_at, updated_at FROM users WHERE `+condition,
		identifier,
	).Scan(&user.ID, &user.Username, &passwordHash, &user.Role, &email, &emailVerifiedAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}

	setUserOptionalEmail(&user, email, emailVerifiedAt)
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, role, email, email_verified_at, created_at, updated_at FROM users ORDER BY username, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		var email sql.NullString
		var emailVerifiedAt sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &email, &emailVerifiedAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		setUserOptionalEmail(&user, email, emailVerifiedAt)
		user.CreatedAt = time.Unix(createdAt, 0).UTC()
		user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	var user User
	var email sql.NullString
	var emailVerifiedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, email, email_verified_at, created_at, updated_at FROM users WHERE id = ?`, id,
	).Scan(&user.ID, &user.Username, &user.Role, &email, &emailVerifiedAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	setUserOptionalEmail(&user, email, emailVerifiedAt)
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Service) RenameUser(ctx context.Context, id int64, currentPassword, username string) (User, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return User{}, ErrInvalidUsername
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin user rename: %w", err)
	}
	defer tx.Rollback()

	var user User
	var passwordHash string
	var email sql.NullString
	var emailVerifiedAt sql.NullInt64
	var createdAt, updatedAt int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, email, email_verified_at, created_at, updated_at FROM users WHERE id = ?`, id,
	).Scan(&user.ID, &user.Username, &passwordHash, &user.Role, &email, &emailVerifiedAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user for rename: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(currentPassword)) != nil {
		return User{}, ErrInvalidCredentials
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? AND id != ?`, username, id).Scan(&existing)
	if err == nil {
		return User{}, ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("check renamed username: %w", err)
	}
	now := s.now().UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`,
		username, now.Unix(), id); err != nil {
		return User{}, fmt.Errorf("rename user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user rename: %w", err)
	}
	user.Username = username
	setUserOptionalEmail(&user, email, emailVerifiedAt)
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = now
	return user, nil
}

func setUserOptionalEmail(user *User, email sql.NullString, verifiedAt sql.NullInt64) {
	if email.Valid {
		user.Email = email.String
	}
	if verifiedAt.Valid {
		value := time.Unix(verifiedAt.Int64, 0).UTC()
		user.EmailVerifiedAt = &value
	}
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	var currentHash string
	if err := tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&currentHash); errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("read current password: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(currentPassword)) != nil {
		return ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(newPassword)) == nil {
		return ErrPasswordUnchanged
	}
	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, s.now().UTC().Truncate(time.Second).Unix(), userID)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return fmt.Errorf("read changed password count: %w", err)
		}
		return ErrUserNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("invalidate changed password sessions: %w", err)
	}
	if err := invalidatePasswordResetTokensTx(ctx, tx, userID, s.now().UTC().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func (s *Service) DeleteUser(ctx context.Context, userID int64) ([]proxystore.Mutation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin user deletion: %w", err)
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read user for deletion: %w", err)
	}
	if role == RoleAdmin {
		return nil, ErrCannotDeleteAdmin
	}

	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT affected.server_id FROM (
		SELECT proxies.server_id AS server_id FROM clients
		JOIN proxies ON proxies.id = clients.proxy_id WHERE clients.assigned_user_id = ?
		UNION
		SELECT relays.server_id AS server_id FROM relays WHERE relays.owner_user_id = ?
			OR relays.source_client_id IN (SELECT id FROM clients WHERE assigned_user_id = ?)
			OR relays.target_client_id IN (SELECT id FROM clients WHERE assigned_user_id = ?)
			OR relays.target_landing_id IN (SELECT id FROM landing_nodes WHERE owner_user_id = ?)
	) AS affected
	JOIN servers ON servers.id = affected.server_id
	WHERE servers.archived_at IS NULL AND servers.decommission_status = ''
	ORDER BY affected.server_id`, userID, userID, userID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("list user deletion servers: %w", err)
	}
	serverIDs := make([]int64, 0)
	for rows.Next() {
		var serverID int64
		if err := rows.Scan(&serverID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan user deletion server: %w", err)
		}
		serverIDs = append(serverIDs, serverID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate user deletion servers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close user deletion servers: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM relays WHERE owner_user_id = ?
		OR source_client_id IN (SELECT id FROM clients WHERE assigned_user_id = ?)
		OR target_client_id IN (SELECT id FROM clients WHERE assigned_user_id = ?)
		OR target_landing_id IN (SELECT id FROM landing_nodes WHERE owner_user_id = ?)`,
		userID, userID, userID, userID); err != nil {
		return nil, fmt.Errorf("delete user relays: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE assigned_user_id = ?`, userID); err != nil {
		return nil, fmt.Errorf("delete user clients: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM landing_nodes WHERE owner_user_id = ?`, userID); err != nil {
		return nil, fmt.Errorf("delete user landing nodes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_invitations WHERE created_by = ?`, userID); err != nil {
		return nil, fmt.Errorf("delete user invitations: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("delete user: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return nil, fmt.Errorf("read deleted user count: %w", err)
		}
		return nil, ErrUserNotFound
	}
	mutations, err := proxystore.BumpServerVersionsTx(ctx, tx, serverIDs, s.now().UTC().Truncate(time.Second))
	if err != nil {
		return nil, fmt.Errorf("bump user deletion server versions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user deletion: %w", err)
	}
	return mutations, nil
}

func prepareCredentials(username, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return "", "", ErrInvalidUsername
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return "", "", err
	}
	return username, passwordHash, nil
}

func validatePassword(password string) error {
	if len(password) < 6 || len(password) > 72 {
		return ErrInvalidPassword
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(passwordHash), nil
}
