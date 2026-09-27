package auth

import (
	"errors"
	"regexp"
	"time"
)

const (
	InvitationLifetime = 24 * time.Hour
	SessionLifetime    = 7 * 24 * time.Hour
	RoleAdmin          = "admin"
	RoleVIP            = "vip"
	RoleUser           = "user"
)

var (
	ErrAlreadyInitialized      = errors.New("panel is already initialized")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrInvalidInvitation       = errors.New("invalid or expired invitation")
	ErrInvitationNotFound      = errors.New("invitation not found")
	ErrInvalidInvitationRole   = errors.New("invitation role must be vip or user")
	ErrInvalidUsername         = errors.New("username must be 3-64 characters using letters, numbers, dot, underscore, or hyphen")
	ErrInvalidPassword         = errors.New("password must be 10-72 bytes")
	ErrUsernameTaken           = errors.New("username is already in use")
	ErrUnauthenticated         = errors.New("authentication required")
	ErrPasswordUnchanged       = errors.New("new password must differ from current password")
	ErrPasswordRequestPending  = errors.New("a password change request is already pending")
	ErrPasswordRequestNotFound = errors.New("password change request not found or already reviewed")

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
	Role              string
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

type CreatedInvitation struct {
	Invitation
	Token string
}

type PasswordChangeRequest struct {
	ID         int64
	UserID     int64
	Username   string
	Status     string
	CreatedAt  time.Time
	ReviewedBy *int64
	ReviewedAt *time.Time
}
