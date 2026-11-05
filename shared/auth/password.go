package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string, cost int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	if cost <= 0 {
		cost = DefaultCost
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	return string(hash), nil
}

// ComparePassword compares a plaintext password with a bcrypt hash.
func ComparePassword(hashedPassword, plainPassword string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plainPassword))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, fmt.Errorf("failed to compare password: %w", err)
	}
	return true, nil
}

// Password policy for local accounts. One rule for every path that sets a
// password: user management, the first-admin form, invites, resets and the
// self-service change.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

// ValidatePassword checks a new password against the policy.
func ValidatePassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return fmt.Errorf("password must be between %d and %d characters", MinPasswordLength, MaxPasswordLength)
	}
	return nil
}
