package api

import (
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registrationRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type invitationResponse struct {
	ID                int64     `json:"id"`
	CreatedBy         int64     `json:"created_by"`
	CreatedByUsername string    `json:"created_by_username"`
	ExpiresAt         time.Time `json:"expires_at"`
	CreatedAt         time.Time `json:"created_at"`
	Token             string    `json:"token,omitempty"`
}

type accessUserResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func toUserResponse(user auth.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt}
}

func toInvitationResponse(invitation auth.Invitation) invitationResponse {
	return invitationResponse{
		ID:                invitation.ID,
		CreatedBy:         invitation.CreatedBy,
		CreatedByUsername: invitation.CreatedByUsername,
		ExpiresAt:         invitation.ExpiresAt,
		CreatedAt:         invitation.CreatedAt,
	}
}
