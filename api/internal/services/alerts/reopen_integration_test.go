//go:build !unit

package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A manual resolve (single or bulk) records who resolved it and through
// which surface; resolved_via is never NULL for one, because NULL is what
// marks the alerter's own resolves, the only ones the reopen cooldown may
// reopen. The relapse fields read back through the detail projection.
func TestManualResolveRecordsResolver(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "resolver")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "api")
	svc := alertsvc.NewService(dbc, nil)

	single := insertAlert(ctx, t, dbc, tenantID, monitorID, "active", time.Now().Add(-time.Minute))
	resolved, err := svc.ResolveAlert(ctx, tenantID, single, alertsvc.Actor{Via: alertsvc.ViaMCP})
	require.NoError(t, err)
	require.NotNil(t, resolved.ResolvedVia)
	require.Equal(t, alertsvc.ViaMCP, *resolved.ResolvedVia)
	require.Equal(t, 0, resolved.RelapseCount)
	require.Nil(t, resolved.ReopenedAt)

	// An actor without a surface still counts as a manual resolve.
	otherMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "web")
	bulk := insertAlert(ctx, t, dbc, tenantID, otherMonitor, "active", time.Now().Add(-time.Minute))
	res, err := svc.BulkResolve(ctx, tenantID, []uuid.UUID{bulk}, alertsvc.Actor{})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	require.NotNil(t, res.Items[0].ResolvedVia)
	require.Equal(t, alertsvc.ViaAPI, *res.Items[0].ResolvedVia)

	// A reopened alert reports its relapse.
	_, err = dbc.ExecContext(ctx, `UPDATE alerts SET status = 'active', resolved_at = NULL, resolved_via = NULL,
		relapse_count = 2, reopened_at = NOW() WHERE id = $1`, bulk)
	require.NoError(t, err)
	reopened, err := svc.GetAlert(ctx, tenantID, bulk)
	require.NoError(t, err)
	require.Equal(t, 2, reopened.RelapseCount)
	require.NotNil(t, reopened.ReopenedAt)
	require.Nil(t, reopened.ResolvedVia)
}
