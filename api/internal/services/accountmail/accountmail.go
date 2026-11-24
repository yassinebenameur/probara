// Package accountmail builds and sends the account emails: invites, password
// resets, email-change confirmations and security notices (two-factor turned
// on or off, a recovery code used). Links point at the operator UI
// (APP_BASE_URL) and carry their single-use token in the URL fragment, which
// browsers never send to a server, so the token stays out of proxy and access
// logs; the page hands it to the API in a request header.
//
// Without a mailer (SMTP_*) or APP_BASE_URL nothing can be delivered:
// Enabled reports false, and callers hand a superadmin the link (LinkPath)
// instead, never a user.
//
// Every message goes to the account's own address only, and says nothing a
// stranger could use to learn whether some other account exists.
package accountmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
)

// UI routes the links open.
const (
	AcceptInvitePath  = "/accept-invite"
	ResetPasswordPath = "/reset-password"
	ConfirmEmailPath  = "/confirm-email"
	LoginPath         = "/login"
	AccountPath       = "/account"
)

// ErrNotConfigured: no mailer or no APP_BASE_URL.
var ErrNotConfigured = errors.New("email delivery is not configured (SMTP and APP_BASE_URL)")

// Mailer sends account mail through the platform mailer.
type Mailer struct {
	sender  mail.Sender
	baseURL string
}

// New returns a Mailer; a nil sender or an empty appBaseURL leaves it
// disabled.
func New(sender mail.Sender, appBaseURL string) *Mailer {
	return &Mailer{sender: sender, baseURL: strings.TrimRight(strings.TrimSpace(appBaseURL), "/")}
}

// Enabled reports whether account mail can be delivered.
func (m *Mailer) Enabled() bool {
	return m != nil && m.sender != nil && m.baseURL != ""
}

// LinkPath is the UI path of a token link: the page, and the token in the
// fragment.
func LinkPath(page, token string) string {
	return page + "#" + token
}

// URL is the absolute link for a UI path, or the path itself without
// APP_BASE_URL (the operator UI then prefixes its own origin).
func (m *Mailer) URL(path string) string {
	if m == nil || m.baseURL == "" {
		return path
	}
	return m.baseURL + path
}

// sendTimeout bounds a background send, like the mailer's own per-message
// deadline.
const sendTimeout = 30 * time.Second

// Background runs send detached from the request, under sendTimeout, so a
// slow or failing relay never delays or changes the answer (a notice is
// best effort; the action it reports already happened). Failures are logged
// without the message.
func (m *Mailer) Background(log *logger.Logger, what string, send func(ctx context.Context) error) {
	if !m.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := send(ctx); err != nil && log != nil {
			log.WithError(err).Warn("Failed to send account email: " + what)
		}
	}()
}

// expiry renders a link's expiry for the mail body.
func expiry(t time.Time) string {
	return t.UTC().Format("Mon 2 Jan 2006 15:04 UTC")
}

// Invite mails a set-your-password invite.
func (m *Mailer) Invite(ctx context.Context, to, username, token string, expires time.Time) error {
	return m.send(ctx, to, view{
		Subject: "You are invited to Probara",
		Title:   "Set up your Probara account",
		Paragraphs: []string{
			fmt.Sprintf("An administrator created a Probara account for you. Your username is %s.", username),
			"Choose a password to finish setting it up.",
		},
		ButtonLabel: "Set your password",
		ButtonURL:   m.URL(LinkPath(AcceptInvitePath, token)),
		Footer: []string{
			fmt.Sprintf("The link works once and expires %s. A newer invite replaces it.", expiry(expires)),
			"If you were not expecting this, you can ignore this email.",
		},
	})
}

// InviteSSO mails an invite for an account that signs in with single sign-on:
// there is no password to set.
func (m *Mailer) InviteSSO(ctx context.Context, to, username, providerLabel string) error {
	if providerLabel == "" {
		providerLabel = "single sign-on"
	}
	return m.send(ctx, to, view{
		Subject: "You are invited to Probara",
		Title:   "Your Probara account is ready",
		Paragraphs: []string{
			fmt.Sprintf("An administrator created a Probara account for you (username %s).", username),
			fmt.Sprintf("Sign in with %s on the Probara sign-in page; there is no password to set.", providerLabel),
		},
		ButtonLabel: "Open Probara",
		ButtonURL:   m.URL(LoginPath),
	})
}

// PasswordReset mails a reset link.
func (m *Mailer) PasswordReset(ctx context.Context, to, username, token string, expires time.Time) error {
	return m.send(ctx, to, view{
		Subject: "Reset your Probara password",
		Title:   "Reset your password",
		Paragraphs: []string{
			fmt.Sprintf("A password reset was requested for the Probara account %s.", username),
			"Choose a new password with the link below. Resetting signs the account out of every browser and disconnects its MCP clients; workspace API keys are not affected.",
		},
		ButtonLabel: "Choose a new password",
		ButtonURL:   m.URL(LinkPath(ResetPasswordPath, token)),
		Footer: []string{
			fmt.Sprintf("The link works once and expires %s.", expiry(expires)),
			"If you did not ask for this, ignore this email: your password stays as it is.",
		},
	})
}

// ConfirmEmail mails the confirmation link to a new address.
func (m *Mailer) ConfirmEmail(ctx context.Context, to, username, token string, expires time.Time) error {
	return m.send(ctx, to, view{
		Subject: "Confirm your new Probara email address",
		Title:   "Confirm your email address",
		Paragraphs: []string{
			fmt.Sprintf("The Probara account %s asked to use this address.", username),
			"Confirm it to finish the change.",
		},
		ButtonLabel: "Confirm email address",
		ButtonURL:   m.URL(LinkPath(ConfirmEmailPath, token)),
		Footer: []string{
			fmt.Sprintf("The link works once and expires %s.", expiry(expires)),
			"If you did not ask for this, ignore this email; nothing changes.",
		},
	})
}

// EmailChangeRequested tells the current address that a change to another
// one is waiting for confirmation.
func (m *Mailer) EmailChangeRequested(ctx context.Context, to, username, newEmail string) error {
	return m.send(ctx, to, view{
		Subject: "Your Probara email address is being changed",
		Title:   "Email address change requested",
		Paragraphs: []string{
			fmt.Sprintf("The Probara account %s asked to change its email address to %s. The change happens when the link sent to that address is used.", username, newEmail),
			"If this was not you, change your password now: that cancels the change and signs everyone else out.",
		},
		ButtonLabel: "Open your account",
		ButtonURL:   m.URL(AccountPath + "?tab=password"),
	})
}

// TwoFactorEnabled tells the account that two-factor sign-in was turned on.
func (m *Mailer) TwoFactorEnabled(ctx context.Context, to, username string) error {
	return m.securityNotice(ctx, to, "Two-factor sign-in turned on",
		fmt.Sprintf("Two-factor sign-in was turned on for the Probara account %s. Signing in now needs a code from the authenticator app, or a recovery code.", username))
}

// TwoFactorDisabled tells the account that two-factor sign-in was turned
// off, by its owner or (byAdministrator) by a superadmin.
func (m *Mailer) TwoFactorDisabled(ctx context.Context, to, username string, byAdministrator bool) error {
	who := "from the account page"
	if byAdministrator {
		who = "by an administrator"
	}
	return m.securityNotice(ctx, to, "Two-factor sign-in turned off",
		fmt.Sprintf("Two-factor sign-in was turned off %s for the Probara account %s. Signing in now needs the password alone.", who, username))
}

// RecoveryCodeUsed tells the account that a recovery code was used to sign
// in, and how many are left.
func (m *Mailer) RecoveryCodeUsed(ctx context.Context, to, username string, remaining int) error {
	return m.securityNotice(ctx, to, "A recovery code was used",
		fmt.Sprintf("A recovery code was used to sign in to the Probara account %s, so every other browser signed in to it was signed out. %d unused recovery codes remain; you can generate a new set on the account page.", username, remaining))
}

// Password change sources, for PasswordChanged.
const (
	PasswordChangedByUser          = "from the account page"
	PasswordChangedByReset         = "with a password reset link"
	PasswordChangedByAdministrator = "by an administrator"
)

// PasswordChanged tells the account its password was changed (how is one of
// the PasswordChangedBy constants).
func (m *Mailer) PasswordChanged(ctx context.Context, to, username, how string) error {
	return m.securityNotice(ctx, to, "Password changed",
		fmt.Sprintf("The password of the Probara account %s was changed %s. Other browsers signed in to it were signed out and its MCP clients disconnected; workspace API keys are not affected.", username, how))
}

// EmailChanged tells the old address that the account's address changed.
func (m *Mailer) EmailChanged(ctx context.Context, to, username, newEmail string) error {
	return m.securityNotice(ctx, to, "Email address changed",
		fmt.Sprintf("The email address of the Probara account %s is now %s. Password reset links and notices go there from now on, and links sent to this address no longer work.", username, newEmail))
}

// SecondFactorLockout tells the account that failed two-factor codes locked
// it: whoever tried knows the password. held: repeated lockouts keep it
// locked until an administrator unlocks it or a password reset is completed.
func (m *Mailer) SecondFactorLockout(ctx context.Context, to, username string, held bool) error {
	lock := "Sign-in is locked for 15 minutes."
	if held {
		lock = "This happened repeatedly, so sign-in stays locked until an administrator unlocks it or you reset your password."
	}
	return m.send(ctx, to, view{
		Subject: "Probara security notice: someone used your password",
		Title:   "Someone entered your password",
		Paragraphs: []string{
			fmt.Sprintf("Someone signed in to the Probara account %s with the right password, then entered wrong two-factor codes until the account locked. %s", username, lock),
			"If this was not you, your password is known to someone else: change it now (use Forgot password on the sign-in page if you are locked out) and tell your Probara administrator.",
		},
		ButtonLabel: "Reset your password",
		ButtonURL:   m.URL("/forgot-password"),
	})
}

func (m *Mailer) securityNotice(ctx context.Context, to, title, text string) error {
	return m.send(ctx, to, view{
		Subject: "Probara security notice: " + strings.ToLower(title[:1]) + title[1:],
		Title:   title,
		Paragraphs: []string{
			text,
			"If this was not you, change your password and contact your Probara administrator.",
		},
		ButtonLabel: "Open your account",
		ButtonURL:   m.URL(AccountPath + "?tab=two-factor"),
	})
}

// view is one message's content; both parts render from it, so the HTML and
// plain-text bodies cannot drift apart.
type view struct {
	Subject     string
	Title       string
	Paragraphs  []string
	ButtonLabel string
	ButtonURL   string
	Footer      []string
}

func (v view) text() string {
	var b strings.Builder
	b.WriteString(v.Title + "\n\n")
	for _, p := range v.Paragraphs {
		b.WriteString(p + "\n\n")
	}
	if v.ButtonURL != "" {
		b.WriteString(v.ButtonLabel + ": " + v.ButtonURL + "\n\n")
	}
	for _, p := range v.Footer {
		b.WriteString(p + "\n\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// Brand tokens, as in the SLA report mail (no brand mark: mail clients drop
// data: images and block remote ones; an orange rule and the product name
// stand in).
const (
	brandOrange = "#ff5a24"
	brandInk    = "#140a05"
	toneMuted   = "#5f6368"
)

var mailHTML = htmltemplate.Must(htmltemplate.New("account").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title></head>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:{{.Ink}}">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:24px 0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;overflow:hidden">
<tr><td style="height:4px;background:{{.Orange}};font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="padding:24px 28px 8px">
<div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:{{.Muted}}">Probara · Account</div>
<h1 style="margin:6px 0 4px;font-size:20px;line-height:1.3;color:{{.Ink}}">{{.Title}}</h1>
</td></tr>
{{range .Paragraphs}}<tr><td style="padding:6px 28px;font-size:14px;line-height:1.5">{{.}}</td></tr>
{{end}}{{if .ButtonURL}}<tr><td style="padding:16px 28px 8px"><a href="{{.ButtonURL}}" style="display:inline-block;padding:10px 18px;border-radius:6px;background:{{.Orange}};color:{{.Ink}};font-weight:600;font-size:14px;text-decoration:none">{{.ButtonLabel}}</a>
<div style="margin-top:8px;font-size:12px;line-height:1.5;color:{{.Muted}};word-break:break-all">{{.ButtonURL}}</div></td></tr>
{{end}}{{if .Footer}}<tr><td style="padding:16px 28px 24px;border-top:1px solid #e4e4e7;font-size:12px;line-height:1.5;color:{{.Muted}}">{{range .Footer}}<p style="margin:0 0 6px">{{.}}</p>{{end}}</td></tr>
{{else}}<tr><td style="padding:0 0 16px"></td></tr>
{{end}}</table></td></tr></table></body></html>
`))

func (v view) html() (string, error) {
	var b bytes.Buffer
	err := mailHTML.Execute(&b, struct {
		view
		Orange, Ink, Muted string
	}{v, brandOrange, brandInk, toneMuted})
	return b.String(), err
}

func (m *Mailer) send(ctx context.Context, to string, v view) error {
	if !m.Enabled() {
		return ErrNotConfigured
	}
	html, err := v.html()
	if err != nil {
		return fmt.Errorf("%w: render: %v", mail.ErrInvalidMessage, err)
	}
	return m.sender.SendMessage(ctx, mail.Message{
		To:      []string{to},
		Subject: v.Subject,
		Text:    v.text(),
		HTML:    html,
	})
}
