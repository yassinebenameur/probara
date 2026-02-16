package notifications

import (
	"encoding/json"
	"testing"
)

func TestParseEmailConfig_Empty(t *testing.T) {
	_, err := ParseEmailConfig(nil)
	if err == nil {
		t.Fatalf("expected error for empty config")
	}
}

func TestParseEmailConfig_InvalidJSON(t *testing.T) {
	_, err := ParseEmailConfig(json.RawMessage("{"))
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestParseEmailConfig_NormalizesRecipientsAndTemplates(t *testing.T) {
	raw := json.RawMessage(`{
		"to": [" Alice@example.com ", "bob@example.com", "BOB@example.com", "invalid"],
		"subject_template": "Primary subject",
		"body_template": "Primary body"
	}`)

	cfg, err := ParseEmailConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.To) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(cfg.To))
	}
	if cfg.To[0] != "Alice@example.com" || cfg.To[1] != "bob@example.com" {
		t.Fatalf("unexpected recipients: %v", cfg.To)
	}
	if cfg.SubjectTemplate != "Primary subject" {
		t.Fatalf("expected subject template, got %q", cfg.SubjectTemplate)
	}
	if cfg.BodyTemplate != "Primary body" {
		t.Fatalf("expected body template, got %q", cfg.BodyTemplate)
	}
}

func TestParseEmailConfig_PrimaryTemplateOverridesFallback(t *testing.T) {
	raw := json.RawMessage(`{
		"to": ["user@example.com"],
		"subject_template": "Primary subject",
		"subject": "Fallback subject",
		"body_template": "Primary body",
		"body": "Fallback body"
	}`)

	cfg, err := ParseEmailConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SubjectTemplate != "Primary subject" {
		t.Fatalf("expected primary subject, got %q", cfg.SubjectTemplate)
	}
	if cfg.BodyTemplate != "Primary body" {
		t.Fatalf("expected primary body, got %q", cfg.BodyTemplate)
	}
}

func TestNormalizeTemplateValue_Fallbacks(t *testing.T) {
	if got := normalizeTemplateValue("  primary ", "fallback"); got != "  primary " {
		t.Fatalf("expected primary value, got %q", got)
	}
	if got := normalizeTemplateValue("", "fallback", ""); got != "fallback" {
		t.Fatalf("expected fallback value, got %q", got)
	}
	if got := normalizeTemplateValue("   ", "   "); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestNormalizeEmails_DedupAndValidate(t *testing.T) {
	emails := []string{" Alice@example.com ", "bob@example.com", "BOB@example.com", "invalid", ""}
	normalized := normalizeEmails(emails)

	if len(normalized) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(normalized))
	}
	if normalized[0] != "Alice@example.com" || normalized[1] != "bob@example.com" {
		t.Fatalf("unexpected normalized emails: %v", normalized)
	}
}
