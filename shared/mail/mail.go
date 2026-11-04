// Package mail is the transactional-mail seam: invites, password resets,
// status-page subscriber mail and scheduled reports build a Message and hand
// it to a Sender. The SMTP implementation is email.SMTPMailer
// (shared/notifications/plugin/builtin/email), the same mailer alert email
// uses; alert delivery itself stays on its SendAlert path.
package mail

import (
	"context"
	"errors"
	"fmt"
	"mime"
	netmail "net/mail"
	"net/textproto"
	"strings"
)

// Sender delivers one transactional message.
type Sender interface {
	SendMessage(ctx context.Context, msg Message) error
}

// Message is one transactional email. From is the mailer's configured sender.
// To is required and never falls back to the alert default-recipient list.
// Text is the required plain-text part; HTML, when set, makes the body
// multipart/alternative, and Attachments wrap it in multipart/mixed.
type Message struct {
	To          []string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
	// Headers are extra header fields, e.g. List-Unsubscribe and
	// List-Unsubscribe-Post. Fields the mailer sets itself (see
	// reservedHeaders) are refused.
	Headers map[string]string
}

// Attachment is one file attached to a Message. ContentType defaults to
// application/octet-stream.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ErrInvalidMessage marks a message that can never be sent as built, so a
// caller knows not to retry it.
var ErrInvalidMessage = errors.New("invalid mail message")

// reservedHeaders are set by the mailer. Accepting them from Headers would
// duplicate or override them, and Bcc/Cc would leak or skip recipients.
var reservedHeaders = map[string]bool{
	"From": true, "Sender": true, "To": true, "Cc": true, "Bcc": true,
	"Subject": true, "Date": true, "Message-Id": true, "Mime-Version": true,
	"Content-Type": true, "Content-Transfer-Encoding": true, "Content-Disposition": true,
	"Return-Path": true, "Auto-Submitted": true, "X-Auto-Response-Suppress": true,
}

// Validate reports why msg cannot be sent, wrapping ErrInvalidMessage. Every
// header value, address and filename is refused if it contains CR, LF or
// another control character, which is what keeps caller input from injecting
// header lines.
func (m Message) Validate() error {
	if len(m.To) == 0 {
		return invalid("at least one recipient is required")
	}
	for _, to := range m.To {
		if _, err := ParseRecipient(to); err != nil {
			return err
		}
	}
	if hasControl(m.Subject) {
		return invalid("subject contains a control character")
	}
	if strings.TrimSpace(m.Text) == "" {
		return invalid("a plain-text body is required")
	}
	seen := make(map[string]bool, len(m.Headers))
	for name, value := range m.Headers {
		if !validFieldName(name) {
			return invalid("header name %q is not a valid field name", name)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if reservedHeaders[canonical] {
			return invalid("header %q is set by the mailer", name)
		}
		if seen[canonical] {
			return invalid("header %q is given twice", name)
		}
		seen[canonical] = true
		if hasControl(value) {
			return invalid("header %q contains a control character", name)
		}
	}
	for i, a := range m.Attachments {
		if strings.TrimSpace(a.Filename) == "" {
			return invalid("attachment %d has no filename", i)
		}
		if hasControl(a.Filename) {
			return invalid("attachment %q filename contains a control character", a.Filename)
		}
		if a.ContentType != "" {
			if hasControl(a.ContentType) {
				return invalid("attachment %q content type contains a control character", a.Filename)
			}
			if _, _, err := mime.ParseMediaType(a.ContentType); err != nil {
				return invalid("attachment %q content type %q: %v", a.Filename, a.ContentType, err)
			}
		}
	}
	return nil
}

// ParseRecipient parses one To entry ("ops@example.com" or
// "Ops <ops@example.com>"), refusing control characters.
func ParseRecipient(value string) (*netmail.Address, error) {
	if hasControl(value) {
		return nil, invalid("recipient %q contains a control character", value)
	}
	addr, err := netmail.ParseAddress(value)
	if err != nil {
		return nil, invalid("recipient %q: %v", value, err)
	}
	return addr, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidMessage, fmt.Sprintf(format, args...))
}

// hasControl reports ASCII control characters (CR and LF among them); tab is
// allowed, as header folding permits it.
func hasControl(s string) bool {
	for _, r := range s {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

// validFieldName follows RFC 5322 field-name: printable ASCII except colon.
func validFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
