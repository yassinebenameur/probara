package pagesubscribers

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"regexp"
	"strings"

	"github.com/yassinebenameur/probara/shared/mail"
)

// Subscriber mail wording. It lives with the subscriber code, not in
// shared/notifications/present: it is not an alert notification, and it
// speaks for the page, in the page's name. The HTML part is deliberately
// plain: the page's accent colour as a rule, its title, the text, a button
// back to the page. No images (mail clients block them, and data: URIs are
// dropped).

const (
	defaultAccent = "#ff5a24"
	inkColor      = "#140a05"
	mutedColor    = "#5f6368"
)

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// accent is the page's primary colour when it is a plain hex colour, else
// the default: anything else could break out of the style attribute.
func accent(primary string) string {
	if hexColor.MatchString(strings.TrimSpace(primary)) {
		return strings.TrimSpace(primary)
	}
	return defaultAccent
}

// notice is one outbox event as mailed, rendered when the row is planned
// and stored as the row's content, so every subscriber gets the same text.
type notice struct {
	Kind      string `json:"kind"`
	PageTitle string `json:"page_title"`
	PageURL   string `json:"page_url"`
	Accent    string `json:"accent"`
	// Label is the short tag before the subject ("Incident", "Update",
	// "Resolved", "Scheduled maintenance", "Maintenance started").
	Label    string `json:"label"`
	Headline string `json:"headline"`
	// Body is the event's own text (an incident summary, a public update, a
	// maintenance description); Facts are labelled lines under it.
	Body  string `json:"body,omitempty"`
	Facts []fact `json:"facts,omitempty"`
}

type fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// oneLine removes control characters (the mail seam refuses them in
// headers) and collapses whitespace.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// senderName is the From display name of a page's mail: the page's name,
// one line, bounded (the address stays the configured SMTP sender).
func senderName(pageTitle string) string {
	return truncate(oneLine(pageTitle), 100)
}

func (n notice) subject() string {
	return truncate(oneLine(fmt.Sprintf("[%s] %s: %s", n.PageTitle, n.Label, n.Headline)), 200)
}

// recipientView is a notice plus what differs per recipient.
type recipientView struct {
	notice
	Recipient   string
	Unsubscribe string
}

func (v recipientView) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n\n", v.Label, v.Headline)
	if v.Body != "" {
		fmt.Fprintf(&b, "%s\n\n", v.Body)
	}
	for _, f := range v.Facts {
		fmt.Fprintf(&b, "%s: %s\n", f.Label, f.Value)
	}
	if len(v.Facts) > 0 {
		b.WriteString("\n")
	}
	if v.PageURL != "" {
		fmt.Fprintf(&b, "Current status: %s\n", v.PageURL)
	}
	fmt.Fprintf(&b, "\nYou receive this because %s subscribed to updates from %s.\nUnsubscribe: %s\n", v.Recipient, v.PageTitle, v.Unsubscribe)
	return b.String()
}

var noticeHTML = htmltemplate.Must(htmltemplate.New("notice").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.PageTitle}}</title></head>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:{{.Ink}}">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:24px 0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;overflow:hidden">
<tr><td style="height:4px;background:{{.AccentCSS}};font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="padding:24px 28px 8px">
<div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:{{.Muted}}">{{.PageTitle}} · {{.Label}}</div>
<h1 style="margin:6px 0 4px;font-size:20px;line-height:1.3;color:{{.Ink}}">{{.Headline}}</h1>
</td></tr>
{{if .Body}}<tr><td style="padding:8px 28px;font-size:14px;line-height:1.5;white-space:pre-line">{{.Body}}</td></tr>{{end}}
{{if .Facts}}<tr><td style="padding:8px 28px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;line-height:1.5">
{{range .Facts}}<tr><td style="padding:4px 12px 4px 0;color:{{$.Muted}};white-space:nowrap;vertical-align:top">{{.Label}}</td><td style="padding:4px 0">{{.Value}}</td></tr>
{{end}}</table></td></tr>{{end}}
{{if .PageURL}}<tr><td style="padding:16px 28px 24px"><a href="{{.PageURL}}" style="display:inline-block;padding:10px 18px;border-radius:6px;background:{{.AccentCSS}};color:#ffffff;font-weight:600;font-size:14px;text-decoration:none">View current status</a></td></tr>{{end}}
<tr><td style="padding:16px 28px;border-top:1px solid #e4e4e7;font-size:12px;line-height:1.5;color:{{.Muted}}">You receive this because {{.Recipient}} subscribed to updates from {{.PageTitle}}. <a href="{{.Unsubscribe}}" style="color:{{.Muted}}">Unsubscribe</a></td></tr>
</table></td></tr></table></body></html>
`))

func (v recipientView) html() (string, error) {
	var b bytes.Buffer
	err := noticeHTML.Execute(&b, struct {
		recipientView
		AccentCSS  htmltemplate.CSS
		Ink, Muted string
	}{v, htmltemplate.CSS(accent(v.Accent)), inkColor, mutedColor})
	return b.String(), err
}

// noticeMessage is one subscriber's copy of a notice, with their own
// unsubscribe link in the body and as List-Unsubscribe (RFC 8058 one-click).
func noticeMessage(n notice, to, unsubscribePage, oneClick string) (mail.Message, error) {
	v := recipientView{notice: n, Recipient: to, Unsubscribe: unsubscribePage}
	html, err := v.html()
	if err != nil {
		return mail.Message{}, fmt.Errorf("%w: render: %v", mail.ErrInvalidMessage, err)
	}
	return mail.Message{
		To:       []string{to},
		FromName: senderName(n.PageTitle),
		Subject:  n.subject(),
		Text:     v.text(),
		HTML:     html,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + oneClick + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, nil
}

// confirmView is the confirmation mail's model.
type confirmView struct {
	PageTitle string
	PageURL   string
	Accent    string
	Confirm   string
	Recipient string
}

func (v confirmView) subject() string {
	return truncate(oneLine("Confirm your subscription to "+v.PageTitle), 200)
}

func (v confirmView) text() string {
	return fmt.Sprintf(`Someone, hopefully you, asked to receive status updates from %s at %s.

To confirm, open this link within 48 hours and press Confirm:
%s

If you did not ask for this, ignore this message: nothing else will be sent to you.
`, v.PageTitle, v.Recipient, v.Confirm)
}

var confirmHTML = htmltemplate.Must(htmltemplate.New("confirm").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.PageTitle}}</title></head>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:{{.Ink}}">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:24px 0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;overflow:hidden">
<tr><td style="height:4px;background:{{.AccentCSS}};font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="padding:24px 28px 8px">
<div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:{{.Muted}}">{{.PageTitle}}</div>
<h1 style="margin:6px 0 4px;font-size:20px;line-height:1.3;color:{{.Ink}}">Confirm your subscription</h1>
</td></tr>
<tr><td style="padding:8px 28px;font-size:14px;line-height:1.5">Someone, hopefully you, asked to receive status updates from {{.PageTitle}} at {{.Recipient}}. Confirm within 48 hours to start receiving them.</td></tr>
<tr><td style="padding:16px 28px 8px"><a href="{{.Confirm}}" style="display:inline-block;padding:10px 18px;border-radius:6px;background:{{.AccentCSS}};color:#ffffff;font-weight:600;font-size:14px;text-decoration:none">Confirm subscription</a></td></tr>
<tr><td style="padding:16px 28px 24px;font-size:12px;line-height:1.5;color:{{.Muted}}">If you did not ask for this, ignore this message: nothing else will be sent to you.</td></tr>
</table></td></tr></table></body></html>
`))

// confirmMessage is the double opt-in mail. It carries no List-Unsubscribe:
// there is nothing to unsubscribe from until it is confirmed, and ignoring
// it is the opt-out.
func confirmMessage(v confirmView) (mail.Message, error) {
	var b bytes.Buffer
	if err := confirmHTML.Execute(&b, struct {
		confirmView
		AccentCSS  htmltemplate.CSS
		Ink, Muted string
	}{v, htmltemplate.CSS(accent(v.Accent)), inkColor, mutedColor}); err != nil {
		return mail.Message{}, fmt.Errorf("%w: render: %v", mail.ErrInvalidMessage, err)
	}
	return mail.Message{To: []string{v.Recipient}, FromName: senderName(v.PageTitle), Subject: v.subject(), Text: v.text(), HTML: b.String()}, nil
}
