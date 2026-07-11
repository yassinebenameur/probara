package apikeys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestScopedAPIKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "keys-tenant")
	svc := NewService(dbClient)

	t.Run("default scope is write", func(t *testing.T) {
		key, err := svc.CreateAPIKey(ctx, tenantID, &models.CreateApiKeyRequest{Name: "default"})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		if key.Scope != auth.ScopeWrite {
			t.Fatalf("expected write scope, got %q", key.Scope)
		}

		identity, err := auth.ValidateAPIKey(ctx, dbClient, key.Key)
		if err != nil {
			t.Fatalf("ValidateAPIKey: %v", err)
		}
		if identity.Scope != auth.ScopeWrite || identity.TenantID != tenantID.String() {
			t.Fatalf("unexpected identity: %+v", identity)
		}
		if identity.KeyID != key.ID.String() {
			t.Fatalf("expected key ID %s, got %s", key.ID, identity.KeyID)
		}
	})

	t.Run("read scope round-trips", func(t *testing.T) {
		key, err := svc.CreateAPIKey(ctx, tenantID, &models.CreateApiKeyRequest{Name: "reader", Scope: auth.ScopeRead})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		identity, err := auth.ValidateAPIKey(ctx, dbClient, key.Key)
		if err != nil {
			t.Fatalf("ValidateAPIKey: %v", err)
		}
		if identity.Scope != auth.ScopeRead {
			t.Fatalf("expected read scope, got %q", identity.Scope)
		}
	})

	t.Run("invalid scope rejected", func(t *testing.T) {
		_, err := svc.CreateAPIKey(ctx, tenantID, &models.CreateApiKeyRequest{Name: "bad", Scope: "root"})
		if !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("expected ErrInvalidScope, got %v", err)
		}
	})

	t.Run("past expiry rejected on create", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		_, err := svc.CreateAPIKey(ctx, tenantID, &models.CreateApiKeyRequest{Name: "expired", ExpiresAt: &past})
		if !errors.Is(err, ErrExpiryInPast) {
			t.Fatalf("expected ErrExpiryInPast, got %v", err)
		}
	})

	t.Run("expired key fails validation", func(t *testing.T) {
		future := time.Now().Add(time.Hour)
		key, err := svc.CreateAPIKey(ctx, tenantID, &models.CreateApiKeyRequest{Name: "short-lived", ExpiresAt: &future})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}

		if _, err := auth.ValidateAPIKey(ctx, dbClient, key.Key); err != nil {
			t.Fatalf("expected key valid before expiry: %v", err)
		}

		// Age the key past its expiry deterministically.
		if _, err := dbClient.ExecContext(ctx, `UPDATE api_keys SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, key.ID); err != nil {
			t.Fatalf("age key: %v", err)
		}
		if _, err := auth.ValidateAPIKey(ctx, dbClient, key.Key); err == nil {
			t.Fatal("expected expired key to fail validation")
		}
	})

	t.Run("list returns scope and expiry", func(t *testing.T) {
		resp, err := svc.ListAPIKeys(ctx, tenantID, 1, 50)
		if err != nil {
			t.Fatalf("ListAPIKeys: %v", err)
		}
		if resp.Total < 3 {
			t.Fatalf("expected at least 3 keys, got %d", resp.Total)
		}
		foundRead := false
		for _, item := range resp.Items {
			if item.Scope == auth.ScopeRead {
				foundRead = true
			}
		}
		if !foundRead {
			t.Fatal("expected a read-scoped key in the list")
		}
	})
}
