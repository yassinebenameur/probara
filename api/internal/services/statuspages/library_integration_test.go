package statuspages

import (
	"context"
	"errors"
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestService_LibraryTemplateLifecycle(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "template-library")
	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "template-library-other")
	svc := NewService(dbClient, nil)

	// Invalid source is rejected before anything is stored.
	if _, err := svc.CreateLibraryTemplate(ctx, tenantID, &models.CreateStatusPageLibraryTemplateRequest{
		Name:   "broken",
		Source: "{{if .Title}}unclosed",
	}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("CreateLibraryTemplate(broken source) error = %v, want ErrInvalidTemplate", err)
	}

	created, err := svc.CreateLibraryTemplate(ctx, tenantID, &models.CreateStatusPageLibraryTemplateRequest{
		Name:   "Aurora light",
		Source: "<html><body>{{.Title}}</body></html>",
	})
	if err != nil {
		t.Fatalf("CreateLibraryTemplate() error = %v", err)
	}

	// Duplicate name (case-insensitive) is rejected.
	if _, err := svc.CreateLibraryTemplate(ctx, tenantID, &models.CreateStatusPageLibraryTemplateRequest{
		Name:   "aurora LIGHT",
		Source: "<html></html>",
	}); err == nil || err.Error() != "template name already exists" {
		t.Fatalf("CreateLibraryTemplate(duplicate) error = %v, want name conflict", err)
	}

	// List returns metadata without source.
	list, err := svc.ListLibraryTemplates(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListLibraryTemplates() error = %v", err)
	}
	if list.Total != 1 || list.Items[0].Name != "Aurora light" || list.Items[0].Source != "" {
		t.Fatalf("ListLibraryTemplates() = %+v, want one source-less item named 'Aurora light'", list)
	}

	// Get returns the source; tenant isolation holds.
	got, err := svc.GetLibraryTemplate(ctx, tenantID, created.ID)
	if err != nil || got.Source != "<html><body>{{.Title}}</body></html>" {
		t.Fatalf("GetLibraryTemplate() = %+v, %v", got, err)
	}
	if _, err := svc.GetLibraryTemplate(ctx, otherTenantID, created.ID); err == nil {
		t.Fatal("GetLibraryTemplate() must not cross tenants")
	}

	// Update validates the new source and bumps metadata.
	if _, err := svc.UpdateLibraryTemplate(ctx, tenantID, created.ID, &models.UpdateStatusPageLibraryTemplateRequest{
		Source: strPtr("{{nosuchfunc}}"),
	}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("UpdateLibraryTemplate(bad source) error = %v, want ErrInvalidTemplate", err)
	}
	updated, err := svc.UpdateLibraryTemplate(ctx, tenantID, created.ID, &models.UpdateStatusPageLibraryTemplateRequest{
		Name:        strPtr("Aurora light v2"),
		Description: strPtr("brand reskin"),
	})
	if err != nil || updated.Name != "Aurora light v2" || updated.Description == nil {
		t.Fatalf("UpdateLibraryTemplate() = %+v, %v", updated, err)
	}

	// Delete is tenant-scoped and final.
	if err := svc.DeleteLibraryTemplate(ctx, otherTenantID, created.ID); err == nil {
		t.Fatal("DeleteLibraryTemplate() must not cross tenants")
	}
	if err := svc.DeleteLibraryTemplate(ctx, tenantID, created.ID); err != nil {
		t.Fatalf("DeleteLibraryTemplate() error = %v", err)
	}
	if err := svc.DeleteLibraryTemplate(ctx, tenantID, created.ID); err == nil || err.Error() != "library template not found" {
		t.Fatalf("DeleteLibraryTemplate(gone) error = %v, want not found", err)
	}
}

func strPtr(s string) *string { return &s }
