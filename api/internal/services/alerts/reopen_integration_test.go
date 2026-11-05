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

// The first acknowledgement is what SLA reports read for MTTA, because a
// reopen and an unacknowledge both clear acknowledged_at. It is set once,
// with the start of the episode it acknowledged (triggered_at, or
// reopened_at for a relapse), and neither a reopen, an unacknowledge nor a
// later acknowledgement changes it, single or bulk.
func TestFirstAcknowledgementSurvivesReopenAndUnack(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "first-ack")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "api")
	svc := alertsvc.NewService(dbc, nil)
	firstAck := func(id uuid.UUID) (ackedAt, episodeAt *time.Time) {
		t.Helper()
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT first_acknowledged_at, first_ack_episode_started_at FROM alerts WHERE id = $1`, id).
			Scan(&ackedAt, &episodeAt))
		return ackedAt, episodeAt
	}
	// What the alerter's reopen does to the row (alerter reopenOutageAlertTx).
	reopen := func(id uuid.UUID) {
		t.Helper()
		_, err := dbc.ExecContext(ctx, `UPDATE alerts SET status = 'active', resolved_at = NULL,
			relapse_count = relapse_count + 1, reopened_at = NOW(),
			acknowledged_at = NULL, acknowledged_by_user_id = NULL, acknowledged_via = NULL, unacknowledged_at = NULL
			WHERE id = $1`, id)
		require.NoError(t, err)
	}

	triggered := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	single := insertAlert(ctx, t, dbc, tenantID, monitorID, "active", triggered)
	_, err := svc.AcknowledgeAlert(ctx, tenantID, single, alertsvc.Actor{Via: alertsvc.ViaAPI})
	require.NoError(t, err)
	ackedAt, episodeAt := firstAck(single)
	require.NotNil(t, ackedAt)
	require.True(t, episodeAt.Equal(triggered), "the first episode starts at the trigger")

	_, err = svc.UnacknowledgeAlert(ctx, tenantID, single, alertsvc.Actor{Via: alertsvc.ViaAPI})
	require.NoError(t, err)
	reopen(single)
	_, err = svc.AcknowledgeAlert(ctx, tenantID, single, alertsvc.Actor{Via: alertsvc.ViaAPI})
	require.NoError(t, err)
	againAt, againEpisode := firstAck(single)
	require.True(t, againAt.Equal(*ackedAt), "a later acknowledgement never replaces the first")
	require.True(t, againEpisode.Equal(triggered))

	// Never acknowledged before its relapse: the first acknowledgement is
	// measured from the reopen.
	bulk := insertAlert(ctx, t, dbc, tenantID, testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "web"), "active", triggered)
	reopen(bulk)
	var reopenedAt time.Time
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT reopened_at FROM alerts WHERE id = $1`, bulk).Scan(&reopenedAt))
	_, err = svc.BulkAcknowledge(ctx, tenantID, []uuid.UUID{bulk}, alertsvc.Actor{Via: alertsvc.ViaAPI})
	require.NoError(t, err)
	ackedAt, episodeAt = firstAck(bulk)
	require.NotNil(t, ackedAt)
	require.True(t, episodeAt.Equal(reopenedAt), "a relapse's first acknowledgement counts from its reopen")
}
