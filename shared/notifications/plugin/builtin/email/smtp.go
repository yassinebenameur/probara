package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

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

	subject, subjectErr := RenderSubject(event, templates)
	body, bodyErr := RenderBody(event, templates)
	renderErr := combineTemplateErrors(subjectErr, bodyErr)
	message := buildEmailMessage(m.cfg.From, to, subject, body)

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

func combineTemplateErrors(subjectErr, bodyErr error) error {
	if subjectErr == nil && bodyErr == nil {
		return nil
	}
	if subjectErr != nil && bodyErr != nil {
		return fmt.Errorf("template rendering failed: subject: %v; body: %v", subjectErr, bodyErr)
	}
	if subjectErr != nil {
		return fmt.Errorf("subject template rendering failed: %w", subjectErr)
	}
	return fmt.Errorf("body template rendering failed: %w", bodyErr)
}

func buildEmailMessage(from string, to []string, subject string, body string) string {
	headers := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", strings.Join(to, ", ")),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=\"UTF-8\"",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}
