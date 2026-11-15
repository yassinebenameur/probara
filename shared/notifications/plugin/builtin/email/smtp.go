package email

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// SMTPParams are the inputs required to construct an SMTPMailer. Both the
// alerter and worker binaries build this struct from their respective config
// objects (AlerterConfig / WorkerConfig) at startup.
type SMTPParams struct {
	Host             string
	Port             int
	Username         string
	Password         string
	From             string
	UseTLS           bool
	DefaultRecipient string // comma-separated fallback list when a channel omits "to"
	// FromName is the display name on the From header, e.g. "Probara Alerts".
	// Empty sends a bare address.
	FromName string
	// AppBaseURL is the public origin of the operator UI. When set, alert emails
	// carry a deep link to the monitor; when empty the button is omitted rather
	// than pointing at a guessed host.
	AppBaseURL string
}

// SMTPMailer is the SMTP backend that satisfies email.Mailer. Living in the
// email plugin package lets both alerter and worker binaries call
// email.NewSMTPMailer + email.SetMailer without cross-imports.
type SMTPMailer struct {
	cfg SMTPParams
	to  []string
}

// NewSMTPMailer constructs a mailer. Returns an error when required fields are
// missing so misconfiguration fails loudly at startup instead of silently
// dropping alerts at delivery time.
func NewSMTPMailer(params SMTPParams) (*SMTPMailer, error) {
	if params.Host == "" || params.From == "" {
		return nil, fmt.Errorf("smtp configuration is incomplete: host and from are required")
	}
	if params.Port <= 0 {
		params.Port = 587
	}
	return &SMTPMailer{
		cfg: params,
		to:  parseRecipients(params.DefaultRecipient),
	}, nil
}

// SendAlert renders subject/body via the email plugin's template engine and
// dispatches the message over SMTP.
func (m *SMTPMailer) SendAlert(ctx context.Context, event notifications.AlertEvent, templates Templates, recipients []string) error {
	to := recipients
	if len(to) == 0 {
		to = m.to
	}
	if len(to) == 0 {
		return fmt.Errorf("email recipients are required")
	}

	subject, subjectErr := RenderSubject(event, templates, m.cfg.AppBaseURL)
	body, bodyErr := RenderBody(event, templates, m.cfg.AppBaseURL)
	html, htmlErr := RenderHTML(event, templates, m.cfg.AppBaseURL)
	renderErr := combineTemplateErrors(subjectErr, bodyErr, htmlErr)
	message := buildEmailMessage(emailMessage{
		From:     m.cfg.From,
		FromName: m.cfg.FromName,
		To:       to,
		Subject:  subject,
		Text:     body,
		HTML:     html,
	})

	if err := m.deliver(ctx, message, to); err != nil {
		return err
	}
	return deliveredDespite(renderErr)
}

var _ mail.Sender = (*SMTPMailer)(nil)

// SendMessage delivers a transactional message (invites, password resets,
// subscriber mail, reports) as one SMTP transaction per recipient, each copy
// addressed to that recipient alone: alert email lists a channel's whole team
// in one To header, but transactional mail goes to people who do not know
// each other (status-page subscribers, report recipients outside the tenant).
// Unlike SendAlert it never falls back to the default recipients, and an
// invalid message (mail.ErrInvalidMessage) is refused before anything is
// dialled. A recipient listed twice is sent one copy.
//
// The transactions share one authenticated connection, since hosted relays
// throttle a sender that opens a session per message. A refused recipient is
// RSET and the rest carry on. When the connection is lost the session is
// reopened; the recipient in flight is retried on it only if the loss came
// before the end of its DATA, when the server cannot have accepted it, so
// nobody gets two copies. If the session cannot be reopened, every remaining
// recipient fails. Failures come back as a *mail.DeliveryError so a retry can
// skip those already served.
func (m *SMTPMailer) SendMessage(ctx context.Context, msg mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	type recipient struct{ given, header, envelope string }
	recipients := make([]recipient, 0, len(msg.To))
	seen := make(map[string]bool, len(msg.To))
	for _, to := range msg.To {
		addr, err := mail.ParseRecipient(to)
		if err != nil {
			return err
		}
		key := strings.ToLower(addr.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		// addr.String() re-quotes and RFC 2047-encodes the display name, so
		// the header carries the parsed address, never the caller's raw text.
		recipients = append(recipients, recipient{given: to, header: foldEncodedWords(addr.String()), envelope: addr.Address})
	}

	var failed []mail.RecipientFailure
	failRest := func(from int, err error) {
		for _, r := range recipients[from:] {
			failed = append(failed, mail.RecipientFailure{Recipient: r.given, Err: err})
		}
	}
	var session *smtpSession
	defer func() {
		if session != nil {
			// Every message is already accepted or refused; a failed QUIT
			// changes neither.
			_ = session.quit(ctx)
		}
	}()
	retried := -1
	for i := 0; i < len(recipients); {
		r := recipients[i]
		if err := ctx.Err(); err != nil {
			failRest(i, err)
			break
		}
		if session == nil {
			var err error
			if session, err = m.openSMTPSession(ctx); err != nil {
				failRest(i, err)
				break
			}
		}
		message := buildEmailMessage(emailMessage{
			From:        m.cfg.From,
			FromName:    m.cfg.FromName,
			To:          []string{r.header},
			Subject:     msg.Subject,
			Text:        msg.Text,
			HTML:        msg.HTML,
			Headers:     msg.Headers,
			Attachments: msg.Attachments,
		})
		stage, err := session.send(ctx, m.cfg.From, []string{r.envelope}, message)
		if err == nil {
			i++
			continue
		}
		if connectionLost(err) {
			session.close()
			session = nil
			if stage == stageBeforeData && retried != i && ctx.Err() == nil {
				retried = i
				continue
			}
		} else if rerr := session.reset(ctx); rerr != nil {
			session.close()
			session = nil
		}
		failed = append(failed, mail.RecipientFailure{Recipient: r.given, Err: err})
		i++
	}
	if len(failed) > 0 {
		return &mail.DeliveryError{Failed: failed}
	}
	return nil
}

// deliver sends one assembled message to every recipient in to over a
// session of its own (implicit TLS when UseTLS).
func (m *SMTPMailer) deliver(ctx context.Context, message string, to []string) error {
	session, err := m.openSMTPSession(ctx)
	if err != nil {
		return err
	}
	if _, err := session.send(ctx, m.cfg.From, to, message); err != nil {
		session.close()
		return err
	}
	return session.quit(ctx)
}

// deliveredDespite reports a template failure on a message that already went
// out. It must be Permanent: a plain error reads as transient, so the alerter
// rolls its claim back and the worker redelivers, and every recipient gets the
// same email again each cycle. Re-rendering fails the same way, so nothing is
// gained by retrying; the error only needs to reach the log (and a test send's
// caller, so the operator sees the broken template).
func deliveredDespite(renderErr error) error {
	if renderErr == nil {
		return nil
	}
	return plugin.Permanent(fmt.Errorf("email sent with built-in fallback: %w", renderErr))
}

func parseRecipients(value string) []string {
	parts := strings.Split(value, ",")
	var recipients []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			recipients = append(recipients, trimmed)
		}
	}
	return recipients
}

// combineTemplateErrors reports every rendering failure at once. A failure is
// non-fatal — the affected part falls back to the built-in rendering — so the
// message still goes out and the error is returned, as Permanent
// (deliveredDespite), for logging.
func combineTemplateErrors(errs ...error) error {
	var parts []string
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("template rendering failed: %s", strings.Join(parts, "; "))
}

// emailMessage is one outgoing message before MIME assembly.
type emailMessage struct {
	From     string
	FromName string
	To       []string
	Subject  string
	Text     string
	HTML     string
	// Headers and Attachments come only from SendMessage, already checked by
	// mail.Message.Validate (no CR/LF, no mailer-owned field names).
	Headers     map[string]string
	Attachments []mail.Attachment
}

// buildEmailMessage assembles the RFC 5322 message. With an HTML part it emits
// multipart/alternative so rich clients render the branded email and text-only
// clients (and mail archives) still get the readable fallback; without one it
// emits a plain single-part message. Attachments wrap that body in
// multipart/mixed, the body first.
//
// Every part is base64-encoded: quoting is what keeps a long styled line from
// tripping the SMTP 998-octet line limit, and it makes the encoding correct for
// any UTF-8 content (accented monitor names, σ, →) without per-character work.
func buildEmailMessage(msg emailMessage) string {
	headers := []string{
		"From: " + formatAddress(msg.From, msg.FromName),
		"To: " + strings.Join(msg.To, ", "),
		"Subject: " + encodeHeader(msg.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + messageID(msg.From),
		"MIME-Version: 1.0",
		"Auto-Submitted: auto-generated",
		"X-Auto-Response-Suppress: All",
	}
	names := make([]string, 0, len(msg.Headers))
	for name := range msg.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		headers = append(headers, textproto.CanonicalMIMEHeaderKey(name)+": "+encodeHeader(msg.Headers[name]))
	}

	bodyHeaders, body := buildBody(msg.Text, msg.HTML)
	if len(msg.Attachments) == 0 {
		headers = append(headers, bodyHeaders...)
		return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
	}

	boundary := mimeBoundary()
	headers = append(headers, fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q", boundary))
	var b strings.Builder
	b.WriteString(strings.Join(headers, "\r\n"))
	b.WriteString("\r\n\r\n--" + boundary + "\r\n")
	b.WriteString(strings.Join(bodyHeaders, "\r\n"))
	b.WriteString("\r\n\r\n")
	b.WriteString(body)
	for _, a := range msg.Attachments {
		b.WriteString("\r\n--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + attachmentContentType(a.ContentType) + "\r\n")
		b.WriteString("Content-Disposition: " + mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}) + "\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(base64Body(string(a.Data)))
	}
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.String()
}

// buildBody returns the content headers and encoded body of the readable
// part: text/plain alone, or multipart/alternative when there is HTML.
func buildBody(text, html string) (headers []string, body string) {
	if strings.TrimSpace(html) == "" {
		return []string{
			"Content-Type: text/plain; charset=\"UTF-8\"",
			"Content-Transfer-Encoding: base64",
		}, base64Body(text)
	}

	boundary := mimeBoundary()
	var b strings.Builder
	// Least-rich part first: multipart/alternative is ordered by increasing
	// preference, so a client picks the last part it understands.
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Body(text))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Body(html))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []string{fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q", boundary)}, b.String()
}

// attachmentContentType re-renders a validated media type (dropping anything
// mime would not round-trip), defaulting to application/octet-stream.
func attachmentContentType(contentType string) string {
	const fallback = "application/octet-stream"
	if contentType == "" {
		return fallback
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fallback
	}
	if formatted := mime.FormatMediaType(mediaType, params); formatted != "" {
		return formatted
	}
	return fallback
}

// formatAddress renders a From header, RFC 2047-encoding the display name when
// it is not plain ASCII.
func formatAddress(addr, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return addr
	}
	return fmt.Sprintf("%s <%s>", encodeHeader(name), addr)
}

// maxRawHeaderValue is the longest printable-ASCII header value sent as it
// is. Anything longer is encoded and folded, so no header line nears the
// SMTP 998-octet limit.
const maxRawHeaderValue = 900

// encodeHeader applies RFC 2047 encoded-word wrapping when a header value is not
// pure ASCII, so a monitor named "Téléphonie SIP" survives the subject line.
// The encoder splits a long value into several encoded words but separates
// them with plain spaces, leaving one unbounded line; foldEncodedWords breaks
// the line between them. Decoders drop the whitespace between adjacent
// encoded words, so the folded value decodes to exactly the original.
func encodeHeader(value string) string {
	encoded := mime.QEncoding.Encode("utf-8", value)
	if encoded == value && len(value) > maxRawHeaderValue {
		// Printable ASCII with no spaces to fold at would still overflow
		// the line, so it is encoded too.
		return bEncodeFolded(value)
	}
	return foldEncodedWords(encoded)
}

// foldEncodedWords puts each RFC 2047 encoded word after the first on its own
// continuation line. An encoded word cannot contain "?= =?" (Q encoding
// escapes "?" and space), so the pattern only matches between words.
func foldEncodedWords(s string) string {
	return strings.ReplaceAll(s, "?= =?", "?=\r\n =?")
}

// bEncodeFolded encodes value as base64 encoded words of 45 bytes each (a
// 72-character word), one per line. Only called for printable ASCII, so a
// byte split never cuts a character in two.
func bEncodeFolded(value string) string {
	const chunk = 45
	var words []string
	for len(value) > chunk {
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(value[:chunk]))+"?=")
		value = value[chunk:]
	}
	words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(value))+"?=")
	return strings.Join(words, "\r\n ")
}

// base64Body encodes a part and hard-wraps to 76 columns per RFC 2045.
func base64Body(body string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	var b strings.Builder
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	return b.String()
}

func mimeBoundary() string {
	return "probara-" + randomToken(16)
}

// messageID gives every alert a stable, unique identifier, which spam filters
// and threading both expect. The domain is taken from the sender address.
func messageID(from string) string {
	domain := "probara.local"
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		domain = strings.Trim(from[at+1:], "<> ")
	}
	return fmt.Sprintf("<%s@%s>", randomToken(16), domain)
}

func randomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is not worth failing delivery over; the token only
		// needs to be unlikely to collide.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
