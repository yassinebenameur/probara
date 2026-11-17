// Package pageaccess is the status page access contract shared by the API,
// which stores a page's visibility, visitor password and kiosk tokens, and the
// status-page service, which enforces them (read-only).
//
//	public    anyone (the default)
//	unlisted  anyone with the link; every response says noindex, and no
//	          listing, sitemap or discovery surface may name the page
//	password  the visitor password, or a kiosk token
//	private   kiosk tokens only; anyone else is told the page does not exist
package pageaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/auth"
)

// Visibility values, as stored in status_pages.visibility (migration 000114
// holds the same list in its CHECK).
const (
	Public   = "public"
	Unlisted = "unlisted"
	Password = "password"
	Private  = "private"
)

// Visibilities lists every valid value, most open first.
var Visibilities = []string{Public, Unlisted, Password, Private}

// Valid reports whether v is a known visibility.
func Valid(v string) bool {
	for _, known := range Visibilities {
		if v == known {
			return true
		}
	}
	return false
}

// Gated reports whether a visibility requires a credential. An unknown value
// is gated, so the gate fails closed.
func Gated(v string) bool {
	return v != Public && v != Unlisted
}

// NoIndex reports whether a page's responses must ask search engines not to
// index it: every visibility but public.
func NoIndex(v string) bool {
	return v != Public
}

// Listed reports whether a page may appear on a surface that enumerates
// pages (a sitemap, a directory, a discovery document). Only public pages.
func Listed(v string) bool {
	return v == Public
}

// AcceptsPassword reports whether the visitor password opens a page.
func AcceptsPassword(v string) bool { return v == Password }

// AcceptsTokens reports whether kiosk tokens open a page.
func AcceptsTokens(v string) bool { return v == Password || v == Private }

// Visitor password rules. bcrypt reads at most 72 bytes, so a longer
// password would be silently truncated; it is refused instead.
const (
	MinPasswordLength = 8
	MaxPasswordBytes  = 72
)

// ValidatePassword checks a visitor password before it is hashed.
func ValidatePassword(p string) error {
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(p) > MaxPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes", MaxPasswordBytes)
	}
	return nil
}

// HashPassword returns the hash stored in status_pages.password_hash, made
// with the same bcrypt helper as operator passwords.
func HashPassword(p string) (string, error) {
	return auth.HashPassword(p, auth.DefaultCost)
}

// CheckPassword reports whether p matches a stored hash. An empty hash or
// password never matches, and neither does an over-long one (bcrypt would
// compare only its first 72 bytes).
func CheckPassword(hash, p string) bool {
	if hash == "" || p == "" || len(p) > MaxPasswordBytes {
		return false
	}
	ok, err := auth.ComparePassword(hash, p)
	return err == nil && ok
}

// TokenPrefix marks a kiosk token so a leaked one is recognizable.
const TokenPrefix = "spk_"

// NewToken mints a kiosk token: TokenPrefix plus 32 random bytes. Only hash
// is stored; token is shown once.
func NewToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), nil
}

// HashToken is the stored form of a kiosk token (hex SHA-256). Tokens carry
// 256 random bits, so an unsalted fast hash is enough, and it lets the
// status page look a token up by hash without ever sending it to the
// database.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
