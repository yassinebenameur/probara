//go:build !unit

package tenancy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/tenancy"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestLiveTenantSQLAndIsDeleted(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	live := func() bool {
		var ok bool
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT `+tenancy.LiveTenantSQL("$1::uuid"), tenantID).Scan(&ok))
		return ok
	}
	deleted := func() bool {
		d, err := tenancy.IsDeleted(ctx, dbClient, tenantID)
		require.NoError(t, err)
		return d
	}

	require.True(t, live())
	require.False(t, deleted())

	_, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID)
	require.NoError(t, err)
	require.False(t, live())
	require.True(t, deleted())

	// A binary ahead of migration 000105: no column, nothing deleted, and
	// neither check may error.
	_, err = dbClient.ExecContext(ctx, `ALTER TABLE tenants DROP COLUMN deleted_at CASCADE`)
	require.NoError(t, err)
	require.True(t, live())
	require.False(t, deleted())
}
