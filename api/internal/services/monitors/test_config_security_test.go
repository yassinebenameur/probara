package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestResolveTestConfigRejectsDifferentSavedType(t *testing.T) {
	repo := NewMockRepository()
	id, tenant := uuid.New(), uuid.New()
	repo.monitors[id] = &models.Monitor{ID: id, TenantID: tenant, Type: models.MonitorTypePostgres, Config: json.RawMessage(`{"password":"stored-secret"}`)}
	_, err := NewService(repo).ResolveTestConfig(context.Background(), tenant, &id, models.MonitorTypeRedis, json.RawMessage(`{"host":"other.example","password":"***"}`))
	if !errors.Is(err, ErrMonitorTypeMismatch) {
		t.Fatalf("expected type mismatch, got %v", err)
	}
}
