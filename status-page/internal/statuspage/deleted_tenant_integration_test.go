//go:build !unit

package statuspage

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace's pages stop serving at once, before the scheduler
// purger removes them: the read path filters on tenants.deleted_at.
func TestDeletedTenantPagesAreNotServed(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "doomed-status", "Doomed")
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE slug = 'doomed-status'
	`); err != nil {
		t.Fatalf("enable push: %v", err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	store := newPushStore(dbClient.DB)

	if _, err := svc.GetStatusPageBySlug(ctx, "doomed-status"); err != nil {
		t.Fatalf("live workspace page: GetStatusPageBySlug() error = %v", err)
	}
	if id, enabled, err := store.PageIDForSlug(ctx, "doomed-status"); err != nil || id == uuid.Nil || !enabled {
		t.Fatalf("live workspace page: PageIDForSlug() = %s, %v, %v", id, enabled, err)
	}

	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID); err != nil {
		t.Fatalf("soft-delete tenant: %v", err)
	}

	if _, err := svc.GetStatusPageBySlug(ctx, "doomed-status"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace page: GetStatusPageBySlug() error = %v, want ErrStatusPageNotFound", err)
	}
	if id, _, err := store.PageIDForSlug(ctx, "doomed-status"); err != nil || id != uuid.Nil {
		t.Fatalf("deleted workspace page: PageIDForSlug() = %s, %v; want no page", id, err)
	}
}
