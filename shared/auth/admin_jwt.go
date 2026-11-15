package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AdminClaims represents JWT claims for admin sessions.
type AdminClaims struct {
	AdminID string `json:"admin_id"`
	jwt.RegisteredClaims
}

// CreateAdminToken creates a signed JWT for an admin user.
func CreateAdminToken(adminID, secret string, ttl time.Duration) (string, error) {
	if adminID == "" {
		return "", fmt.Errorf("admin ID cannot be empty")
	}
	if secret == "" {
		return "", fmt.Errorf("JWT secret cannot be empty")
	}

	now := time.Now()
	claims := AdminClaims{
		AdminID: adminID,
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
