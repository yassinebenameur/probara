package models

import (
	"time"

	"github.com/google/uuid"
)

// AdminSession is one sign-in of a user (a session family): it lasts across
// refresh-token rotations until it expires, the user signs out, or it is
// revoked.
type AdminSession struct {
	ID uuid.UUID `json:"id"`
	// AuthMethod is "password" or "oidc"; empty for sign-ins that predate
	// session tracking.
	AuthMethod string `json:"auth_method,omitempty"`
	// SecondFactor is how a two-factor sign-in passed its second step:
	// "totp" or "recovery_code"; empty without one.
	SecondFactor string    `json:"second_factor,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// LastUsedAt moves at sign-in and at each token refresh (about every
	// access-token lifetime while the session is in use), not per request.
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	// IP and UserAgent are those of the last sign-in or refresh; Client is a
	// short summary of the user agent ("Firefox on macOS").
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Client    string `json:"client,omitempty"`
	// Current marks the session the request itself was made with.
	Current bool `json:"current"`
}

// AdminSessionListResponse is a user's live sessions, most recently used
// first.
type AdminSessionListResponse struct {
	Items []AdminSession `json:"items"`
}

// RevokeSessionsResponse reports how many sessions a bulk revoke ended.
type RevokeSessionsResponse struct {
	Revoked int `json:"revoked"`
}

// UnlockUserResponse reports whether an unlock lifted an active lock.
type UnlockUserResponse struct {
	WasLocked bool `json:"was_locked"`
}
