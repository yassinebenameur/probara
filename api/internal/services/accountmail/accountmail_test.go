package accountmail

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
)

type captureSender struct{ msgs []mail.Message }

func (c *captureSender) SendMessage(_ context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	c.msgs = append(c.msgs, msg)
	return nil
}

func TestDisabledWithoutSenderOrBaseURL(t *testing.T) {
	if New(nil, "https://ops.example.com").Enabled() {
		t.Fatal("enabled without a sender")
	}
	m := New(&captureSender{}, "")
	if m.Enabled() {
		t.Fatal("enabled without APP_BASE_URL")
	}
	if err := m.Invite(context.Background(), "a@example.com", "a", "tok", time.Now()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if got := m.URL("/x"); got != "" {
		t.Fatalf("URL without base = %q", got)
	}
}

func TestLinksCarryTheTokenInTheFragment(t *testing.T) {
	sender := &captureSender{}
	m := New(sender, "https://ops.example.com/")
	expires := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	ctx := context.Background()
	if err := m.Invite(ctx, "a@example.com", "alice", "tok-1", expires); err != nil {
		t.Fatal(err)
	}
	if err := m.PasswordReset(ctx, "a@example.com", "alice", "tok-2", expires); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmEmail(ctx, "new@example.com", "alice", "tok-3", expires); err != nil {
		t.Fatal(err)
	}
	if err := m.InviteSSO(ctx, "a@example.com", "alice", "Okta"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://ops.example.com/accept-invite#token=tok-1",
		"https://ops.example.com/reset-password#token=tok-2",
		"https://ops.example.com/confirm-email#token=tok-3",
		"https://ops.example.com/login",
	}
	for i, link := range want {
		msg := sender.msgs[i]
		if !strings.Contains(msg.Text, link) || !strings.Contains(msg.HTML, link) {
			t.Fatalf("message %d lacks %s:\n%s", i, link, msg.Text)
		}
	}
	if !strings.Contains(sender.msgs[3].Text, "Okta") {
		t.Fatal("SSO invite does not name the provider")
	}
	if sender.msgs[2].To[0] != "new@example.com" {
		t.Fatal("confirmation not sent to the new address")
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	sender := &captureSender{}
	m := New(sender, "https://ops.example.com")
	if err := m.EmailChanged(context.Background(), "a@example.com", "<b>x</b>", "n@example.com"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sender.msgs[0].HTML, "<b>x</b>") {
		t.Fatal("username rendered as HTML")
	}
}
