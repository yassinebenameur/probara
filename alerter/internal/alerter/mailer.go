package alerter

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
)

// Mailer defines the interface for sending alert notifications.
type Mailer interface {
	SendAlert(ctx context.Context, event AlertEvent, templates EmailTemplates, recipients []string) error
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	UseTLS   bool
}

// SMTPMailer sends alerts via SMTP.
type SMTPMailer struct {
	config SMTPConfig
}

type EmailTemplates struct {
	SubjectTemplate string
	BodyTemplate    string
}

// NewSMTPMailer creates a new SMTP mailer from config.
func NewSMTPMailer(cfg *config.AlerterConfig) (*SMTPMailer, error) {
	if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
		return nil, fmt.Errorf("smtp configuration is incomplete")
	}

	return &SMTPMailer{
		config: SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
			To:       parseRecipients(cfg.AlertEmailTo),
			UseTLS:   cfg.SMTPUseTLS,
		},
	}, nil
}

// SendAlert sends an alert email.
func (m *SMTPMailer) SendAlert(ctx context.Context, event AlertEvent, templates EmailTemplates, recipients []string) error {
	_ = ctx

	to := recipients
	if len(to) == 0 {
		to = m.config.To
	}
	if len(to) == 0 {
		return fmt.Errorf("email recipients are required")
	}

	subject, subjectErr := renderSubject(event, templates)
	body, bodyErr := renderBody(event, templates)
	renderErr := combineTemplateErrors(subjectErr, bodyErr)
	message := buildEmailMessage(m.config.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", m.config.Host, m.config.Port)
	var auth smtp.Auth
	if m.config.Username != "" {
		auth = smtp.PlainAuth("", m.config.Username, m.config.Password, m.config.Host)
	}

	if m.config.UseTLS {
		if err := m.sendWithTLS(addr, auth, message, to); err != nil {
			return err
		}
		return renderErr
	}

	if err := smtp.SendMail(addr, auth, m.config.From, to, []byte(message)); err != nil {
		return err
	}

	return renderErr
}

func (m *SMTPMailer) sendWithTLS(addr string, auth smtp.Auth, message string, recipients []string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.config.Host})
	if err != nil {
		return fmt.Errorf("failed to dial smtp server: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.config.Host)
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

	if err := client.Mail(m.config.From); err != nil {
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

func renderSubject(event AlertEvent, templates EmailTemplates) (string, error) {
	defaultSubject := defaultSubjectForEvent(event)
	if strings.TrimSpace(templates.SubjectTemplate) == "" {
		return defaultSubject, nil
	}

	return renderTemplate(templates.SubjectTemplate, defaultSubject, buildTemplateData(event))
}

func renderBody(event AlertEvent, templates EmailTemplates) (string, error) {
	defaultBody := defaultBodyForEvent(event)
	if strings.TrimSpace(templates.BodyTemplate) == "" {
		return defaultBody, nil
	}

	return renderTemplate(templates.BodyTemplate, defaultBody, buildTemplateData(event))
}

func renderTemplate(templateText string, fallback string, data map[string]interface{}) (string, error) {
	tmpl, err := template.New("alert").Option("missingkey=zero").Parse(templateText)
	if err != nil {
		return fallback, err
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, data); err != nil {
		return fallback, err
	}

	return builder.String(), nil
}

func buildTemplateData(event AlertEvent) map[string]interface{} {
	var lastError string
	if event.Alert.LastError != nil {
		lastError = *event.Alert.LastError
	}
	var resolvedAt string
	if event.Alert.ResolvedAt != nil {
		resolvedAt = event.Alert.ResolvedAt.Format(timeLayout)
	}

	return map[string]interface{}{
		"alert_id":      event.Alert.ID,
		"monitor_id":    event.Alert.MonitorID,
		"monitor_name":  event.Alert.MonitorName,
		"policy_id":     event.Alert.AlertPolicyID,
		"policy_name":   event.Alert.PolicyName,
		"status":        event.Alert.Status,
		"triggered_at":  event.Alert.TriggeredAt.Format(timeLayout),
		"resolved_at":   resolvedAt,
		"failure_count": event.Alert.FailureCount,
		"last_error":    lastError,
		"tenant_id":     event.TenantID,
		"event_type":    event.Type,
		"timestamp":     event.Timestamp.Format(timeLayout),
	}
}

func defaultSubjectForEvent(event AlertEvent) string {
	switch event.Type {
	case "resolved":
		return fmt.Sprintf("[Alert Resolved] %s", event.Alert.MonitorName)
	default:
		return fmt.Sprintf("[Alert Triggered] %s", event.Alert.MonitorName)
	}
}

func defaultBodyForEvent(event AlertEvent) string {
	lines := []string{
		fmt.Sprintf("Monitor: %s", event.Alert.MonitorName),
		fmt.Sprintf("Policy: %s", event.Alert.PolicyName),
		fmt.Sprintf("Status: %s", event.Alert.Status),
		fmt.Sprintf("Triggered At: %s", event.Alert.TriggeredAt.Format(timeLayout)),
		fmt.Sprintf("Failure Count: %d", event.Alert.FailureCount),
		fmt.Sprintf("Tenant ID: %s", event.TenantID),
	}

	if event.Alert.LastError != nil && *event.Alert.LastError != "" {
		lines = append(lines, fmt.Sprintf("Last Error: %s", *event.Alert.LastError))
	}
	if event.Alert.ResolvedAt != nil {
		lines = append(lines, fmt.Sprintf("Resolved At: %s", event.Alert.ResolvedAt.Format(timeLayout)))
	}

	return strings.Join(lines, "\n")
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

const timeLayout = time.RFC3339

func derefTemplate(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
