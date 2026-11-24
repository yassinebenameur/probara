package testutil

import (
	"context"
	"testing"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

// MaterializeOneOffMaintenance gives a maintenance window inserted directly
// (as a one-off, starts_at..ends_at) its single occurrence, the way the API
// does on create. Coverage reads occurrences only (docs/state-semantics.md
// S-M5), so a window without one covers nothing.
func MaterializeOneOffMaintenance(ctx context.Context, t testing.TB, dbClient *shareddb.Client, windowID uuid.UUID) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
		SELECT id, tenant_id, starts_at, ends_at FROM maintenance_windows WHERE id = $1
		ON CONFLICT (window_id, starts_at) DO UPDATE SET ends_at = EXCLUDED.ends_at
	`, windowID); err != nil {
		t.Fatalf("materialize maintenance occurrence: %v", err)
	}
}
