// Package contacts holds the rules for verified per-user contacts (table
// user_contacts, migration 000111): address normalization, verification
// codes and their limits, and the lookup the delivery worker uses to turn a
// DispatchEnvelope.ContactID into an address. The API's account routes own
// the CRUD and verification flow (api/internal/services/contacts).
package contacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Kind is the transport a contact is reached by.
type Kind string

const (
	KindEmail Kind = "email"
	KindSMS   Kind = "sms"
)

// Limits on verification. A code is 6 digits, lives CodeTTL and allows
// MaxAttempts guesses; a new code needs ResendCooldown since the last one,
// and a user triggers at most MaxSendsPerHour codes and test messages in
// total, so neither the guess space nor a workspace's SMS budget can be
// brute-forced.
const (
	CodeDigits         = 6
	CodeTTL            = 15 * time.Minute
	MaxAttempts        = 5
	ResendCooldown     = 60 * time.Second
	MaxSendsPerHour    = 10
	MaxContactsPerUser = 10
	MaxLabelLength     = 100
)

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindEmail || k == KindSMS }

// NormalizeAddress validates raw for kind and returns its canonical form:
// a bare lower-cased email address, or an E.164 number with spaces, dashes,
// dots and parentheses removed.
func NormalizeAddress(kind Kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case KindEmail:
		addr, err := mail.ParseRecipient(raw)
		if err != nil || addr.Name != "" {
			return "", errors.New("address must be a plain email address (ops@example.com)")
		}
		return strings.ToLower(addr.Address), nil
	case KindSMS:
		n := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '-', '.', '(', ')', '\t':
				return -1
			}
			return r
		}, raw)
		if !e164Pattern.MatchString(n) {
			return "", errors.New("address must be a phone number in international format (+15557654321)")
		}
		return n, nil
	default:
		return "", fmt.Errorf("kind must be %q or %q", KindEmail, KindSMS)
	}
}

// Mask hides most of an address for logs and audit details.
func Mask(kind Kind, address string) string {
	if kind == KindEmail {
		local, domain, ok := strings.Cut(address, "@")
		if !ok || local == "" {
			return "***"
		}
		return local[:1] + "***@" + domain
	}
	if len(address) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(address)-4) + address[len(address)-4:]
}

// NewCode returns a random numeric verification code.
func NewCode() (string, error) {
	max := big.NewInt(1)
	for range CodeDigits {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate verification code: %w", err)
	}
	return fmt.Sprintf("%0*d", CodeDigits, n), nil
}

// HashCode is the stored form of a code, bound to its contact so a hash
// cannot be replayed onto another row.
func HashCode(contactID uuid.UUID, code string) string {
	sum := sha256.Sum256([]byte(contactID.String() + ":" + strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

// CodeMatches compares a submitted code with the stored hash in constant time.
func CodeMatches(contactID uuid.UUID, code, storedHash string) bool {
	want := HashCode(contactID, code)
	return subtle.ConstantTimeCompare([]byte(want), []byte(storedHash)) == 1
}

// Contact is a resolved, decrypted contact.
type Contact struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Kind     Kind
	Address  string
	Verified bool
}

// ErrNotDeliverable is returned by LoadVerified for a contact that is gone,
// unverified, or whose user is disabled: there is nobody to send to, and a
// retry will not change that.
var ErrNotDeliverable = errors.New("contact not deliverable")

// LoadVerified resolves a contact ID into its decrypted address for a send
// (the delivery worker, for an envelope that carries a ContactID). Only a
// verified contact of an enabled user is deliverable.
func LoadVerified(ctx context.Context, q db.Querier, enc secrets.Encryptor, id uuid.UUID) (Contact, error) {
	c := Contact{ID: id, Verified: true}
	var kind, stored string
	err := q.QueryRowContext(ctx, `
		SELECT c.user_id, c.kind, c.address
		FROM user_contacts c
		JOIN admin_users u ON u.id = c.user_id
		WHERE c.id = $1 AND c.verified_at IS NOT NULL AND u.disabled_at IS NULL
	`, id).Scan(&c.UserID, &kind, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrNotDeliverable
	}
	if err != nil {
		return Contact{}, fmt.Errorf("load contact: %w", err)
	}
	c.Kind = Kind(kind)
	c.Address, err = enc.Decrypt(stored)
	if err != nil {
		return Contact{}, fmt.Errorf("decrypt contact address: %w", err)
	}
	return c, nil
}
