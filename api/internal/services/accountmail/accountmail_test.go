package accountmail

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
)

type captureSender struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (c *captureSender) SendMessage(_ context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

func TestDisabledWithoutSenderOrBaseURL(t *testing.T) {
	if New(nil, "https://app.example").Enabled() {
		t.Fatal("enabled without a sender")
	}
	if New(&captureSender{}, " ").Enabled() {
		t.Fatal("enabled without APP_BASE_URL")
	}
	var nilMailer *Mailer
	if nilMailer.Enabled() {
		t.Fatal("nil mailer enabled")
	}
	if err := New(nil, "").Invite(context.Background(), "a@example.com", "a", "tok", time.Now()); err != ErrNotConfigured {
		t.Fatalf("send without config: %v", err)
	}
	// Without APP_BASE_URL a link is a path on the operator UI.
	if got := New(nil, "").URL(LinkPath(ResetPasswordPath, "tok")); got != "/reset-password#tok" {
		t.Fatalf("URL = %q", got)
	}
}

func TestLinksCarryTheTokenInTheFragment(t *testing.T) {
	sender := &captureSender{}
	m := New(sender, "https://app.example/")
	expires := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	if err := m.PasswordReset(context.Background(), "ops@example.com", "ops", "T0KEN_abc-123", expires); err != nil {
		t.Fatal(err)
	}
	msg := sender.msgs[0]
	if len(msg.To) != 1 || msg.To[0] != "ops@example.com" {
		t.Fatalf("to = %v", msg.To)
	}
	want := "https://app.example/reset-password#T0KEN_abc-123"
	if !strings.Contains(msg.Text, want) || !strings.Contains(msg.HTML, want) {
		t.Fatalf("link %s missing:\n%s\n%s", want, msg.Text, msg.HTML)
	}
	if strings.Contains(msg.Text, "?token") || strings.Contains(msg.HTML, "data:") {
		t.Fatal("token outside the fragment, or a data: URI in the mail")
	}
	if !strings.Contains(msg.Text, "Fri 20 Nov 2026 09:30 UTC") {
		t.Fatalf("expiry missing:\n%s", msg.Text)
	}
}

func TestMessagesRenderAndEscape(t *testing.T) {
	sender := &captureSender{}
	m := New(sender, "https://app.example")
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	for name, send := range map[string]func() error{
		"invite":       func() error { return m.Invite(ctx, "a@example.com", "<b>a</b>", "t", exp) },
		"invite sso":   func() error { return m.InviteSSO(ctx, "a@example.com", "a", "Okta") },
		"confirm":      func() error { return m.ConfirmEmail(ctx, "new@example.com", "a", "t", exp) },
		"change":       func() error { return m.EmailChangeRequested(ctx, "old@example.com", "a", "new@example.com") },
		"2fa on":       func() error { return m.TwoFactorEnabled(ctx, "a@example.com", "a") },
		"2fa off":      func() error { return m.TwoFactorDisabled(ctx, "a@example.com", "a", true) },
		"recovery use": func() error { return m.RecoveryCodeUsed(ctx, "a@example.com", "a", 9) },
	} {
		if err := send(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, msg := range sender.msgs {
		if msg.Subject == "" || msg.Text == "" || msg.HTML == "" {
			t.Fatalf("incomplete message %+v", msg)
		}
		if strings.Contains(msg.HTML, "<b>a</b>") {
			t.Fatal("username rendered unescaped")
		}
	}
}

func TestBackgroundSendsDetached(t *testing.T) {
	sender := &captureSender{}
	m := New(sender, "https://app.example")
	done := make(chan struct{})
	m.Background(nil, "test", func(ctx context.Context) error {
		defer close(done)
		if _, ok := ctx.Deadline(); !ok {
			t.Error("background send has no deadline")
		}
		return m.TwoFactorEnabled(ctx, "a@example.com", "a")
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background send never ran")
	}
	// A disabled mailer runs nothing.
	New(nil, "").Background(nil, "test", func(context.Context) error {
		t.Error("ran without a mailer")
		return nil
	})
}
