//go:build !unit

package statuspage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace's status pages are not served (by slug, preview or
// ID), take no push subscriptions, and its queued visitor pushes are never
// claimed for sending.
func TestDeletedWorkspaceStatusPagesAreGone(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	live := testutil.InsertTenant(ctx, t, dbClient, "live")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "deleted")
	testutil.InsertStatusPage(ctx, t, dbClient, live, "live-page", "Live")
	deletedPage := testutil.InsertStatusPage(ctx, t, dbClient, deleted, "gone-page", "Gone")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "api")
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1;`, deletedPage); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO status_page_push_deliveries (status_page_id, monitor_id, interval_id, kind, monitor_name)
		VALUES ($1, $2, $3, 'down', 'api')`, deletedPage, monitor, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	if _, err := svc.GetStatusPageBySlug(ctx, "live-page"); err != nil {
		t.Fatalf("live page: %v", err)
	}
	if _, err := svc.GetStatusPageBySlug(ctx, "gone-page"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace page: err = %v, want not found", err)
	}
	if _, _, _, err := svc.GetDraftTemplateBySlug(ctx, "gone-page"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace preview: err = %v, want not found", err)
	}
	if _, err := svc.GetTenantIDByStatusPageID(ctx, deletedPage); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("deleted workspace page by id: err = %v, want not found", err)
	}

	store := newPushStore(dbClient.DB)
	if id, _, err := store.PageIDForSlug(ctx, "gone-page"); err != nil || id != uuid.Nil {
		t.Fatalf("push subscribe lookup = %v, %v; want no page", id, err)
	}
	claimed, err := store.ClaimDeliveries(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d pushes of a deleted workspace", len(claimed))
	}
}
