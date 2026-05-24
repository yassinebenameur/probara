// Package webhook implements the generic outbound webhook alert plugin. It
// POSTs the full AlertEvent payload as JSON to a user-supplied URL, optionally
// signed with HMAC-SHA256 so the receiver can verify authenticity.
//
// The HMAC scheme follows GitHub's webhook convention: the receiver computes
// HMAC_SHA256(secret, body) and compares it to the value in the
// X-Probara-Signature header (prefixed with "sha256=").
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const (
	pluginType       = "generic_webhook"
	signatureHeader  = "X-Probara-Signature"
	eventTypeHeader  = "X-Probara-Event-Type"
	idempotencyHdr   = "X-Probara-Idempotency-Key"
	signaturePrefix  = "sha256="
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	URL            string            `json:"url"`
	HMACSecret     string            `json:"hmac_secret,omitempty"`
	CustomHeadersJ string            `json:"custom_headers,omitempty"` // raw JSON object string from the UI textarea
	customHeaders  map[string]string // parsed at Validate/Send time, not persisted
}

// Plugin is the generic webhook alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the default HTTP client (10s timeout).
func New() *Plugin {
	return &Plugin{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Manifest returns the generic webhook plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Generic Webhook",
		Description: "POST the full alert event payload as JSON to any HTTPS endpoint, optionally signed with HMAC-SHA256.",
		IconKey:     "webhook",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityRawEvent,
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "url",
				Label:       "Webhook URL",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "https://example.com/hooks/probara",
				Help:        "Stored encrypted at rest. Must use https.",
			},
			{
				Key:         "hmac_secret",
				Label:       "HMAC Secret",
				Type:        plugin.FieldTypeSecret,
				Secret:      true,
				Placeholder: "(optional) shared secret for HMAC-SHA256 body signing",
				Help:        "Receivers verify by computing HMAC_SHA256(secret, body) and comparing against the X-Probara-Signature header.",
			},
			{
				Key:         "custom_headers",
				Label:       "Custom Headers (JSON)",
				Type:        plugin.FieldTypeTextarea,
				Placeholder: `{"X-Source":"probara","Authorization":"Bearer ..."}`,
				Help:        "Optional flat JSON object of additional headers to send with each request.",
			},
		},
	}
}

// Validate checks the raw config blob before persisting.
func (p *Plugin) Validate(raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if cfg.URL == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return fmt.Errorf("url is not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("url must use https")
	}
	if cfg.CustomHeadersJ != "" {
		if _, err := parseCustomHeaders(cfg.CustomHeadersJ); err != nil {
			return fmt.Errorf("custom_headers: %w", err)
		}
	}
	return nil
}

// Send POSTs the AlertEvent to the configured URL with optional HMAC signing.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	target, ok := stringFromMap(req.Channel.Config, "url")
	if !ok || target == "" {
		return errors.New("generic_webhook channel missing url")
	}

	body, err := json.Marshal(req.Event)
	if err != nil {
		return fmt.Errorf("marshal alert event: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(eventTypeHeader, eventTypeOf(req))
	httpReq.Header.Set(idempotencyHdr, fmt.Sprintf("%s:%s:%s", req.Event.Alert.ID, req.Channel.ID, eventTypeOf(req)))

	if secret, ok := stringFromMap(req.Channel.Config, "hmac_secret"); ok && secret != "" {
		httpReq.Header.Set(signatureHeader, signaturePrefix+computeSignature(secret, body))
	}

	if headersRaw, ok := stringFromMap(req.Channel.Config, "custom_headers"); ok && headersRaw != "" {
		extra, err := parseCustomHeaders(headersRaw)
		if err != nil {
			return fmt.Errorf("parse custom_headers: %w", err)
		}
		for k, v := range extra {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func computeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func parseCustomHeaders(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(trimmed), &headers); err != nil {
		return nil, fmt.Errorf("must be a flat JSON object of strings: %w", err)
	}
	for k := range headers {
		if strings.TrimSpace(k) == "" {
			return nil, errors.New("header keys must be non-empty")
		}
	}
	return headers, nil
}

func eventTypeOf(req plugin.DispatchRequest) string {
	if req.EventType != "" {
		return req.EventType
	}
	return req.Event.Type
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("generic_webhook config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid generic_webhook config: %w", err)
	}
	cfg.URL = strings.TrimSpace(cfg.URL)
	return cfg, nil
}

func stringFromMap(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func init() {
	plugin.Register(New())
}

// silence unused-field warning — CustomHeadersJ is the persisted form;
// customHeaders is only used as a transient parse target inside this file.
var _ = Config{}.customHeaders
