// Package contacts is the signed-in user's verified contacts (the
// /account/contacts routes) and the workspace's personal SMS channel
// (/people-settings). Addresses are stored as secrets envelopes and returned
// only to their owner; codes are stored as hashes. The rules (normalization,
// limits, code hashing) live in shared/contacts.
package contacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	shared "github.com/yassinebenameur/probara/shared/contacts"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// sendTimeout bounds one verification or test send (SMTP or the SMS API).
const sendTimeout = 20 * time.Second

// Contact is one of the signed-in user's contacts, address in clear.
type Contact struct {
	ID         uuid.UUID   `json:"id"`
	Kind       shared.Kind `json:"kind"`
	Address    string      `json:"address"`
	Label      string      `json:"label"`
	Verified   bool        `json:"verified"`
	VerifiedAt *time.Time  `json:"verified_at,omitempty"`
	// CodeExpiresAt is set while a verification code is outstanding.
	CodeExpiresAt *time.Time `json:"code_expires_at,omitempty"`
	CodeSentAt    *time.Time `json:"code_sent_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Transports says which contact kinds can be verified right now: email
// needs the installation's SMTP, SMS the selected workspace's personal SMS
// channel.
type Transports struct {
	Email bool `json:"email"`
	SMS   bool `json:"sms"`
}

// ContactList is the response of GET /account/contacts.
type ContactList struct {
	Items      []Contact  `json:"items"`
	Transports Transports `json:"transports"`
}

// CreateContactRequest adds an unverified contact.
type CreateContactRequest struct {
	Kind    shared.Kind `json:"kind"`
	Address string      `json:"address"`
	Label   string      `json:"label,omitempty"`
}

// UpdateContactRequest renames a contact. The address cannot change: delete
// the contact and add the new address, which needs its own verification.
type UpdateContactRequest struct {
	Label *string `json:"label,omitempty"`
}

// VerifyContactRequest submits a verification code.
type VerifyContactRequest struct {
	Code string `json:"code"`
}

// Service implements contacts and the people settings.
type Service struct {
	db     *shareddb.Client
	enc    secrets.Encryptor
	mailer mail.Sender
}

// NewService builds the service. mailer is nil when SMTP is not configured
// (email contacts then cannot be verified).
func NewService(db *shareddb.Client, enc secrets.Encryptor, mailer mail.Sender) *Service {
	if enc == nil {
		enc = secrets.NoOpEncryptor{}
	}
	return &Service{db: db, enc: enc, mailer: mailer}
}

const contactColumns = `id, kind, address, label, verified_at,
	CASE WHEN code_hash IS NOT NULL AND code_expires_at > NOW() THEN code_expires_at END,
	code_sent_at, created_at`

func (s *Service) scan(row interface{ Scan(...any) error }) (Contact, error) {
	var c Contact
	var kind, sealed string
	if err := row.Scan(&c.ID, &kind, &sealed, &c.Label, &c.VerifiedAt, &c.CodeExpiresAt, &c.CodeSentAt, &c.CreatedAt); err != nil {
		return c, err
	}
	c.Kind = shared.Kind(kind)
	c.Verified = c.VerifiedAt != nil
	address, err := s.enc.Decrypt(sealed)
	if err != nil {
		return c, fmt.Errorf("decrypt contact address: %w", err)
	}
	c.Address = address
	return c, nil
}

// List returns the user's contacts and which kinds can be verified in the
// selected workspace (tenantID may be uuid.Nil).
func (s *Service) List(ctx context.Context, userID, tenantID uuid.UUID) (*ContactList, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+`
		FROM user_contacts WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()
	out := &ContactList{Items: []Contact{}}
	for rows.Next() {
		c, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Transports.Email = s.mailer != nil
	if tenantID != uuid.Nil {
		if _, _, err := s.smsChannel(ctx, tenantID); err == nil {
			out.Transports.SMS = true
		} else if !errors.As(err, new(Unavailable)) {
			return nil, err
		}
	}
	return out, nil
}

// Get returns one of the user's contacts.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*Contact, error) {
	c, err := s.scan(s.db.QueryRowContext(ctx, `SELECT `+contactColumns+`
		FROM user_contacts WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get contact: %w", err)
	}
	return &c, nil
}

func cleanLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if len([]rune(label)) > shared.MaxLabelLength {
		return "", invalid("label must be at most %d characters", shared.MaxLabelLength)
	}
	if strings.ContainsFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", invalid("label must not contain control characters")
	}
	return label, nil
}

// Create adds an unverified contact. It sends nothing: the caller asks for a
// code with SendCode.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, req CreateContactRequest) (*Contact, error) {
	if !req.Kind.Valid() {
		return nil, invalid("kind must be %q or %q", shared.KindEmail, shared.KindSMS)
	}
	address, err := shared.NormalizeAddress(req.Kind, req.Address)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	label, err := cleanLabel(req.Label)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize a user's creates so the count and duplicate checks hold.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM admin_users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		return nil, fmt.Errorf("lock user: %w", err)
	}
	// Addresses are envelopes (random nonce), so duplicates are found by
	// decrypting the user's few contacts rather than by an index.
	rows, err := tx.QueryContext(ctx, `SELECT kind, address FROM user_contacts WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("load contacts: %w", err)
	}
	count := 0
	for rows.Next() {
		var kind, sealed string
		if err := rows.Scan(&kind, &sealed); err != nil {
			rows.Close()
			return nil, err
		}
		count++
		if shared.Kind(kind) != req.Kind {
			continue
		}
		existing, err := s.enc.Decrypt(sealed)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("decrypt contact address: %w", err)
		}
		if existing == address {
			rows.Close()
			return nil, Conflict{Message: "you already have this " + string(req.Kind) + " contact"}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count >= shared.MaxContactsPerUser {
		return nil, invalid("you can have at most %d contacts", shared.MaxContactsPerUser)
	}
	sealed, err := s.enc.Encrypt(address)
	if err != nil {
		return nil, fmt.Errorf("encrypt contact address: %w", err)
	}
	var id uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO user_contacts (user_id, kind, address, label) VALUES ($1, $2, $3, $4) RETURNING id
	`, userID, string(req.Kind), sealed, label).Scan(&id); err != nil {
		return nil, fmt.Errorf("insert contact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, userID, id)
}

// Update renames a contact.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, req UpdateContactRequest) (*Contact, error) {
	if req.Label != nil {
		label, err := cleanLabel(*req.Label)
		if err != nil {
			return nil, err
		}
		res, err := s.db.ExecContext(ctx, `UPDATE user_contacts SET label = $3, updated_at = NOW()
			WHERE id = $1 AND user_id = $2`, id, userID, label)
		if err != nil {
			return nil, fmt.Errorf("update contact: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
	}
	return s.Get(ctx, userID, id)
}

// Delete removes a contact.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_contacts WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// lockedContact is a contact row read FOR UPDATE inside a send or verify.
type lockedContact struct {
	kind        shared.Kind
	address     string
	verified    bool
	codeHash    sql.NullString
	codeLive    bool
	attempts    int
	cooldownFor time.Duration // > 0 while the last code/test is too recent
}

func (s *Service) lock(ctx context.Context, tx *sql.Tx, userID, id uuid.UUID, sentColumn string) (*lockedContact, error) {
	var c lockedContact
	var kind, sealed string
	var cooldown float64
	// sentColumn is code_sent_at or last_test_at, never caller input.
	err := tx.QueryRowContext(ctx, `
		SELECT kind, address, verified_at IS NOT NULL, code_hash,
			code_hash IS NOT NULL AND code_expires_at > NOW(), attempts,
			GREATEST(0, EXTRACT(EPOCH FROM (`+sentColumn+` + make_interval(secs => $3) - NOW())))::float8
		FROM user_contacts WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, id, userID, shared.ResendCooldown.Seconds()).Scan(&kind, &sealed, &c.verified, &c.codeHash, &c.codeLive, &c.attempts, &sqlFloat{&cooldown})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock contact: %w", err)
	}
	c.kind = shared.Kind(kind)
	c.cooldownFor = time.Duration(cooldown * float64(time.Second))
	c.address, err = s.enc.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("decrypt contact address: %w", err)
	}
	return &c, nil
}

// sqlFloat scans a nullable float as 0.
type sqlFloat struct{ v *float64 }

func (f *sqlFloat) Scan(src any) error {
	var n sql.NullFloat64
	if err := n.Scan(src); err != nil {
		return err
	}
	*f.v = n.Float64
	return nil
}

// reserveSend enforces the hourly per-user cap and records one send. It runs
// in the send's transaction, so a failed delivery (rolled back) uses none of
// the budget.
func reserveSend(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	// Serialize one user's sends (transaction-scoped, released at commit or
	// rollback) so concurrent requests cannot overshoot the cap.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('user_contact_sends:' || $1::text, 0))`, userID); err != nil {
		return fmt.Errorf("lock sends: %w", err)
	}
	var recent int
	var oldest sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(sent_at) FROM user_contact_sends
		WHERE user_id = $1 AND sent_at > NOW() - INTERVAL '1 hour'
	`, userID).Scan(&recent, &oldest); err != nil {
		return fmt.Errorf("count sends: %w", err)
	}
	if recent >= shared.MaxSendsPerHour {
		retry := time.Hour
		if oldest.Valid {
			retry = time.Until(oldest.Time.Add(time.Hour)).Round(time.Second)
		}
		return RateLimited{
			Message:    fmt.Sprintf("you can send at most %d codes and test messages per hour", shared.MaxSendsPerHour),
			RetryAfter: retry,
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_contact_sends WHERE user_id = $1 AND sent_at < NOW() - INTERVAL '1 day'`, userID); err != nil {
		return fmt.Errorf("prune sends: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_contact_sends (user_id) VALUES ($1)`, userID); err != nil {
		return fmt.Errorf("record send: %w", err)
	}
	return nil
}

func cooldownError(d time.Duration, what string) error {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return RateLimited{
		Message:    fmt.Sprintf("wait %d seconds before sending another %s", secs, what),
		RetryAfter: time.Duration(secs) * time.Second,
	}
}

// SendCode sends a new verification code to an unverified contact. tenantID
// is the selected workspace, whose personal SMS channel carries SMS codes.
// The row stays locked through the delivery, so two clicks send one code.
func (s *Service) SendCode(ctx context.Context, userID, tenantID, id uuid.UUID) (*Contact, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lock(ctx, tx, userID, id, "code_sent_at")
	if err != nil {
		return nil, err
	}
	if c.verified {
		return nil, Conflict{Message: "this contact is already verified"}
	}
	if c.cooldownFor > 0 {
		return nil, cooldownError(c.cooldownFor, "code")
	}
	if err := reserveSend(ctx, tx, userID); err != nil {
		return nil, err
	}
	code, err := shared.NewCode()
	if err != nil {
		return nil, err
	}
	minutes := int(shared.CodeTTL / time.Minute)
	msg := message{
		subject: "Your Probara verification code",
		text: fmt.Sprintf("Your Probara verification code is %s. It expires in %d minutes.\n\n"+
			"Someone, hopefully you, added this address as a contact on a Probara account. "+
			"If it was not you, ignore this message.", code, minutes),
		sms: fmt.Sprintf("Probara verification code: %s (expires in %d min)", code, minutes),
	}
	if err := s.deliver(ctx, tenantID, id, c.kind, c.address, msg); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_contacts
		SET code_hash = $2, code_expires_at = NOW() + make_interval(secs => $3), code_sent_at = NOW(),
			attempts = 0, updated_at = NOW()
		WHERE id = $1
	`, id, shared.HashCode(id, code), shared.CodeTTL.Seconds()); err != nil {
		return nil, fmt.Errorf("store code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, userID, id)
}

// ErrWrongCode is returned by Verify for a code that does not match. The
// message carries the guesses left.
type ErrWrongCode struct{ Remaining int }

func (e ErrWrongCode) Error() string {
	if e.Remaining <= 0 {
		return "incorrect code; no attempts left, send a new code"
	}
	return fmt.Sprintf("incorrect code; %d attempt(s) left", e.Remaining)
}

// Verify checks a code. A wrong guess uses one of MaxAttempts; the last one
// voids the code.
func (s *Service) Verify(ctx context.Context, userID, id uuid.UUID, code string) (*Contact, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, invalid("code is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lock(ctx, tx, userID, id, "code_sent_at")
	if err != nil {
		return nil, err
	}
	if c.verified {
		return nil, Conflict{Message: "this contact is already verified"}
	}
	if !c.codeLive || c.attempts >= shared.MaxAttempts {
		return nil, Conflict{Message: "no active code for this contact; send a new one"}
	}
	if !shared.CodeMatches(id, code, c.codeHash.String) {
		attempts := c.attempts + 1
		// The last allowed guess voids the code, so it cannot be retried.
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_contacts
			SET attempts = $2::int,
				code_hash = CASE WHEN $2::int >= $3::int THEN NULL ELSE code_hash END,
				code_expires_at = CASE WHEN $2::int >= $3::int THEN NULL ELSE code_expires_at END,
				updated_at = NOW()
			WHERE id = $1
		`, id, attempts, shared.MaxAttempts); err != nil {
			return nil, fmt.Errorf("count attempt: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return nil, ErrWrongCode{Remaining: shared.MaxAttempts - attempts}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_contacts
		SET verified_at = NOW(), code_hash = NULL, code_expires_at = NULL, attempts = 0, updated_at = NOW()
		WHERE id = $1
	`, id); err != nil {
		return nil, fmt.Errorf("verify contact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, userID, id)
}

// SendTest sends a test message to a verified contact.
func (s *Service) SendTest(ctx context.Context, userID, tenantID, id uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lock(ctx, tx, userID, id, "last_test_at")
	if err != nil {
		return err
	}
	if !c.verified {
		return Conflict{Message: "verify this contact before sending a test"}
	}
	if c.cooldownFor > 0 {
		return cooldownError(c.cooldownFor, "test")
	}
	if err := reserveSend(ctx, tx, userID); err != nil {
		return err
	}
	msg := message{
		subject: "Probara test message",
		text: "This is a test message from Probara. This address is verified for your account, so " +
			"Probara can reach you here.",
		sms: "Probara test: this number is verified for your account.",
	}
	if err := s.deliver(ctx, tenantID, id, c.kind, c.address, msg); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_contacts SET last_test_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("record test: %w", err)
	}
	return tx.Commit()
}

type message struct {
	subject, text, sms string
}

// deliver sends msg to address: email through the platform mailer, SMS
// through the workspace's personal SMS channel.
func (s *Service) deliver(ctx context.Context, tenantID, contactID uuid.UUID, kind shared.Kind, address string, msg message) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	switch kind {
	case shared.KindEmail:
		if s.mailer == nil {
			return Unavailable{Message: "email is not configured on this installation (SMTP), so email contacts cannot be verified"}
		}
		if err := s.mailer.SendMessage(ctx, mail.Message{To: []string{address}, Subject: msg.subject, Text: msg.text}); err != nil {
			return DeliveryFailed{Err: err}
		}
		return nil
	case shared.KindSMS:
		p, ref, err := s.smsChannel(ctx, tenantID)
		if err != nil {
			return err
		}
		err = p.Send(ctx, plugin.DispatchRequest{
			Channel:   ref,
			Event:     notifications.AlertEvent{TenantID: tenantID.String(), Timestamp: time.Now().UTC()},
			Attempt:   1,
			Test:      true,
			Recipient: &plugin.Recipient{ContactID: contactID.String(), Address: address, Text: msg.sms},
		})
		if err != nil {
			return DeliveryFailed{Err: err}
		}
		return nil
	default:
		return invalid("unknown contact kind %q", kind)
	}
}

// smsChannel loads the workspace's personal SMS channel with its secrets
// decrypted.
func (s *Service) smsChannel(ctx context.Context, tenantID uuid.UUID) (plugin.Plugin, plugin.ChannelRef, error) {
	if tenantID == uuid.Nil {
		return nil, plugin.ChannelRef{}, Unavailable{Message: "select a workspace: SMS codes go through the workspace's SMS channel"}
	}
	var ref plugin.ChannelRef
	var typ string
	var raw []byte
	var active bool
	err := s.db.QueryRowContext(ctx, `
		SELECT ac.id, ac.name, ac.type, ac.config, ac.is_active
		FROM tenants t
		JOIN alert_channels ac ON ac.id = t.personal_sms_channel_id AND ac.tenant_id = t.id
		WHERE t.id = $1
	`, tenantID).Scan(&ref.ID, &ref.Name, &typ, &raw, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ref, Unavailable{Message: "this workspace has no SMS channel for people; a workspace admin chooses one in Settings"}
	}
	if err != nil {
		return nil, ref, fmt.Errorf("load personal SMS channel: %w", err)
	}
	if !active {
		return nil, ref, Unavailable{Message: "this workspace's SMS channel for people is paused"}
	}
	p, ok := plugin.DefaultRegistry.Get(typ)
	if !ok || !p.Manifest().HasCapability(plugin.CapabilityDirectRecipient) {
		return nil, ref, Unavailable{Message: "this workspace's SMS channel for people cannot text a single person"}
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, ref, fmt.Errorf("decode channel config: %w", err)
		}
	}
	cfg, err = secrets.DecryptConfig(s.enc, p.Manifest(), cfg)
	if err != nil {
		return nil, ref, fmt.Errorf("decrypt channel config: %w", err)
	}
	ref.Config = cfg
	return p, ref, nil
}
