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

// Sender delivers transactional mail.
type Sender interface {
	// SendMessage sends msg to every address in msg.To as a separate
	// message whose To header names that recipient alone, so recipients
	// never see each other. An invalid message (ErrInvalidMessage) is
	// refused before anything is sent. When some recipients fail, the error
	// is a *DeliveryError naming them; the others already have their copy.
	SendMessage(ctx context.Context, msg Message) error
}

// Message is one transactional email. From is the mailer's configured sender.
// To is required and never falls back to the alert default-recipient list.
// Text is the required plain-text part; HTML, when set, makes the body
// multipart/alternative, and Attachments wrap it in multipart/mixed.
//
// Every recipient gets the same headers, so mail whose headers differ per
// recipient (a List-Unsubscribe link carrying that subscriber's token) is
// sent as one Message per recipient.
type Message struct {
	To []string
	// FromName, when set, is the From header's display name for this
	// message (a status page's name on its subscriber mail); the address is
	// always the mailer's configured sender. Empty: the configured name.
	FromName    string
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

// ErrMaybeDelivered marks a recipient whose message was fully transmitted
// but whose acceptance was never confirmed (the connection dropped or timed
// out waiting for the reply to the end of data). The relay may have queued
// it, so a caller that must never mail anyone twice does not retry it.
var ErrMaybeDelivered = errors.New("mail may have been delivered")

// Length bounds that keep every header line the mailer writes under the
// SMTP 998-octet limit. The subject needs none: it is encoded and folded.
// A header value is sent as given, since List-Unsubscribe and similar fields
// must stay parseable; a filename or content type may triple in length once
// RFC 2231-encoded.
const (
	maxRecipientLen   = 512
	maxHeaderValueLen = 900
	maxFilenameLen    = 255
	maxContentTypeLen = 255
	maxFromNameLen    = 200
)

// RecipientFailure is one recipient a SendMessage could not reach.
type RecipientFailure struct {
	Recipient string
	Err       error
}

// DeliveryError reports the recipients of a SendMessage that failed. The
// recipients it does not list were sent their copy, so a retry addresses only
// Recipients() and nobody is mailed twice.
type DeliveryError struct {
	Failed []RecipientFailure
}

func (e *DeliveryError) Error() string {
	parts := make([]string, len(e.Failed))
	for i, f := range e.Failed {
		parts[i] = fmt.Sprintf("%s: %v", f.Recipient, f.Err)
	}
	return fmt.Sprintf("mail not delivered to %d recipient(s): %s", len(e.Failed), strings.Join(parts, "; "))
}

// Unwrap exposes each recipient's error to errors.Is and errors.As.
func (e *DeliveryError) Unwrap() []error {
	errs := make([]error, len(e.Failed))
	for i, f := range e.Failed {
		errs[i] = f.Err
	}
	return errs
}

// Recipients lists the failed recipients as they appeared in Message.To.
func (e *DeliveryError) Recipients() []string {
	out := make([]string, len(e.Failed))
	for i, f := range e.Failed {
		out[i] = f.Recipient
	}
	return out
}

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
	if hasControl(m.FromName) {
		return invalid("sender name contains a control character")
	}
	if len(m.FromName) > maxFromNameLen {
		return invalid("sender name is longer than %d bytes", maxFromNameLen)
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
		if len(value) > maxHeaderValueLen {
			return invalid("header %q is longer than %d bytes", name, maxHeaderValueLen)
		}
	}
	for i, a := range m.Attachments {
		if strings.TrimSpace(a.Filename) == "" {
			return invalid("attachment %d has no filename", i)
		}
		if hasControl(a.Filename) {
			return invalid("attachment %q filename contains a control character", a.Filename)
		}
		if len(a.Filename) > maxFilenameLen {
			return invalid("attachment %d filename is longer than %d bytes", i, maxFilenameLen)
		}
		if len(a.ContentType) > maxContentTypeLen {
			return invalid("attachment %q content type is longer than %d bytes", a.Filename, maxContentTypeLen)
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
// "Ops <ops@example.com>"), refusing control characters and any address
// that is not plain ASCII dot-atoms on both sides of the "@".
//
// net/mail accepts quoted local parts and returns them unquoted, so
// "attacker@evil.example>"@corp.example would come back as
// attacker@evil.example>@corp.example and smuggle a second address into
// RCPT TO:<...> and the To header. Without SMTPUTF8 the envelope must be
// ASCII as well, so an internationalised domain is given in its punycode
// form.
func ParseRecipient(value string) (*netmail.Address, error) {
	if hasControl(value) {
		return nil, invalid("recipient %q contains a control character", value)
	}
	if len(value) > maxRecipientLen {
		return nil, invalid("recipient is longer than %d bytes", maxRecipientLen)
	}
	addr, err := netmail.ParseAddress(value)
	if err != nil {
		return nil, invalid("recipient %q: %v", value, err)
	}
	at := strings.LastIndexByte(addr.Address, '@')
	if at < 0 || !isDotAtom(addr.Address[:at]) || !isDotAtom(addr.Address[at+1:]) {
		return nil, invalid("recipient %q: only plain addresses are accepted (letters, digits and !#$%%&'*+-/=?^_`{|}~ in dot-separated words)", value)
	}
	return addr, nil
}

// isDotAtom reports whether s is an RFC 5322 dot-atom of ASCII atext: no
// quotes, whitespace or <>()[],;:\" and no leading, trailing or doubled dot.
func isDotAtom(s string) bool {
	if s == "" || s[0] == '.' || s[len(s)-1] == '.' || strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.':
		case strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidMessage, fmt.Sprintf(format, args...))
}

// hasControl reports control characters (CR and LF among them, and the C1
// range, which holds NEL); tab is allowed, as header folding permits it.
func hasControl(s string) bool {
	for _, r := range s {
		if (r < 0x20 && r != '\t') || (r >= 0x7f && r <= 0x9f) {
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
