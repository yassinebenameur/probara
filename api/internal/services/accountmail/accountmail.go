// Package accountmail builds and sends the account-lifecycle emails:
// invites, password resets and email-change confirmations. Links point at
// the operator UI (APP_BASE_URL) and carry the one-time token in the URL
// fragment, which browsers never send to a server, so the token stays out
// of proxy and access logs; the page posts it to the API.
//
// Without a mailer (SMTP_*) or APP_BASE_URL nothing can be delivered:
// Enabled reports false and callers fall back to handing a superadmin the
// link (LinkPath) for manual delivery.
package accountmail

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
)

// UI routes the links open.
const (
	AcceptInvitePath  = "/accept-invite"
	ResetPasswordPath = "/reset-password"
	ConfirmEmailPath  = "/confirm-email"
	LoginPath         = "/login"
)

// ErrNotConfigured: no mailer or no APP_BASE_URL.
var ErrNotConfigured = errors.New("email delivery is not configured (SMTP and APP_BASE_URL)")

// Mailer sends account mail through the platform mailer.
type Mailer struct {
	sender  mail.Sender
	baseURL string
}

// New returns a Mailer; a nil sender or empty appBaseURL leaves it disabled.
func New(sender mail.Sender, appBaseURL string) *Mailer {
	return &Mailer{sender: sender, baseURL: strings.TrimRight(strings.TrimSpace(appBaseURL), "/")}
}

// Enabled reports whether account mail can be delivered.
func (m *Mailer) Enabled() bool {
	return m != nil && m.sender != nil && m.baseURL != ""
}

// LinkPath is the UI path (with the token fragment) for a token link.
func LinkPath(page, token string) string {
	if token == "" {
		return page
	}
	return page + "#token=" + token
}

// URL is the absolute link for path, or "" without APP_BASE_URL.
func (m *Mailer) URL(path string) string {
	if m == nil || m.baseURL == "" {
		return ""
	}
	return m.baseURL + path
}

// Invite mails a set-your-password invite.
func (m *Mailer) Invite(ctx context.Context, to, username, token string, expires time.Time) error {
	link := m.URL(LinkPath(AcceptInvitePath, token))
	return m.send(ctx, to, "You have been invited to Probara", []string{
		fmt.Sprintf("An administrator created a Probara account for you (username: %s).", username),
		"Set your password to sign in:",
	}, link, "Set your password", []string{
		fmt.Sprintf("This link works once and expires %s.", expires.UTC().Format("Mon 2 Jan 2006 15:04 MST")),
		"If you were not expecting this, you can ignore this email.",
	})
}

// InviteSSO mails an invite for an account that signs in through SSO.
func (m *Mailer) InviteSSO(ctx context.Context, to, username, providerLabel string) error {
	if providerLabel == "" {
		providerLabel = "single sign-on"
	}
	return m.send(ctx, to, "You have been invited to Probara", []string{
		fmt.Sprintf("An administrator created a Probara account for you (username: %s).", username),
		fmt.Sprintf("Sign in with %s; there is no password to set.", providerLabel),
	}, m.URL(LoginPath), "Sign in", nil)
}

// PasswordReset mails a reset link.
func (m *Mailer) PasswordReset(ctx context.Context, to, username, token string, expires time.Time) error {
	link := m.URL(LinkPath(ResetPasswordPath, token))
	return m.send(ctx, to, "Reset your Probara password", []string{
		fmt.Sprintf("Someone asked to reset the password of the Probara account %s.", username),
		"Choose a new password:",
	}, link, "Reset password", []string{
		fmt.Sprintf("This link works once and expires %s. Resetting signs the account out everywhere.", expires.UTC().Format("Mon 2 Jan 2006 15:04 MST")),
		"If you did not ask for this, ignore this email; your password stays as it is.",
	})
}

// ConfirmEmail mails the confirmation link to a new address.
func (m *Mailer) ConfirmEmail(ctx context.Context, to, username, token string, expires time.Time) error {
	link := m.URL(LinkPath(ConfirmEmailPath, token))
	return m.send(ctx, to, "Confirm your new Probara email address", []string{
		fmt.Sprintf("The Probara account %s asked to use this address.", username),
		"Confirm it to finish the change:",
	}, link, "Confirm email address", []string{
		fmt.Sprintf("This link works once and expires %s.", expires.UTC().Format("Mon 2 Jan 2006 15:04 MST")),
		"If you did not ask for this, ignore this email; nothing changes.",
	})
}

// EmailChanged tells the old address that the account moved away from it.
func (m *Mailer) EmailChanged(ctx context.Context, to, username, newEmail string) error {
	return m.send(ctx, to, "Your Probara email address was changed", []string{
		fmt.Sprintf("The email address of the Probara account %s is now %s.", username, newEmail),
		"If you did not make this change, contact your Probara administrator.",
	}, "", "", nil)
}

// send renders one message: intro paragraphs, an optional button link, and
// footer paragraphs, as plain text and as minimal escaped HTML.
func (m *Mailer) send(ctx context.Context, to, subject string, intro []string, link, linkLabel string, footer []string) error {
	if !m.Enabled() {
		return ErrNotConfigured
	}
	var text, body strings.Builder
	for _, p := range intro {
		text.WriteString(p + "\n\n")
		body.WriteString("<p>" + html.EscapeString(p) + "</p>")
	}
	if link != "" {
		text.WriteString(link + "\n\n")
		body.WriteString(`<p><a href="` + html.EscapeString(link) + `" style="display:inline-block;padding:10px 18px;background:#f97316;color:#111;text-decoration:none;border-radius:6px;font-weight:600">` +
			html.EscapeString(linkLabel) + `</a></p><p style="color:#666;font-size:12px">` + html.EscapeString(link) + `</p>`)
	}
	for _, p := range footer {
		text.WriteString(p + "\n\n")
		body.WriteString(`<p style="color:#666;font-size:13px">` + html.EscapeString(p) + "</p>")
	}
	return m.sender.SendMessage(ctx, mail.Message{
		To:      []string{to},
		Subject: subject,
		Text:    strings.TrimSpace(text.String()) + "\n",
		HTML:    `<div style="font-family:system-ui,sans-serif;font-size:14px;line-height:1.5;color:#111">` + body.String() + "</div>",
	})
}
