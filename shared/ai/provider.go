package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotConfigured is returned by NewAnalyzer when the LLM endpoint is not
// configured. Callers treat this as "feature disabled" rather than a failure.
var ErrNotConfigured = errors.New("ai: LLM provider not configured")

// Provider is the low-level transport: a single chat/completions round-trip.
// Concrete implementations (e.g. openaiCompatProvider) own the wire format;
// the analyzer owns prompt construction and result parsing. Adding support for
// a different vendor API is a new Provider implementation with no changes to
// callers.
type Provider interface {
	// Name identifies the provider implementation (for logging).
	Name() string
	// Complete sends one request and returns the model's raw text content.
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// CompletionRequest is a vendor-neutral completion request.
type CompletionRequest struct {
	Model     string
	System    string
	User      string
	MaxTokens int
	// JSONSchema, when set, asks the provider to constrain output to JSON.
	// Providers that don't support strict schemas fall back to plain JSON
	// mode; the analyzer parses defensively either way.
	JSONSchema json.RawMessage
	SchemaName string
}

// CompletionResponse is a vendor-neutral completion response.
type CompletionResponse struct {
	Content string
	Model   string
}

// Config selects and configures the provider, populated from env in
// shared/config. All fields except BaseURL have sensible defaults.
type Config struct {
	Provider  string        // default "openai_compat"
	BaseURL   string        // required; e.g. https://api.openai.com/v1
	APIKey    string        // optional (keyless local runtimes)
	Model     string        // model string passed through to the provider
	MaxTokens int           // default 1024
	Timeout   time.Duration // default 60s
	// JSONMode controls the response_format field sent to the provider:
	// "" / "off" (default) omits it — the prompt still describes the schema and
	// the result is parsed defensively, so this works against any endpoint.
	// "json_object" and "json_schema" opt into provider-native structured
	// output where supported. Some servers (e.g. certain SGLang builds) error
	// on response_format, so off is the portable default.
	JSONMode string
}

func (cfg *Config) applyDefaults() {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
}

// newProvider constructs the transport for the configured vendor. This is the
// single extension point for additional LLM vendors — add a case here and a
// new Provider implementation; nothing else changes.
func newProvider(cfg Config) (Provider, error) {
	switch strings.TrimSpace(cfg.Provider) {
	case "", "openai_compat", "openai":
		return newOpenAICompatProvider(cfg.BaseURL, cfg.APIKey, cfg.Timeout, cfg.JSONMode), nil
	default:
		return nil, fmt.Errorf("ai: unknown LLM provider %q", cfg.Provider)
	}
}

// NewAnalyzer builds a RootCauseAnalyzer from config. Returns ErrNotConfigured
// when no endpoint is set so callers can disable the feature cleanly.
func NewAnalyzer(cfg Config) (RootCauseAnalyzer, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("ai: LLM_MODEL is required")
	}
	cfg.applyDefaults()

	provider, err := newProvider(cfg)
	if err != nil {
		return nil, err
	}
	return &llmAnalyzer{
		provider:  provider,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
	}, nil
}

// Probe verifies connectivity and credentials against the configured endpoint
// with a tiny completion. It powers the UI "Test connection" button. On success
// it returns the model id the server reported (may differ from cfg.Model).
func Probe(ctx context.Context, cfg Config) (string, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return "", ErrNotConfigured
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return "", fmt.Errorf("ai: model is required")
	}
	cfg.applyDefaults()

	provider, err := newProvider(cfg)
	if err != nil {
		return "", err
	}
	resp, err := provider.Complete(ctx, CompletionRequest{
		Model:     cfg.Model,
		System:    "You are a health check. Reply with the single word: OK.",
		User:      "ping",
		MaxTokens: 16,
	})
	if err != nil {
		return "", err
	}
	if resp.Model != "" {
		return resp.Model, nil
	}
	return cfg.Model, nil
}
