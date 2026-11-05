package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AdminClaims represents JWT claims for admin sessions. Identity only:
// roles are never claims (they are resolved from the database per request).
type AdminClaims struct {
	AdminID string `json:"admin_id"`
	// SessionID is the session family (admin_session_families.id) the token
	// was issued for. The auth middleware rejects the token once that family
	// is revoked. Empty on tokens minted before session families existed.
	SessionID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// CreateAdminToken creates a signed JWT for an admin user with no session
// family. Prefer CreateAdminSessionToken, which can be revoked early.
func CreateAdminToken(adminID, secret string, ttl time.Duration) (string, error) {
	return CreateAdminSessionToken(adminID, "", secret, ttl)
}

// CreateAdminSessionToken creates a signed JWT for an admin user, bound to a
// session family via the sid claim (empty sessionID omits the claim).
func CreateAdminSessionToken(adminID, sessionID, secret string, ttl time.Duration) (string, error) {
	if adminID == "" {
		return "", fmt.Errorf("admin ID cannot be empty")
	}
	if secret == "" {
		return "", fmt.Errorf("JWT secret cannot be empty")
	}

	now := time.Now()
	claims := AdminClaims{
		AdminID:   adminID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   adminID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "probara-api",
			Audience:  []string{"admin"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseAdminToken validates and parses an admin JWT.
func ParseAdminToken(tokenString, secret string) (*AdminClaims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("token string cannot be empty")
	}
	if secret == "" {
		return nil, fmt.Errorf("JWT secret cannot be empty")
	}

	claims := &AdminClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	}, jwt.WithIssuer("probara-api"), jwt.WithAudience("admin"))
	if err != nil {
		return nil, err
	}

	return claims, nil
}
