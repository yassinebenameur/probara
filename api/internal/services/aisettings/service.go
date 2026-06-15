// Package aisettings manages per-tenant LLM configuration for AI root cause
// analysis. The API key is encrypted at rest and never returned to clients
// (Get reports only whether a key is set). When a tenant has no enabled row,
// callers fall back to the LLM_* environment defaults.
package aisettings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// validProviders are the selectable LLM vendors. Additional vendors are added
// here and in shared/ai newProvider; the UI reads this list shape implicitly.
var validProviders = map[string]bool{
	"openai_compat": true,
}

var validJSONModes = map[string]bool{
	"off":         true,
	"json_object": true,
	"json_schema": true,
}

// Settings is the client-facing view. The API key is masked to HasAPIKey.
type Settings struct {
	Enabled        bool   `json:"enabled"`
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	JSONMode       string `json:"json_mode"`
	MaxTokens      int    `json:"max_tokens"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	HasAPIKey      bool   `json:"has_api_key"`
}

// UpdateRequest carries partial updates; nil pointers are left unchanged.
// APIKey uses three-state semantics: nil = unchanged, "" = clear, value = set.
type UpdateRequest struct {
	Enabled        *bool   `json:"enabled,omitempty"`
	Provider       *string `json:"provider,omitempty"`
	BaseURL        *string `json:"base_url,omitempty"`
	Model          *string `json:"model,omitempty"`
	JSONMode       *string `json:"json_mode,omitempty"`
	MaxTokens      *int    `json:"max_tokens,omitempty"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty"`
	APIKey         *string `json:"api_key,omitempty"`
}

// TestRequest tests an arbitrary configuration (typically the values in the
// edit form). An empty APIKey falls back to the stored key for that tenant.
type TestRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	JSONMode string `json:"json_mode"`
	APIKey   string `json:"api_key"`
}

// TestResult is the outcome of a connectivity probe.
type TestResult struct {
	OK      bool   `json:"ok"`
	Model   string `json:"model,omitempty"`
	Message string `json:"message,omitempty"`
}

type record struct {
	Enabled        bool
	Provider       string
	BaseURL        string
	APIKeyEnc      string
	Model          string
	JSONMode       string
	MaxTokens      int
	TimeoutSeconds int
}

func defaultRecord() record {
	return record{Provider: "openai_compat", JSONMode: "off", MaxTokens: 1024, TimeoutSeconds: 60}
}

// Service provides per-tenant AI settings CRUD plus a connectivity probe.
type Service struct {
	db          *shareddb.Client
	encryptor   secrets.Encryptor
	envFallback bool
}

// NewService creates a Service. envFallback is true when the LLM_* environment
// defaults are configured, so EffectiveEnabled returns true even without a row.
func NewService(db *shareddb.Client, encryptor secrets.Encryptor, envFallback bool) *Service {
	if encryptor == nil {
		encryptor = secrets.NoOpEncryptor{}
	}
	return &Service{db: db, encryptor: encryptor, envFallback: envFallback}
}

func (s *Service) loadRecord(ctx context.Context, tenantID uuid.UUID) (record, bool, error) {
	rec := defaultRecord()
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, provider, base_url, api_key_encrypted, model, json_mode, max_tokens, timeout_seconds
		FROM ai_settings WHERE tenant_id = $1`, tenantID).Scan(
		&rec.Enabled, &rec.Provider, &rec.BaseURL, &rec.APIKeyEnc, &rec.Model, &rec.JSONMode, &rec.MaxTokens, &rec.TimeoutSeconds)
	if err == sql.ErrNoRows {
		return defaultRecord(), false, nil
	}
	if err != nil {
		return record{}, false, fmt.Errorf("load ai settings: %w", err)
	}
	return rec, true, nil
}

func toSettings(rec record) *Settings {
	return &Settings{
		Enabled:        rec.Enabled,
		Provider:       rec.Provider,
		BaseURL:        rec.BaseURL,
		Model:          rec.Model,
		JSONMode:       rec.JSONMode,
		MaxTokens:      rec.MaxTokens,
		TimeoutSeconds: rec.TimeoutSeconds,
		HasAPIKey:      rec.APIKeyEnc != "",
	}
}

// Get returns the tenant's settings (masked), or defaults when unset.
func (s *Service) Get(ctx context.Context, tenantID uuid.UUID) (*Settings, error) {
	rec, _, err := s.loadRecord(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return toSettings(rec), nil
}

// Update merges partial changes and upserts the row.
func (s *Service) Update(ctx context.Context, tenantID uuid.UUID, req UpdateRequest) (*Settings, error) {
	rec, _, err := s.loadRecord(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	if req.Provider != nil {
		p := strings.TrimSpace(*req.Provider)
		if p != "" && !validProviders[p] {
			return nil, fmt.Errorf("unsupported provider %q", p)
		}
		if p != "" {
			rec.Provider = p
		}
	}
	if req.BaseURL != nil {
		rec.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.Model != nil {
		rec.Model = strings.TrimSpace(*req.Model)
	}
	if req.JSONMode != nil {
		m := strings.TrimSpace(*req.JSONMode)
		if m != "" && !validJSONModes[m] {
			return nil, fmt.Errorf("invalid json_mode %q", m)
		}
		if m != "" {
			rec.JSONMode = m
		}
	}
	if req.MaxTokens != nil {
		if *req.MaxTokens <= 0 {
			return nil, fmt.Errorf("max_tokens must be > 0")
		}
		rec.MaxTokens = *req.MaxTokens
	}
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds <= 0 {
			return nil, fmt.Errorf("timeout_seconds must be > 0")
		}
		rec.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.APIKey != nil {
		if *req.APIKey == "" {
			rec.APIKeyEnc = ""
		} else {
			enc, err := s.encryptor.Encrypt(*req.APIKey)
			if err != nil {
				return nil, fmt.Errorf("encrypt api key: %w", err)
			}
			rec.APIKeyEnc = enc
		}
	}

	// Guard against enabling an unusable config.
	if rec.Enabled && rec.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required to enable AI analysis")
	}
	if rec.Enabled && rec.Model == "" {
		return nil, fmt.Errorf("model is required to enable AI analysis")
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_settings (tenant_id, enabled, provider, base_url, api_key_encrypted, model, json_mode, max_tokens, timeout_seconds, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			provider = EXCLUDED.provider,
			base_url = EXCLUDED.base_url,
			api_key_encrypted = EXCLUDED.api_key_encrypted,
			model = EXCLUDED.model,
			json_mode = EXCLUDED.json_mode,
			max_tokens = EXCLUDED.max_tokens,
			timeout_seconds = EXCLUDED.timeout_seconds,
			updated_at = NOW()`,
		tenantID, rec.Enabled, rec.Provider, rec.BaseURL, rec.APIKeyEnc, rec.Model, rec.JSONMode, rec.MaxTokens, rec.TimeoutSeconds); err != nil {
		return nil, fmt.Errorf("upsert ai settings: %w", err)
	}

	return toSettings(rec), nil
}

// EffectiveEnabled reports whether AI analysis can run for the tenant: either an
// enabled, usable row exists, or the environment fallback is configured.
func (s *Service) EffectiveEnabled(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	rec, found, err := s.loadRecord(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if found && rec.Enabled && rec.BaseURL != "" && rec.Model != "" {
		return true, nil
	}
	return s.envFallback, nil
}

// Test runs a connectivity probe against the provided config. When APIKey is
// empty, the stored (decrypted) key for the tenant is used so the user can test
// without re-entering it.
func (s *Service) Test(ctx context.Context, tenantID uuid.UUID, req TestRequest) TestResult {
	apiKey := req.APIKey
	if apiKey == "" {
		if rec, found, err := s.loadRecord(ctx, tenantID); err == nil && found && rec.APIKeyEnc != "" {
			if dec, derr := s.encryptor.Decrypt(rec.APIKeyEnc); derr == nil {
				apiKey = dec
			}
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	model, err := ai.Probe(probeCtx, ai.Config{
		Provider: req.Provider,
		BaseURL:  req.BaseURL,
		APIKey:   apiKey,
		Model:    req.Model,
		JSONMode: req.JSONMode,
	})
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	return TestResult{OK: true, Model: model, Message: "Connected successfully"}
}
