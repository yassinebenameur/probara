package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/smtp"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
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
	_ = ctx

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

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}

	if m.cfg.UseTLS {
		if err := m.sendWithTLS(addr, auth, message, to); err != nil {
			return err
		}
		return renderErr
	}

	if err := smtp.SendMail(addr, auth, m.cfg.From, to, []byte(message)); err != nil {
		return err
	}

	return renderErr
}

func (m *SMTPMailer) sendWithTLS(addr string, auth smtp.Auth, message string, recipients []string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	if err != nil {
		return fmt.Errorf("failed to dial smtp server: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("failed to create smtp client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth failed: %w", err)
			}
		}
	}

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp from failed: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt failed: %w", err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data failed: %w", err)
	}

	if _, err := writer.Write([]byte(message)); err != nil {
		return fmt.Errorf("smtp write failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close failed: %w", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit failed: %w", err)
	}

	return nil
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
// message still goes out and the error is returned for logging.
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

// emailMessage is one outgoing alert message before MIME assembly.
type emailMessage struct {
	From     string
	FromName string
	To       []string
	Subject  string
	Text     string
	HTML     string
}

// buildEmailMessage assembles the RFC 5322 message. With an HTML part it emits
// multipart/alternative so rich clients render the branded email and text-only
// clients (and mail archives) still get the readable fallback; without one it
// emits a plain single-part message.
//
// Both parts are base64-encoded: quoting is what keeps a long styled line from
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

	if strings.TrimSpace(msg.HTML) == "" {
		headers = append(headers,
			"Content-Type: text/plain; charset=\"UTF-8\"",
			"Content-Transfer-Encoding: base64",
		)
		return strings.Join(headers, "\r\n") + "\r\n\r\n" + base64Body(msg.Text)
	}

	boundary := mimeBoundary()
	headers = append(headers, fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q", boundary))

	var b strings.Builder
	b.WriteString(strings.Join(headers, "\r\n"))
	b.WriteString("\r\n\r\n")
	// Least-rich part first: multipart/alternative is ordered by increasing
	// preference, so a client picks the last part it understands.
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Body(msg.Text))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Body(msg.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.String()
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

// encodeHeader applies RFC 2047 encoded-word wrapping when a header value is not
// pure ASCII, so a monitor named "Téléphonie SIP" survives the subject line.
func encodeHeader(value string) string {
	return mime.QEncoding.Encode("utf-8", value)
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
