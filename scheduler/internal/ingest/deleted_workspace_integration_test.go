//go:build !unit

package ingest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace's private-location workers are cut off at ingest: the
// credential lookup behind result and heartbeat verification refuses them.
func TestLocationCredentialRefusesDeletedWorkspace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	i := newTestIngest(t, dbClient, nil)

	location := func(tenant uuid.UUID) uuid.UUID {
		id := uuid.New()
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug, worker_credential)
			VALUES ($1, $2, $3, $3, 'credential')`, id, tenant, "loc-"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := testutil.InsertTenant(ctx, t, dbClient, "")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "")
	liveLocation, deletedLocation := location(live), location(deleted)
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	if _, err := i.locationCredential(ctx, liveLocation.String(), "", ""); err != nil {
		t.Fatalf("live workspace location: %v", err)
	}
	if _, err := i.locationCredential(ctx, deletedLocation.String(), "", ""); err == nil {
		t.Fatal("a deleted workspace's location must not authenticate")
	}
	if _, err := i.locationCredential(ctx, deletedLocation.String(), deleted.String(), ""); err == nil {
		t.Fatal("a deleted workspace's location must not authenticate a result")
	}
}
