package notifications

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EmailConfig represents the configuration for an email alert channel.
type EmailConfig struct {
	To              []string `json:"to"`
	SubjectTemplate string   `json:"subject_template,omitempty"`
	BodyTemplate    string   `json:"body_template,omitempty"`
}

type emailConfigAlt struct {
	To                   string `json:"to"`
	SubjectTemplate      string `json:"subject_template"`
	BodyTemplate         string `json:"body_template"`
	Subject              string `json:"subject"`
	Body                 string `json:"body"`
	EmailSubjectTemplate string `json:"email_subject_template"`
	EmailBodyTemplate    string `json:"email_body_template"`
}

// ParseEmailConfig parses an email config from JSON.
func ParseEmailConfig(raw json.RawMessage) (*EmailConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("email config is required")
	}

	var cfg EmailConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid email config: %w", err)
	}

	var alt emailConfigAlt
	if err := json.Unmarshal(raw, &alt); err == nil {
		if len(cfg.To) == 0 && strings.TrimSpace(alt.To) != "" {
			cfg.To = splitEmails(alt.To)
		}

		cfg.SubjectTemplate = normalizeTemplateValue(
			cfg.SubjectTemplate,
			alt.SubjectTemplate,
			alt.Subject,
			alt.EmailSubjectTemplate,
		)
		cfg.BodyTemplate = normalizeTemplateValue(
			cfg.BodyTemplate,
			alt.BodyTemplate,
			alt.Body,
			alt.EmailBodyTemplate,
		)
	}

	cfg.To = normalizeEmails(cfg.To)
	if len(cfg.To) == 0 {
		return nil, fmt.Errorf("email recipients are required")
	}

	return &cfg, nil
}

func splitEmails(value string) []string {
	parts := strings.Split(value, ",")
	var emails []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			emails = append(emails, trimmed)
		}
	}
	return emails
}

func normalizeEmails(emails []string) []string {
	var normalized []string
	seen := map[string]struct{}{}
	for _, email := range emails {
		trimmed := strings.TrimSpace(email)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "@") {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func normalizeTemplateValue(primary string, fallbacks ...string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	for _, value := range fallbacks {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
