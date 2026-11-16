//go:build !unit

package tenancy_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/tenancy"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestLiveAndDeletedTenantSQL(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	check := func(id uuid.UUID) (live, deleted bool) {
		t.Helper()
		if err := dbClient.QueryRowContext(ctx,
			`SELECT `+tenancy.LiveTenantSQL("$1::uuid")+`, `+tenancy.DeletedTenantSQL("$1::uuid"), id,
		).Scan(&live, &deleted); err != nil {
			t.Fatal(err)
		}
		return live, deleted
	}
	expect := func(step string, id uuid.UUID, wantLive, wantDeleted bool) {
		t.Helper()
		if live, deleted := check(id); live != wantLive || deleted != wantDeleted {
			t.Fatalf("%s: live=%v deleted=%v, want %v %v", step, live, deleted, wantLive, wantDeleted)
		}
	}

	expect("live", tenantID, true, false)
	expect("unknown", uuid.New(), false, false)
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}
	expect("deleted", tenantID, false, true)

	// A binary ahead of migration 000116 (no deleted_at column): nothing is
	// deleted, and neither predicate may error.
	if _, err := dbClient.ExecContext(ctx, `ALTER TABLE tenants DROP COLUMN deleted_at CASCADE`); err != nil {
		t.Fatal(err)
	}
	expect("pre-migration", tenantID, true, false)
}
