package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TeamsWebhookConfig represents the configuration for a Teams webhook channel.
type TeamsWebhookConfig struct {
	WebhookURL string `json:"webhook_url"`
}

// ParseTeamsWebhookConfig parses a Teams webhook config from JSON.
func ParseTeamsWebhookConfig(raw json.RawMessage) (*TeamsWebhookConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("teams config is required")
	}

	var cfg TeamsWebhookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid teams config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("webhook_url is required")
	}
	return &cfg, nil
}

// TeamsMessage represents a minimal Teams message card payload.
type TeamsMessage struct {
	Type     string         `json:"@type"`
	Context  string         `json:"@context"`
	Summary  string         `json:"summary,omitempty"`
	Title    string         `json:"title,omitempty"`
	Text     string         `json:"text,omitempty"`
	Sections []TeamsSection `json:"sections,omitempty"`
}

// TeamsSection represents a section within a Teams message card.
type TeamsSection struct {
	ActivityTitle    string      `json:"activityTitle,omitempty"`
	ActivitySubtitle string      `json:"activitySubtitle,omitempty"`
	Text             string      `json:"text,omitempty"`
	Facts            []TeamsFact `json:"facts,omitempty"`
	Markdown         bool        `json:"markdown,omitempty"`
}

// TeamsFact represents a name/value pair in a Teams message card.
type TeamsFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SendTeamsWebhook sends a message card to a Teams webhook URL.
func SendTeamsWebhook(ctx context.Context, webhookURL string, message TeamsMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal Teams payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create Teams webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Teams webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Teams webhook returned status %d", resp.StatusCode)
	}

	return nil
}
