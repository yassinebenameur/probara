package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openaiCompatProvider speaks the OpenAI Chat Completions protocol over HTTP
// using only the standard library — no vendor SDK. This single implementation
// covers OpenAI, Azure OpenAI, gateways, and self-hosted OpenAI-compatible
// runtimes (Ollama, vLLM, LM Studio, llama.cpp), which is what makes the
// feature wireable to "whatever LLM" by config.
type openaiCompatProvider struct {
	baseURL    string
	apiKey     string
	jsonMode   string
	httpClient *http.Client
}

func newOpenAICompatProvider(baseURL, apiKey string, timeout time.Duration, jsonMode string) *openaiCompatProvider {
	return &openaiCompatProvider{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		jsonMode:   strings.TrimSpace(jsonMode),
		httpClient: &http.Client{Timeout: timeout},
	}
}

// responseFormat builds the response_format payload for the configured JSON
// mode, or nil to omit it entirely (the portable default).
func (p *openaiCompatProvider) responseFormat(req CompletionRequest) json.RawMessage {
	if len(req.JSONSchema) == 0 {
		return nil
	}
	switch p.jsonMode {
	case "json_object":
		return json.RawMessage(`{"type":"json_object"}`)
	case "json_schema":
		name := req.SchemaName
		if name == "" {
			name = "result"
		}
		payload, err := json.Marshal(map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   name,
				"schema": req.JSONSchema,
				"strict": true,
			},
		})
		if err != nil {
			return nil
		}
		return json.RawMessage(payload)
	default:
		// "" / "off" / anything else → omit response_format. The system prompt
		// already describes the schema and parsing is defensive.
		return nil
	}
}

func (p *openaiCompatProvider) Name() string { return "openai_compat" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (p *openaiCompatProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	body := chatRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		ResponseFormat: p.responseFormat(req),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompletionResponse{}, fmt.Errorf("ai: provider returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return CompletionResponse{}, fmt.Errorf("ai: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return CompletionResponse{}, fmt.Errorf("ai: provider error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("ai: provider returned no choices")
	}

	return CompletionResponse{
		Content: parsed.Choices[0].Message.Content,
		Model:   parsed.Model,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
