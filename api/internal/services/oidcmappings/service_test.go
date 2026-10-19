package oidcmappings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
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
