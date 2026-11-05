package models

import (
	"time"

	"github.com/google/uuid"
)

// AdminSession is one sign-in (a session family): every refresh-token
// rotation of that sign-in stays under the same ID.
type AdminSession struct {
	ID         uuid.UUID `json:"id"`
	AuthMethod string    `json:"auth_method"`
	CreatedAt  time.Time `json:"created_at"`
	// LastActiveAt is the latest refresh (the browser refreshes its access
	// token every few minutes while it is in use).
	LastActiveAt time.Time `json:"last_active_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	IP           string    `json:"ip,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	// Current marks the session the request itself was made with.
	Current bool `json:"current"`
}

// AdminSessionListResponse lists the caller's active sessions.
type AdminSessionListResponse struct {
	Items []AdminSession `json:"items"`
}

// RevokeSessionsResponse reports how many sessions a bulk revoke ended.
type RevokeSessionsResponse struct {
	Revoked int `json:"revoked"`
}

// UnlockUserResponse reports whether an unlock cleared an active lockout.
type UnlockUserResponse struct {
	WasLocked bool `json:"was_locked"`
}
