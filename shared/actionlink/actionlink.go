// Package actionlink mints and verifies the signed tokens behind one-click
// alert actions in notifications ("acknowledge this alert"). *Signer
// implements present.LinkSigner, the seam package present renders action
// links through, and the API verifies the same tokens.
//
// A token is stateless. It carries the action, the tenant, alert and channel
// it was delivered for, its expiry and the key version that signed it, plus a
// truncated HMAC-SHA256 over all of them. The HMAC key is HKDF-SHA256 of the
// PROBARA_SECRETS_KEY version named in the token (info hkdfInfo), so:
//   - no new secret is needed, and the at-rest encryption key is never used
//     directly as a MAC key;
//   - after a _V<n> rotation, links signed under an older version keep
//     verifying for as long as that version stays configured, and removing
//     a version revokes every link it signed;
//   - a process without a key mints nothing and verifies nothing.
//
// Nothing is stored server side, so a token is exactly as reusable as the
// action it authorizes: acknowledging an acknowledged alert is a no-op.
package actionlink

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// ActionAcknowledge is the only action a token can authorize. It equals
// present.ActionAcknowledge (pinned by a test).
const ActionAcknowledge = "ack"

// TTL is how long a link stays valid after the notification carrying it was
// built: long enough to cover a weekend of reminders, short enough that a
// forwarded message goes stale.
const TTL = 72 * time.Hour

// hkdfInfo separates the link-signing key from every other use of the
// secrets keyring (AES-GCM envelopes use the raw key). Changing it revokes
// every outstanding link.
const hkdfInfo = "probara/alert-action/v1"

// Binary layout before base64url encoding: format, action code, tenant,
// alert and channel UUIDs, expiry (unix seconds), key version, truncated MAC.
// 71 bytes, 95 characters encoded, so the whole link fits an SMS beside the
// alert text.
const (
	formatV1   = 1
	macBytes   = 16 // 128-bit truncated HMAC-SHA256
	headerSize = 1 + 1 + 16*3 + 4 + 1
	tokenBytes = headerSize + macBytes
	// EncodedLen is the length of every token in characters.
	EncodedLen = (tokenBytes*8 + 5) / 6
)

// actionCodes maps an action to its byte in the token. A code is never
// reused for a different action.
var actionCodes = map[string]byte{ActionAcknowledge: 1}

// Errors. ErrExpired is returned only for an authentic token, together with
// its claims, so a caller may still say which alert the stale link was for.
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

// Signer mints and verifies action tokens. It is safe for concurrent use.
// A nil *Signer mints nothing (ErrNotConfigured) and verifies nothing
// (ErrInvalid), so a process without a key fails closed.
type Signer struct {
	keys Keyring
	now  func() time.Time

	mu      sync.Mutex
	derived map[int][]byte
}

// New returns a signer over keys.
func New(keys Keyring) *Signer {
	return &Signer{keys: keys, now: time.Now, derived: map[int][]byte{}}
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

// ActionToken mints a token authorizing action on alertID in tenantID, as
// delivered on channelID, valid for TTL from issuedAt (the time the
// notification was built). A zero or future issuedAt counts from now, so a
// skewed event clock can never stretch a link's lifetime. It implements
// present.LinkSigner.
func (s *Signer) ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error) {
	if s == nil || s.keys == nil {
		return "", ErrNotConfigured
	}
	code, ok := actionCodes[action]
	if !ok {
		return "", fmt.Errorf("actionlink: unknown action %q", action)
	}
	var ids [3]uuid.UUID
	for i, raw := range []string{tenantID, alertID, channelID} {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || id == uuid.Nil {
			return "", errors.New("actionlink: a token needs tenant, alert and channel IDs")
		}
		ids[i] = id
	}
	_, version, err := s.keys.CurrentKey()
	if err != nil {
		return "", err
	}
	if version < 1 || version > 0xff {
		return "", fmt.Errorf("actionlink: unsupported key version %d", version)
	}
	key, err := s.key(version)
	if err != nil {
		return "", err
	}
	now := s.now()
	if issuedAt.IsZero() || issuedAt.After(now) {
		issuedAt = now
	}
	exp := issuedAt.Add(TTL).Unix()
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
	buf[54] = byte(version)
	copy(buf[headerSize:], mac(key, buf[:headerSize]))
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Verify checks token and returns its claims when it is authentic, names
// action and has not expired. An authentic but expired token returns its
// claims with ErrExpired. Anything else that fails (malformed, tampered,
// another action, a key version no longer configured) returns ErrInvalid and
// no claims, so callers cannot tell those cases apart and neither can a
// prober.
func (s *Signer) Verify(token, action string) (Claims, error) {
	if s == nil || s.keys == nil {
		return Claims{}, ErrInvalid
	}
	if len(token) != EncodedLen {
		return Claims{}, ErrInvalid
	}
	buf, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(buf) != tokenBytes || buf[0] != formatV1 {
		return Claims{}, ErrInvalid
	}
	version := int(buf[54])
	key, err := s.key(version)
	if err != nil {
		// A retired or never-configured key version: the link is revoked.
		return Claims{}, ErrInvalid
	}
	// The MAC covers every header byte, so nothing below reads data an
	// attacker chose.
	if !hmac.Equal(buf[headerSize:], mac(key, buf[:headerSize])) {
		return Claims{}, ErrInvalid
	}
	// A token minted for one action never authorizes another.
	if code, ok := actionCodes[action]; !ok || buf[1] != code {
		return Claims{}, ErrInvalid
	}
	claims := Claims{
		Action:     action,
		ExpiresAt:  time.Unix(int64(binary.BigEndian.Uint32(buf[50:54])), 0).UTC(),
		KeyVersion: version,
	}
	copy(claims.TenantID[:], buf[2:18])
	copy(claims.AlertID[:], buf[18:34])
	copy(claims.ChannelID[:], buf[34:50])
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

// mac is the truncated HMAC-SHA256 over the token header.
func mac(key, header []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(header)
	return h.Sum(nil)[:macBytes]
}
