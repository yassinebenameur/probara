//go:build !unit

package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A relapsed alert reports its relapse count and last reopen on every read,
// and its relapse history on GET, scoped to its tenant. A manual resolve
// leaves it not auto-resolved, so it can never reopen.
func TestRelapsedAlertReads(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "relapse-reads")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "relapse-reads-other")
	svc := alertsvc.NewService(dbc, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)

	alertID := insertAlert(ctx, t, dbc, tenantID, testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "api"), "active", now.Add(-time.Hour))
	_, err := dbc.ExecContext(ctx, `UPDATE alerts SET relapse_count = 2, reopened_at = $2 WHERE id = $1`, alertID, now.Add(-time.Minute))
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at)
		VALUES ($1, 1, $2, $3), ($1, 2, $4, $5)`, alertID,
		now.Add(-40*time.Minute), now.Add(-37*time.Minute), now.Add(-5*time.Minute), now.Add(-time.Minute))
	require.NoError(t, err)
	fresh := insertAlert(ctx, t, dbc, tenantID, testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "web"), "active", now)

	list, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{}, alertsvc.ListSort{})
	require.NoError(t, err)
	byID := map[uuid.UUID]models.AlertWithDetails{}
	for _, a := range list.Items {
		byID[a.ID] = a
	}
	require.Equal(t, 2, byID[alertID].RelapseCount)
	require.NotNil(t, byID[alertID].ReopenedAt)
	require.True(t, byID[alertID].ReopenedAt.Equal(now.Add(-time.Minute)))
	require.Empty(t, byID[alertID].Relapses, "the list carries the count, not the history")
	require.Equal(t, 0, byID[fresh].RelapseCount)
	require.Nil(t, byID[fresh].ReopenedAt)

	got, err := svc.GetAlert(ctx, tenantID, alertID)
	require.NoError(t, err)
	require.Len(t, got.Relapses, 2)
	require.Equal(t, 1, got.Relapses[0].Number)
	require.True(t, got.Relapses[0].RecoveredAt.Equal(now.Add(-40*time.Minute)))
	require.True(t, got.Relapses[1].ReopenedAt.Equal(now.Add(-time.Minute)))
	_, err = svc.GetAlert(ctx, otherTenant, alertID)
	require.ErrorIs(t, err, alertsvc.ErrAlertNotFound)

	_, err = svc.ResolveAlert(ctx, tenantID, alertID)
	require.NoError(t, err)
	var auto bool
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT auto_resolved FROM alerts WHERE id = $1`, alertID).Scan(&auto))
	require.False(t, auto)
}

// The first acknowledgement records when the episode it answered began: the
// trigger, or the relapse reopen it followed. A later acknowledgement (after
// an unacknowledge, or in a later relapse) changes neither.
func TestFirstAcknowledgementRecordsItsEpisode(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "relapse-ack")
	svc := alertsvc.NewService(dbc, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	episode := func(id uuid.UUID) (firstAck, start *time.Time) {
		t.Helper()
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT first_acknowledged_at, first_ack_episode_started_at FROM alerts WHERE id = $1`,
			id).Scan(&firstAck, &start))
		return firstAck, start
	}

	// Acknowledged in its first episode: from the trigger.
	plain := insertAlert(ctx, t, dbc, tenantID, testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "plain"), "active", now.Add(-time.Hour))
	_, err := svc.AcknowledgeAlert(ctx, tenantID, plain)
	require.NoError(t, err)
	firstAck, start := episode(plain)
	require.NotNil(t, firstAck)
	require.NotNil(t, start)
	require.True(t, start.Equal(now.Add(-time.Hour)))

	// First acknowledged after it relapsed: from the reopen.
	relapsed := insertAlert(ctx, t, dbc, tenantID, testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "relapsed"), "active", now.Add(-time.Hour))
	_, err = dbc.ExecContext(ctx, `UPDATE alerts SET relapse_count = 1, reopened_at = $2 WHERE id = $1`, relapsed, now.Add(-2*time.Minute))
	require.NoError(t, err)
	res, err := svc.BulkAcknowledge(ctx, tenantID, []uuid.UUID{relapsed})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	firstAck, start = episode(relapsed)
	require.NotNil(t, firstAck)
	require.True(t, start.Equal(now.Add(-2*time.Minute)))

	// Unacknowledged, reopened again and re-acknowledged: both stay.
	_, err = svc.UnacknowledgeAlert(ctx, tenantID, relapsed)
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `UPDATE alerts SET relapse_count = 2, reopened_at = NOW() WHERE id = $1`, relapsed)
	require.NoError(t, err)
	_, err = svc.AcknowledgeAlert(ctx, tenantID, relapsed)
	require.NoError(t, err)
	againAck, againStart := episode(relapsed)
	require.True(t, againAck.Equal(*firstAck))
	require.True(t, againStart.Equal(*start))
}
