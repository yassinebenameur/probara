package auth

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Password length bounds for every password a person chooses: user create
// and update, first-admin bootstrap, invite acceptance, password reset and
// the account page. ValidatePassword is the one check. The upper bound is in
// bytes because bcrypt refuses more than 72 bytes of input
// (ErrPasswordTooLong); an accented or non-Latin letter takes two to four,
// so such passwords hold fewer characters. Existing hashes are unaffected:
// they were all made from at most 72 bytes.
const (
	MinPasswordLength = 12
	MaxPasswordBytes  = 72
)

// ErrPasswordTooLong is a password longer than MaxPasswordBytes; HashPassword
// returns it (never a bare bcrypt error), so callers can answer it as a
// validation error.
var ErrPasswordTooLong = fmt.Errorf("password must be at most %d bytes (fewer characters when it uses accented or non-Latin letters)", MaxPasswordBytes)

// ValidatePassword reports why a chosen password is refused. Its messages
// reach API clients.
func ValidatePassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string, cost int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	if cost <= 0 {
		cost = DefaultCost
	}

	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
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
