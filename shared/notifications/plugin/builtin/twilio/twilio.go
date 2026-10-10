// Package twilio implements the SMS alert plugin over Twilio's Messages API
// (https://www.twilio.com/docs/messaging/api/message-resource#create-a-message-resource).
//
// SMS is length-limited and billed per segment, so the body is the compact
// present.Message.Short form, and one request goes out per recipient.
// Delivery is at least once: when one recipient fails the whole send is
// retried, so recipients that already succeeded can receive a duplicate.
package twilio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const (
	pluginType = "twilio_sms"
	apiBase    = "https://api.twilio.com"
	// maxBody is Twilio's limit on a message body (it splits it into
	// segments on the carrier side).
	maxBody = 1600
	// maxRecipients bounds fan-out per channel; larger lists belong in a
	// paging tool.
	maxRecipients = 10
	// maxConcurrentSends caps in-flight Messages API calls per delivery,
	// well under Twilio's per-account concurrency limit.
	maxConcurrentSends = 5
)

var (
	accountSIDPattern = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
	apiKeySIDPattern  = regexp.MustCompile(`^SK[0-9a-fA-F]{32}$`)
	serviceSIDPattern = regexp.MustCompile(`^MG[0-9a-fA-F]{32}$`)
	e164Pattern       = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	AccountSID string `json:"account_sid"`
	// APIKeySID is optional: when set, APIKeySID/AuthToken is an API key pair
	// (recommended); otherwise AuthToken is the account's auth token.
	APIKeySID string `json:"api_key_sid,omitempty"`
	AuthToken string `json:"auth_token"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Plugin is the Twilio SMS alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
	// base overrides the API origin in tests.
	base string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second), base: apiBase}
}

// Manifest returns the plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "SMS (Twilio)",
		Description: "Text alerts to up to 10 phone numbers through your Twilio account.",
		IconKey:     "sms",
		DocsURL:     "https://www.twilio.com/docs/messaging",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "account_sid",
				Label:       "Account SID",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "AC…",
			},
			{
				Key:         "api_key_sid",
				Label:       "API key SID",
				Type:        plugin.FieldTypeString,
				Placeholder: "(optional) SK…",
				Help:        "Recommended: create a Standard API key and put its SID here and its secret below. Leave empty to authenticate with the account auth token.",
			},
			{
				Key:      "auth_token",
				Label:    "API key secret or auth token",
				Type:     plugin.FieldTypeSecret,
				Required: true,
				Secret:   true,
			},
			{
				Key:         "from",
				Label:       "From",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "+15551234567 or MG…",
				Help:        "A Twilio phone number in E.164 format, or a Messaging Service SID.",
			},
			{
				Key:         "to",
				Label:       "Recipients",
				Type:        plugin.FieldTypeTextarea,
				Required:    true,
				Placeholder: "+15557654321, +447700900123",
				Help:        "Up to 10 phone numbers in E.164 format, separated by commas or new lines. Each one is a billed message per alert event.",
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
	_, err = cfg.validate()
	return err
}

func (cfg Config) validate() ([]string, error) {
	if !accountSIDPattern.MatchString(cfg.AccountSID) {
		return nil, errors.New("account_sid must be AC followed by 32 hex characters")
	}
	if cfg.APIKeySID != "" && !apiKeySIDPattern.MatchString(cfg.APIKeySID) {
		return nil, errors.New("api_key_sid must be SK followed by 32 hex characters")
	}
	if cfg.AuthToken == "" {
		return nil, errors.New("auth_token is required")
	}
	if !e164Pattern.MatchString(cfg.From) && !serviceSIDPattern.MatchString(cfg.From) {
		return nil, errors.New("from must be an E.164 number (+15551234567) or a Messaging Service SID (MG…)")
	}
	to := splitNumbers(cfg.To)
	if len(to) == 0 {
		return nil, errors.New("to requires at least one phone number")
	}
	if len(to) > maxRecipients {
		return nil, fmt.Errorf("to accepts at most %d phone numbers", maxRecipients)
	}
	for _, n := range to {
		if !e164Pattern.MatchString(n) {
			return nil, fmt.Errorf("recipient %q is not an E.164 number (+15557654321)", n)
		}
	}
	return to, nil
}

// Send texts every recipient.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	raw, err := json.Marshal(req.Channel.Config)
	if err != nil {
		return plugin.Permanent(err)
	}
	cfg, err := parseConfig(raw)
	if err != nil {
		return plugin.Permanent(err)
	}
	to, err := cfg.validate()
	if err != nil {
		return plugin.Permanent(err)
	}

	user := cfg.AccountSID
	if cfg.APIKeySID != "" {
		user = cfg.APIKeySID
	}
	headers := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+cfg.AuthToken)),
	}
	target := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", p.base, url.PathEscape(cfg.AccountSID))
	body := truncate(req.View().Short(), maxBody)

	// Text recipients concurrently. One after another, a slow API spends the
	// dispatcher's deadline on the first few numbers, the rest fail with a
	// context timeout, and the redelivery re-texts (and re-bills) everyone
	// who already got it.
	results := make([]error, len(to))
	sem := make(chan struct{}, maxConcurrentSends)
	var wg sync.WaitGroup
	for i, number := range to {
		wg.Add(1)
		go func(i int, number string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			form := url.Values{"To": {number}, "Body": {body}}
			if strings.HasPrefix(cfg.From, "MG") {
				form.Set("MessagingServiceSid", cfg.From)
			} else {
				form.Set("From", cfg.From)
			}
			if err := plugin.Post(ctx, p.httpClient, target, "application/x-www-form-urlencoded", []byte(form.Encode()), headers, "twilio messages api"); err != nil {
				results[i] = fmt.Errorf("%s: %w", maskNumber(number), err)
			}
		}(i, number)
	}
	wg.Wait()
	var errs []error
	for _, err := range results {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	joined := errors.Join(errs...)
	// Retry unless every failure was permanent (bad credentials, invalid
	// numbers): a transient failure on one recipient is worth redelivering.
	// The mixed case must be flattened: plugin.IsPermanent walks a joined
	// error's children, so returning the join would let the permanent child
	// mark the whole delivery permanent and drop the retryable recipient.
	// The longest provider-requested delay (a 429's Retry-After) is carried
	// over so the redelivery respects it for every recipient.
	retryable := false
	var delay time.Duration
	for _, e := range errs {
		if plugin.IsPermanent(e) {
			continue
		}
		retryable = true
		if d, ok := plugin.RetryAfterDelay(e); ok && d > delay {
			delay = d
		}
	}
	if !retryable {
		return plugin.Permanent(joined)
	}
	flat := errors.New(joined.Error())
	if delay > 0 {
		return plugin.RetryAfter(flat, delay)
	}
	return flat
}

// maskNumber keeps phone numbers out of logs except the last digits.
func maskNumber(n string) string {
	if len(n) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(n)-4) + n[len(n)-4:]
}

func splitNumbers(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		n := strings.Join(strings.Fields(part), "")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("twilio config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid twilio config: %w", err)
	}
	cfg.AccountSID = strings.TrimSpace(cfg.AccountSID)
	cfg.APIKeySID = strings.TrimSpace(cfg.APIKeySID)
	cfg.AuthToken = strings.TrimSpace(cfg.AuthToken)
	cfg.From = strings.TrimSpace(cfg.From)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}
