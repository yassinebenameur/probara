//go:build !unit

package statuspages

import (
	"context"
	"encoding/json"
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The template setting through the service and the database: created,
// read back, patched, kept by a patch that omits it, refused when unknown
// (the handler validates before the service), and stored under the key the
// status-page read mirror parses.
func TestService_TemplateSettingRoundTrip(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-template-setting")
	svc := NewService(dbClient, nil)
	str := func(s string) *string { return &s }
	template := func(page *models.StatusPage) string {
		t.Helper()
		if page.Settings == nil || page.Settings.Template == nil {
			t.Fatal("settings.template missing from the API response")
		}
		return *page.Settings.Template
	}
	storedTemplate := func(page *models.StatusPage) any {
		t.Helper()
		var raw []byte
		if err := dbClient.QueryRowContext(ctx, `SELECT settings FROM status_pages WHERE id = $1`, page.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		return keys["template"]
	}

	plain, err := svc.CreateStatusPage(ctx, tenantID, &models.CreateStatusPageRequest{Slug: "plain", Title: "Plain"})
	if err != nil {
		t.Fatal(err)
	}
	if got := template(plain); got != "default" {
		t.Fatalf("a page created without the setting reads %q, want default", got)
	}

	create := &models.CreateStatusPageRequest{Slug: "classic", Title: "Classic", Settings: &models.StatusPageSettings{Template: str("classic")}}
	if err := validation.ValidateStatusPage(create); err != nil {
		t.Fatalf("classic refused: %v", err)
	}
	page, err := svc.CreateStatusPage(ctx, tenantID, create)
	if err != nil {
		t.Fatal(err)
	}
	if got := template(page); got != "classic" {
		t.Fatalf("created template = %q, want classic", got)
	}
	if got := storedTemplate(page); got != "classic" {
		t.Fatalf("stored settings.template = %v, want classic", got)
	}

	// A patch that omits the setting keeps it.
	page, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{
		Settings: &models.StatusPageSettings{FooterText: str("Footer")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := template(page); got != "classic" {
		t.Fatalf("absent template changed the stored one to %q", got)
	}
	reread, err := svc.GetStatusPage(ctx, tenantID, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := template(reread); got != "classic" {
		t.Fatalf("read back template = %q, want classic", got)
	}

	// Unknown names are refused before the service runs.
	if err := validation.ValidateStatusPageUpdate(&models.UpdateStatusPageRequest{
		Settings: &models.StatusPageSettings{Template: str("modern")},
	}); err == nil {
		t.Fatal("an unknown template must be refused")
	}

	page, err = svc.UpdateStatusPage(ctx, tenantID, page.ID, &models.UpdateStatusPageRequest{
		Settings: &models.StatusPageSettings{Template: str("default")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := template(page); got != "default" {
		t.Fatalf("patched template = %q, want default", got)
	}
}
