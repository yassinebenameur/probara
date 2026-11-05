// Package actionlink mints and verifies the signed tokens behind one-click
// alert actions in notifications ("acknowledge this alert"). It implements
// present.LinkSigner, the seam package present renders action links through.
//
// A token is stateless: it carries the tenant, alert and channel it was
// delivered for, its expiry and the key version that signed it, plus a
// truncated HMAC-SHA256 over the canonical string
//
//	v1|<action>|<tenant>|<alert>|<channel>|<exp unix>|<key version>
//
// The HMAC key is HKDF-SHA256 of the PROBARA_SECRETS_KEY version named in the
// token (info "probara/alert-action/v1"), so no new secret is needed, a link
// keeps working across a _V<n> rotation for as long as its key version is
// configured, and removing a version revokes every link it signed. Nothing is
// stored server side; a token is only as single-use as the action it
// authorizes (acknowledging twice is a no-op).
package actionlink

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// ActionAcknowledge is the only action a token can authorize today. It
// equals present.ActionAcknowledge (pinned by a test).
const ActionAcknowledge = "ack"

// DefaultTTL is how long a minted link stays valid: long enough to cover a
// weekend of reminders, short enough that a forwarded email goes stale.
const DefaultTTL = 72 * time.Hour

// hkdfInfo separates the link-signing key from every other use of the
// secrets keyring (AES-GCM envelopes use the raw key).
const hkdfInfo = "probara/alert-action/v1"

// Token layout (before base64url): format, action code, three UUIDs, expiry,
// key version, truncated MAC. 72 bytes, 96 characters encoded, short enough
// for an SMS.
const (
	formatV1   = 1
	macBytes   = 16 // 128-bit truncated HMAC-SHA256
	headerSize = 1 + 1 + 16*3 + 4 + 2
	tokenBytes = headerSize + macBytes
)

// actionCodes maps an action to its byte in the token. Codes are never
// reused for a different action.
var actionCodes = map[string]byte{ActionAcknowledge: 1}

// Errors Verify returns. ErrExpired is only returned for an authentic token,
// with its claims, so a caller may still attribute the attempt.
var (
	ErrInvalid       = errors.New("actionlink: invalid token")
	ErrExpired       = errors.New("actionlink: token expired")
	ErrNotConfigured = errors.New("actionlink: no signing key configured")
)

// Keyring is the slice of secrets.KeyProvider the signer needs.
type Keyring interface {
	CurrentKey() ([]byte, int, error)
	KeyForVersion(v int) ([]byte, error)
}

// Claims is what a verified token authorizes.
type Claims struct {
	Action     string
	TenantID   uuid.UUID
	AlertID    uuid.UUID
	ChannelID  uuid.UUID
	ExpiresAt  time.Time
	KeyVersion int
}

// Signer mints and verifies action tokens. Its methods are safe for
// concurrent use, and a nil *Signer mints nothing (ErrNotConfigured) and
// verifies nothing (ErrInvalid), so a process without a key fails closed.
type Signer struct {
	keys Keyring
	ttl  time.Duration
	now  func() time.Time

	mu      sync.Mutex
	derived map[int][]byte
}

// New returns a signer over keys with the default lifetime.
func New(keys Keyring) *Signer {
	return &Signer{keys: keys, ttl: DefaultTTL, now: time.Now, derived: map[int][]byte{}}
}

// FromEnv builds a signer over the PROBARA_SECRETS_KEY keyring. It returns
// (nil, nil) when no key is configured, which means no action links, and an
// error when a key is set but malformed.
func FromEnv() (*Signer, error) {
	kp, err := secrets.NewEnvKeyProvider()
	if errors.Is(err, secrets.ErrKeyNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return New(kp), nil
}

// WithClock replaces the signer's clock (tests).
func (s *Signer) WithClock(now func() time.Time) *Signer {
	s.now = now
	return s
}

// ActionToken mints a token authorizing action on alertID in tenantID as
// delivered on channelID, valid for the signer's lifetime. It implements
// present.LinkSigner.
func (s *Signer) ActionToken(action, tenantID, alertID, channelID string) (string, error) {
	if s == nil || s.keys == nil {
		return "", ErrNotConfigured
	}
	code, ok := actionCodes[action]
	if !ok {
		return "", fmt.Errorf("actionlink: unknown action %q", action)
	}
	ids := [3]uuid.UUID{}
	for i, raw := range []string{tenantID, alertID, channelID} {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || id == uuid.Nil {
			return "", fmt.Errorf("actionlink: token needs tenant, alert and channel IDs")
		}
		ids[i] = id
	}
	_, version, err := s.keys.CurrentKey()
	if err != nil {
		return "", err
	}
	if version < 1 || version > 0xffff {
		return "", fmt.Errorf("actionlink: unsupported key version %d", version)
	}
	key, err := s.key(version)
	if err != nil {
		return "", err
	}
	exp := s.now().Add(s.ttl).Unix()
	if exp <= 0 || exp > 0xffffffff {
		return "", errors.New("actionlink: expiry out of range")
	}

	buf := make([]byte, tokenBytes)
	buf[0] = formatV1
	buf[1] = code
	copy(buf[2:18], ids[0][:])
	copy(buf[18:34], ids[1][:])
	copy(buf[34:50], ids[2][:])
	binary.BigEndian.PutUint32(buf[50:54], uint32(exp))
	binary.BigEndian.PutUint16(buf[54:56], uint16(version))
	claims := Claims{Action: action, TenantID: ids[0], AlertID: ids[1], ChannelID: ids[2], ExpiresAt: time.Unix(exp, 0).UTC(), KeyVersion: version}
	copy(buf[headerSize:], sign(key, claims))
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Verify checks token and returns its claims when it is authentic, names
// action, and has not expired. An authentic but expired token returns its
// claims with ErrExpired; anything else that fails returns ErrInvalid.
func (s *Signer) Verify(token, action string) (Claims, error) {
	if s == nil || s.keys == nil {
		return Claims{}, ErrInvalid
	}
	buf, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(buf) != tokenBytes || buf[0] != formatV1 {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	for name, code := range actionCodes {
		if code == buf[1] {
			claims.Action = name
		}
	}
	if claims.Action == "" {
		return Claims{}, ErrInvalid
	}
	copy(claims.TenantID[:], buf[2:18])
	copy(claims.AlertID[:], buf[18:34])
	copy(claims.ChannelID[:], buf[34:50])
	claims.ExpiresAt = time.Unix(int64(binary.BigEndian.Uint32(buf[50:54])), 0).UTC()
	claims.KeyVersion = int(binary.BigEndian.Uint16(buf[54:56]))

	key, err := s.key(claims.KeyVersion)
	if err != nil {
		// A retired or never-configured key version: the link is revoked.
		return Claims{}, ErrInvalid
	}
	if !hmac.Equal(buf[headerSize:], sign(key, claims)) {
		return Claims{}, ErrInvalid
	}
	// The action is part of the MAC, so this is a purpose check on an
	// authentic token: a token minted for one action never authorizes
	// another.
	if claims.Action != action {
		return Claims{}, ErrInvalid
	}
	if !s.now().Before(claims.ExpiresAt) {
		return claims, ErrExpired
	}
	return claims, nil
}

// key returns the derived HMAC key for a keyring version, cached.
func (s *Signer) key(version int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.derived[version]; ok {
		return k, nil
	}
	secret, err := s.keys.KeyForVersion(version)
	if err != nil {
		return nil, err
	}
	k, err := hkdf.Key(sha256.New, secret, nil, hkdfInfo, sha256.Size)
	if err != nil {
		return nil, fmt.Errorf("actionlink: derive key: %w", err)
	}
	if s.derived == nil {
		s.derived = map[int][]byte{}
	}
	s.derived[version] = k
	return k, nil
}

// sign is the truncated HMAC over the canonical claims string.
func sign(key []byte, c Claims) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join([]string{
		"v1", c.Action, c.TenantID.String(), c.AlertID.String(), c.ChannelID.String(),
		strconv.FormatInt(c.ExpiresAt.Unix(), 10), strconv.Itoa(c.KeyVersion),
	}, "|")))
	return mac.Sum(nil)[:macBytes]
}
