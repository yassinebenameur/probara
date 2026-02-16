package models

import (
	"time"

	"github.com/google/uuid"
)

// ApiKey represents an API key (secret only returned on creation).
type ApiKey struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	KeyPrefix string     `json:"key_prefix"`
	Key       string     `json:"key,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// ApiKeyListResponse represents a list of API keys.
type ApiKeyListResponse struct {
	Items    []ApiKey `json:"items"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	Total    int      `json:"total"`
}

// CreateApiKeyRequest represents a request to create an API key.
type CreateApiKeyRequest struct {
	Name string `json:"name"`
}
