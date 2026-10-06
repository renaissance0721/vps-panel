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

type createInvitationRequest struct {
	Role string `json:"role"`
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
	Role              string    `json:"role"`
	ExpiresAt         time.Time `json:"expires_at"`
	CreatedAt         time.Time `json:"created_at"`
	Token             string    `json:"token,omitempty"`
}

type accessUserResponse struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Role          string `json:"role"`
	EmailMasked   string `json:"email_masked,omitempty"`
	EmailVerified bool   `json:"email_verified"`
}

func toAccessUserResponse(user auth.User) accessUserResponse {
	return accessUserResponse{
		ID: user.ID, Username: user.Username, Role: user.Role,
		EmailMasked: maskEmailIfPresent(user.Email), EmailVerified: user.Email != "" && user.EmailVerifiedAt != nil,
	}
}

func maskEmailIfPresent(value string) string {
	if value == "" {
		return ""
	}
	return maskEmail(value)
}

func toUserResponse(user auth.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt}
}

func toInvitationResponse(invitation auth.Invitation) invitationResponse {
	return invitationResponse{
		ID:                invitation.ID,
		CreatedBy:         invitation.CreatedBy,
		CreatedByUsername: invitation.CreatedByUsername,
		Role:              invitation.Role,
		ExpiresAt:         invitation.ExpiresAt,
		CreatedAt:         invitation.CreatedAt,
	}
}
