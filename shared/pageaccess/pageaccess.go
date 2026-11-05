// Package pageaccess is the status page access contract shared by the API,
// which stores a page's visibility, visitor password and kiosk tokens, and the
// status-page service, which enforces them.
//
// Visibility:
//
//	public    anyone (the default)
//	unlisted  anyone with the link; every response says noindex
//	password  the visitor password, or a kiosk token
//	private   kiosk tokens only
//
// A visitor who passes the gate gets a session cookie signed with
// STATUS_PAGE_SESSION_SECRET. The cookie names the credential it came from:
// a password session is bound to the page's password_version (a password
// change signs everyone out), a token session to the token's id (revoking the
// token ends the session on the next request). Nothing about a session is
// stored, which keeps the status-page service read-only.
package pageaccess

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Visibility values, as stored in status_pages.visibility (migration 000116
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
// is gated: the gate fails closed.
func Gated(v string) bool {
	return v != Public && v != Unlisted
}

// NoIndex reports whether responses for a page must ask search engines not to
// index it: every visibility but public.
func NoIndex(v string) bool {
	return v != Public
}

// AcceptsPassword reports whether the visitor password opens a page.
func AcceptsPassword(v string) bool { return v == Password }

// AcceptsTokens reports whether kiosk tokens open a page.
func AcceptsTokens(v string) bool { return v == Password || v == Private }

// Password rules. bcrypt reads at most 72 bytes, so a longer password would be
// silently truncated; refuse it instead.
const (
	MinPasswordLength = 8
	MaxPasswordBytes  = 72
	// PasswordCost is the bcrypt cost for visitor passwords. Unlock is
	// rate-limited per client and per page, so the cost bounds CPU per
	// attempt rather than being the only brute-force defense.
	PasswordCost = 11
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

// HashPassword returns the bcrypt hash stored in status_pages.password_hash.
func HashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), PasswordCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword reports whether p matches a stored hash. An empty hash never
// matches.
func CheckPassword(hash, p string) bool {
	if hash == "" || p == "" || len(p) > MaxPasswordBytes {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// TokenPrefix marks a kiosk token so a leaked one is recognizable.
const TokenPrefix = "spk_"

// NewToken mints a kiosk token: TokenPrefix plus 32 random bytes. Only hash is
// stored; token is shown once.
func NewToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), nil
}

// HashToken is the stored form of a kiosk token (hex SHA-256). Tokens carry
// 256 random bits, so an unsalted fast hash is enough.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MinSessionSecretLength is the shortest STATUS_PAGE_SESSION_SECRET accepted.
const MinSessionSecretLength = 32

// Credential is what a visitor session was opened with.
type Credential struct {
	// Kind is KindPassword or KindToken.
	Kind string
	// PasswordVersion is the page's password_version at unlock (password kind).
	PasswordVersion int
	// TokenID is the kiosk token's row id (token kind).
	TokenID uuid.UUID
}

// Credential kinds.
const (
	KindPassword = "p"
	KindToken    = "t"
)

// Session lifetimes. A token session is long because a kiosk can run for
// months without a reload; revocation is checked on every request, so the
// lifetime is not what ends it. Browsers cap cookies at 400 days.
const (
	PasswordSessionTTL = 30 * 24 * time.Hour
	TokenSessionTTL    = 400 * 24 * time.Hour
)

// TTL is the session lifetime for a credential kind.
func (c Credential) TTL() time.Duration {
	if c.Kind == KindToken {
		return TokenSessionTTL
	}
	return PasswordSessionTTL
}

// Signer signs and verifies visitor session cookies.
type Signer struct {
	key []byte
}

// ErrNoSessionSecret means STATUS_PAGE_SESSION_SECRET is unset.
var ErrNoSessionSecret = errors.New("STATUS_PAGE_SESSION_SECRET is not set")

// NewSigner derives the cookie key from the session secret. It returns
// ErrNoSessionSecret for an empty secret and an error for one shorter than
// MinSessionSecretLength.
func NewSigner(secret string) (*Signer, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, ErrNoSessionSecret
	}
	if len(secret) < MinSessionSecretLength {
		return nil, fmt.Errorf("STATUS_PAGE_SESSION_SECRET must be at least %d characters", MinSessionSecretLength)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("probara/status-page-session/v1"))
	return &Signer{key: mac.Sum(nil)}, nil
}

// CookieName is the session cookie for one page. Keyed on the page id, which
// survives a slug rename, so one browser can hold sessions for several pages.
func CookieName(pageID uuid.UUID) string {
	return "probara_sp_" + strings.ReplaceAll(pageID.String(), "-", "")
}

const sessionVersion = "v1"

// Sign returns a cookie value for pageID and c, valid until expires.
func (s *Signer) Sign(pageID uuid.UUID, c Credential, expires time.Time) string {
	var cred string
	switch c.Kind {
	case KindToken:
		cred = KindToken + ":" + c.TokenID.String()
	default:
		cred = KindPassword + ":" + strconv.Itoa(c.PasswordVersion)
	}
	payload := strings.Join([]string{sessionVersion, pageID.String(), cred, strconv.FormatInt(expires.Unix(), 10)}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload))
}

// Verify checks a cookie value for pageID and returns its credential. A
// session signed for another page, expired, tampered with or malformed is
// rejected. Whether the credential is still good (password version, token not
// revoked, visibility still accepting it) is the caller's check.
func (s *Signer) Verify(pageID uuid.UUID, value string, now time.Time) (Credential, bool) {
	if s == nil {
		return Credential{}, false
	}
	encPayload, encMAC, ok := strings.Cut(value, ".")
	if !ok {
		return Credential{}, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return Credential{}, false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(encMAC)
	if err != nil {
		return Credential{}, false
	}
	payload := string(payloadBytes)
	if subtle.ConstantTimeCompare(gotMAC, s.mac(payload)) != 1 {
		return Credential{}, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 4 || parts[0] != sessionVersion || parts[1] != pageID.String() {
		return Credential{}, false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || !now.Before(time.Unix(exp, 0)) {
		return Credential{}, false
	}
	kind, val, ok := strings.Cut(parts[2], ":")
	if !ok {
		return Credential{}, false
	}
	switch kind {
	case KindPassword:
		v, err := strconv.Atoi(val)
		if err != nil {
			return Credential{}, false
		}
		return Credential{Kind: KindPassword, PasswordVersion: v}, true
	case KindToken:
		id, err := uuid.Parse(val)
		if err != nil {
			return Credential{}, false
		}
		return Credential{Kind: KindToken, TokenID: id}, true
	}
	return Credential{}, false
}

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}
