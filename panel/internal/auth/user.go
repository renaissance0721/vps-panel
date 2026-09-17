package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, role, created_at, updated_at FROM users ORDER BY username, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		var createdAt, updatedAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		user.CreatedAt = time.Unix(createdAt, 0).UTC()
		user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
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
