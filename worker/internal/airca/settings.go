package airca

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// resolveAnalyzer returns the analyzer to use for a tenant: a per-tenant
// ai_settings row if present and enabled, otherwise the env fallback (which may
// be nil). Reading the table directly keeps the worker independent of the API's
// internal packages.
func (c *Consumer) resolveAnalyzer(ctx context.Context, tenantID uuid.UUID) (ai.RootCauseAnalyzer, error) {
	var (
		enabled     bool
		provider    string
		baseURL     string
		apiKeyEnc   string
		model       string
		jsonMode    string
		maxTokens   int
		timeoutSecs int
	)
	err := c.db.QueryRowContext(ctx, `
		SELECT enabled, provider, base_url, api_key_encrypted, model, json_mode, max_tokens, timeout_seconds
		FROM ai_settings WHERE tenant_id = $1`, tenantID).Scan(
		&enabled, &provider, &baseURL, &apiKeyEnc, &model, &jsonMode, &maxTokens, &timeoutSecs)
	if err == sql.ErrNoRows {
		return c.envAnalyzer, nil
	}
	if err != nil {
		return nil, err
	}
	if !enabled || baseURL == "" || model == "" {
		return c.envAnalyzer, nil
	}

	apiKey := ""
	if apiKeyEnc != "" {
		decrypted, derr := c.encryptor.Decrypt(apiKeyEnc)
		if derr != nil {
			return nil, derr
		}
		apiKey = decrypted
	}

	analyzer, err := ai.NewAnalyzer(ai.Config{
		Provider:  provider,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		JSONMode:  jsonMode,
		MaxTokens: maxTokens,
		Timeout:   time.Duration(timeoutSecs) * time.Second,
	})
	if err != nil {
		// Misconfigured tenant row — fall back to env so a bad row doesn't break
		// everything; the per-tenant validation lives in the API.
		return c.envAnalyzer, nil
	}
	return analyzer, nil
}
