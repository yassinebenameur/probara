//go:build !unit

package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	shared "github.com/yassinebenameur/probara/shared/contacts"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// fakeMailer captures the platform mail.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	fail error
}

func (m *fakeMailer) SendMessage(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *fakeMailer) last() mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

const smsStubType = "test_contacts_sms_stub"

// smsStub is a direct-recipient plugin like Twilio.
type smsStub struct {
	mu   sync.Mutex
	sent []plugin.DispatchRequest
}

func (p *smsStub) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: smsStubType, Capabilities: []plugin.Capability{plugin.CapabilityDirectRecipient},
		Fields: []plugin.Field{{Key: "token", Type: plugin.FieldTypeSecret, Secret: true}}}
}
func (p *smsStub) Validate(json.RawMessage) error { return nil }
func (p *smsStub) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, req)
	return nil
}

var sms = &smsStub{}

func init() { plugin.Register(sms) }

var codePattern = regexp.MustCompile(`\b([0-9]{6})\b`)

func codeIn(t *testing.T, text string) string {
	t.Helper()
	m := codePattern.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no code in %q", text)
	}
	return m[1]
}

func setup(t *testing.T) (context.Context, *Service, *fakeMailer, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "people")
	userID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, 'alice', 'x')`, userID); err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	return ctx, NewService(dbClient, nil, mailer), mailer, tenantID, userID
}

func TestEmailContactVerification(t *testing.T) {
	ctx, s, mailer, tenantID, userID := setup(t)

	c, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindEmail, Address: " Alice@Example.com ", Label: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "alice@example.com" || c.Verified || c.CodeExpiresAt != nil {
		t.Fatalf("created = %+v", c)
	}
	if _, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindEmail, Address: "ALICE@example.com"}); !errors.As(err, new(Conflict)) {
		t.Fatalf("duplicate: err = %v, want Conflict", err)
	}
	// The address is a stored envelope only with an encryptor; the service
	// never stores the code itself.
	var stored, hash string
	if err := s.db.QueryRowContext(ctx, `SELECT address, COALESCE(code_hash, '') FROM user_contacts WHERE id = $1`, c.ID).Scan(&stored, &hash); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Verify(ctx, userID, c.ID, "123456"); !errors.As(err, new(Conflict)) {
		t.Fatalf("verify before a code: err = %v, want Conflict", err)
	}
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	msg := mailer.last()
	if len(msg.To) != 1 || msg.To[0] != "alice@example.com" {
		t.Fatalf("mail to %v", msg.To)
	}
	code := codeIn(t, msg.Text)
	if err := s.db.QueryRowContext(ctx, `SELECT code_hash FROM user_contacts WHERE id = $1`, c.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, code) || hash != shared.HashCode(c.ID, code) {
		t.Fatalf("stored code hash %q", hash)
	}

	// Resend is held by the cooldown.
	var limited RateLimited
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("resend: err = %v, want RateLimited", err)
	}

	// Wrong guesses count down; the right code verifies.
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	var wc ErrWrongCode
	if _, err := s.Verify(ctx, userID, c.ID, wrong); !errors.As(err, &wc) || wc.Remaining != shared.MaxAttempts-1 {
		t.Fatalf("wrong code: err = %v", err)
	}
	// Another user cannot verify it.
	if _, err := s.Verify(ctx, uuid.New(), c.ID, code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user: err = %v, want ErrNotFound", err)
	}
	v, err := s.Verify(ctx, userID, c.ID, code)
	if err != nil || !v.Verified || v.CodeExpiresAt != nil {
		t.Fatalf("verify: %+v, %v", v, err)
	}
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); !errors.As(err, new(Conflict)) {
		t.Fatalf("code for a verified contact: err = %v, want Conflict", err)
	}

	// Verified contacts take a test message.
	if err := s.SendTest(ctx, userID, tenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := mailer.last(); got.Subject != "Probara test message" {
		t.Fatalf("test mail = %+v", got)
	}
	if err := s.SendTest(ctx, userID, tenantID, c.ID); !errors.As(err, new(RateLimited)) {
		t.Fatalf("second test: err = %v, want RateLimited", err)
	}

	list, err := s.List(ctx, userID, tenantID)
	if err != nil || len(list.Items) != 1 || !list.Transports.Email || list.Transports.SMS {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.Delete(ctx, userID, c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAttemptsVoidTheCode(t *testing.T) {
	ctx, s, mailer, tenantID, userID := setup(t)
	c, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindEmail, Address: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	code := codeIn(t, mailer.last().Text)
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for range shared.MaxAttempts {
		if _, err := s.Verify(ctx, userID, c.ID, wrong); !errors.As(err, new(ErrWrongCode)) {
			t.Fatalf("wrong code: err = %v", err)
		}
	}
	// The right code no longer works once the guesses are spent.
	if _, err := s.Verify(ctx, userID, c.ID, code); !errors.As(err, new(Conflict)) {
		t.Fatalf("after max attempts: err = %v, want Conflict", err)
	}
}

func TestFailedDeliveryUsesNoBudget(t *testing.T) {
	ctx, s, mailer, tenantID, userID := setup(t)
	c, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindEmail, Address: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	mailer.fail = errors.New("smtp down")
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); !errors.As(err, new(DeliveryFailed)) {
		t.Fatalf("err = %v, want DeliveryFailed", err)
	}
	mailer.fail = nil
	// No cooldown was started by the failure.
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); err != nil {
		t.Fatalf("retry after a failed delivery: %v", err)
	}
	var sends int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_contact_sends WHERE user_id = $1`, userID).Scan(&sends); err != nil || sends != 1 {
		t.Fatalf("sends = %d, %v; want only the delivered one counted", sends, err)
	}
}

func TestHourlySendCap(t *testing.T) {
	ctx, s, _, tenantID, userID := setup(t)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO user_contact_sends (user_id, sent_at)
		SELECT $1, NOW() - make_interval(mins => g) FROM generate_series(1, $2) g`, userID, shared.MaxSendsPerHour); err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindEmail, Address: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); !errors.As(err, new(RateLimited)) {
		t.Fatalf("err = %v, want RateLimited by the hourly cap", err)
	}
}

func TestSMSContactUsesWorkspaceChannel(t *testing.T) {
	ctx, s, _, tenantID, userID := setup(t)
	c, err := s.Create(ctx, userID, CreateContactRequest{Kind: shared.KindSMS, Address: "+1 555 765 4321"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); !errors.As(err, new(Unavailable)) {
		t.Fatalf("no SMS channel: err = %v, want Unavailable", err)
	}

	channelID := uuid.New()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'People SMS', $3, '{"token":"secret-token"}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID, smsStubType); err != nil {
		t.Fatal(err)
	}
	otherTenant := testutil.InsertTenant(ctx, t, s.db, "other")
	if _, err := s.UpdatePeopleSettings(ctx, otherTenant, UpdatePeopleSettingsRequest{PersonalSMSChannelID: &channelID}); !errors.As(err, new(Invalid)) {
		t.Fatalf("another workspace's channel: err = %v, want Invalid", err)
	}
	settings, err := s.UpdatePeopleSettings(ctx, tenantID, UpdatePeopleSettingsRequest{PersonalSMSChannelID: &channelID})
	if err != nil || settings.PersonalSMSChannelID == nil || *settings.PersonalSMSChannelID != channelID ||
		len(settings.SMSChannelOptions) != 1 {
		t.Fatalf("settings = %+v, %v", settings, err)
	}

	if _, err := s.SendCode(ctx, userID, tenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	req := sms.sent[len(sms.sent)-1]
	if req.Recipient == nil || req.Recipient.Address != "+15557654321" || !req.Test ||
		req.Channel.String("token") != "secret-token" {
		t.Fatalf("sms request = %+v", req)
	}
	code := codeIn(t, req.Recipient.Text)
	if _, err := s.Verify(ctx, userID, c.ID, code); err != nil {
		t.Fatal(err)
	}

	// The worker's lookup resolves the verified contact.
	got, err := shared.LoadVerified(ctx, s.db, s.enc, c.ID)
	if err != nil || got.Address != "+15557654321" || got.UserID != userID {
		t.Fatalf("LoadVerified = %+v, %v", got, err)
	}

	cleared, err := s.UpdatePeopleSettings(ctx, tenantID, UpdatePeopleSettingsRequest{})
	if err != nil || cleared.PersonalSMSChannelID != nil {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
}
