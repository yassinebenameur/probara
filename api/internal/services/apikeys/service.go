package apikeys

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

var ErrAPIKeyNotFound = errors.New("api key not found")

// Service handles API key operations.
type Service struct {
	db *db.Client
}

// NewService creates a new API key service.
func NewService(dbClient *db.Client) *Service {
	return &Service{db: dbClient}
}

// ListAPIKeys returns API keys for a tenant with pagination.
func (s *Service) ListAPIKeys(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.ApiKeyListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE tenant_id = $1`, tenantID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count api keys: %w", err)
	}

	query := `
		SELECT id, name, key_prefix, created_at, revoked_at
		FROM api_keys
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	defer rows.Close()

	var items []models.ApiKey
	for rows.Next() {
		var item models.ApiKey
		var keyPrefix sql.NullString
		var revokedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.Name, &keyPrefix, &item.CreatedAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}
		if keyPrefix.Valid {
			item.KeyPrefix = keyPrefix.String
		}
		if revokedAt.Valid {
			item.RevokedAt = &revokedAt.Time
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate api keys: %w", err)
	}

	return &models.ApiKeyListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// CreateAPIKey creates a new API key for a tenant.
func (s *Service) CreateAPIKey(ctx context.Context, tenantID uuid.UUID, req *models.CreateApiKeyRequest) (*models.ApiKey, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("api key name is required")
	}

	plainKey, err := generateAPIKey()
	if err != nil {
		return nil, err
	}

	hashResult, err := auth.HashAPIKey(plainKey)
	if err != nil {
		return nil, fmt.Errorf("failed to hash api key: %w", err)
	}

	now := time.Now()
	keyID := uuid.New()

	query := `
		INSERT INTO api_keys (id, tenant_id, name, key_hash, key_prefix, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	if _, err := s.db.ExecContext(ctx, query, keyID, tenantID, name, hashResult.BcryptHash, hashResult.KeyPrefix, now); err != nil {
		return nil, fmt.Errorf("failed to create api key: %w", err)
	}

	return &models.ApiKey{
		ID:        keyID,
		Name:      name,
		KeyPrefix: hashResult.KeyPrefix,
		Key:       plainKey,
		CreatedAt: now,
	}, nil
}

// RevokeAPIKey revokes an API key for a tenant.
func (s *Service) RevokeAPIKey(ctx context.Context, tenantID, keyID uuid.UUID) error {
	query := `
		UPDATE api_keys
		SET revoked_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`

	result, err := s.db.ExecContext(ctx, query, keyID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}
	if rows == 0 {
		return ErrAPIKeyNotFound
	}

	return nil
}

func generateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate api key: %w", err)
	}
	return "pk_" + hex.EncodeToString(bytes), nil
}
