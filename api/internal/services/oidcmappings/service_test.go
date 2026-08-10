package oidcmappings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Validation happens before any DB access, so these run without a database.
func TestCreateValidation(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	tenantID := uuid.New().String()

	tests := []struct {
		name string
		req  models.CreateOIDCGroupMappingRequest
	}{
		{"empty group name", models.CreateOIDCGroupMappingRequest{GroupName: "  ", TenantID: &tenantID, Role: auth.RoleViewer}},
		{"group name too long", models.CreateOIDCGroupMappingRequest{GroupName: strings.Repeat("g", 256), TenantID: &tenantID, Role: auth.RoleViewer}},
		{"platform mapping with tenant role", models.CreateOIDCGroupMappingRequest{GroupName: "ops", Role: auth.RoleAdmin}},
		{"tenant mapping with platform role", models.CreateOIDCGroupMappingRequest{GroupName: "ops", TenantID: &tenantID, Role: auth.PlatformRoleSuperadmin}},
		{"invalid tenant id", models.CreateOIDCGroupMappingRequest{GroupName: "ops", TenantID: ptr("not-a-uuid"), Role: auth.RoleViewer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, &tt.req); !errors.Is(err, ErrInvalidMapping) {
				t.Fatalf("expected ErrInvalidMapping, got %v", err)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestMappingCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "mapping-tenant")
	tenantIDStr := tenantID.String()
	svc := NewService(dbClient)

	created, err := svc.Create(ctx, &models.CreateOIDCGroupMappingRequest{
		GroupName: "ops", TenantID: &tenantIDStr, Role: auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.TenantName == nil || *created.TenantName != "mapping-tenant" {
		t.Fatalf("expected resolved tenant name, got %v", created.TenantName)
	}

	if _, err := svc.Create(ctx, &models.CreateOIDCGroupMappingRequest{
		GroupName: "ops", TenantID: &tenantIDStr, Role: auth.RoleAdmin,
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for same (group, tenant), got %v", err)
	}

	platform, err := svc.Create(ctx, &models.CreateOIDCGroupMappingRequest{
		GroupName: "ops", Role: auth.PlatformRoleSuperadmin,
	})
	if err != nil {
		t.Fatalf("platform mapping for same group must coexist with tenant one: %v", err)
	}
	if _, err := svc.Create(ctx, &models.CreateOIDCGroupMappingRequest{
		GroupName: "ops", Role: auth.PlatformRoleSuperadmin,
	}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for second platform mapping, got %v", err)
	}

	missingTenant := uuid.New().String()
	if _, err := svc.Create(ctx, &models.CreateOIDCGroupMappingRequest{
		GroupName: "ops", TenantID: &missingTenant, Role: auth.RoleViewer,
	}); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}

	updated, err := svc.Update(ctx, created.ID, &models.UpdateOIDCGroupMappingRequest{Role: ptr(auth.RoleAdmin)})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Role != auth.RoleAdmin {
		t.Fatalf("expected admin after update, got %q", updated.Role)
	}
	if _, err := svc.Update(ctx, platform.ID, &models.UpdateOIDCGroupMappingRequest{Role: ptr(auth.RoleAdmin)}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("platform mapping role must stay superadmin, got %v", err)
	}

	// Label: set without touching role, then clear with an empty string.
	labeled, err := svc.Update(ctx, created.ID, &models.UpdateOIDCGroupMappingRequest{Label: ptr("  Engineering  ")})
	if err != nil {
		t.Fatalf("set label: %v", err)
	}
	if labeled.Label == nil || *labeled.Label != "Engineering" {
		t.Fatalf("expected trimmed label, got %v", labeled.Label)
	}
	if labeled.Role != auth.RoleAdmin {
		t.Fatalf("label-only update must keep role, got %q", labeled.Role)
	}
	cleared, err := svc.Update(ctx, created.ID, &models.UpdateOIDCGroupMappingRequest{Label: ptr("")})
	if err != nil {
		t.Fatalf("clear label: %v", err)
	}
	if cleared.Label != nil {
		t.Fatalf("expected label cleared, got %v", *cleared.Label)
	}
	if _, err := svc.Update(ctx, created.ID, &models.UpdateOIDCGroupMappingRequest{Label: ptr(strings.Repeat("l", 101))}); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping for oversized label, got %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(list))
	}
	// Platform row (NULL tenant) sorts first within the group.
	if list[0].TenantID != nil || list[1].TenantID == nil {
		t.Fatalf("expected platform row first, got %+v", list)
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on double delete, got %v", err)
	}

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO oidc_seen_groups (group_name, last_seen_at)
		VALUES ('older-group', NOW() - INTERVAL '1 hour'), ('newer-group', NOW())
	`); err != nil {
		t.Fatalf("insert seen groups: %v", err)
	}
	seen, err := svc.SeenGroups(ctx)
	if err != nil {
		t.Fatalf("seen groups: %v", err)
	}
	if len(seen) != 2 || seen[0].GroupName != "newer-group" || seen[1].GroupName != "older-group" {
		t.Fatalf("expected [newer-group, older-group], got %+v", seen)
	}
}
