//go:build !unit

package alerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Acknowledge records who; unacknowledge returns the alert to active, clears
// the acknowledgement, stamps the resume point and re-arms the paging
// acknowledgement; snooze and unsnooze set and end the mute, reported only
// while it holds. Every action is tenant-scoped and refuses the wrong state.
func TestAlertMuteActions(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "mute")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "mute-other")
	monitor := func(name string) uuid.UUID { return testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, name) }
	userID := uuid.New()
	_, err := dbc.ExecContext(ctx, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, 'oncall', 'x')`, userID)
	require.NoError(t, err)
	asUser := ctxpkg.WithAdminID(ctx, userID.String())
	svc := alertsvc.NewService(dbc, nil)
	now := time.Now()
	firstAck := func(id uuid.UUID) *time.Time {
		t.Helper()
		var at *time.Time
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT first_acknowledged_at FROM alerts WHERE id = $1`, id).Scan(&at))
		return at
	}

	alertID := insertAlert(ctx, t, dbc, tenantID, monitor("api"), "active", now.Add(-time.Hour))
	channelID := uuid.New()
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID)
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, acknowledged_sent_at, created_at, updated_at)
		VALUES ($1, $2, NOW(), 'created', NOW(), NOW(), NOW())`, alertID, channelID)
	require.NoError(t, err)

	t.Run("acknowledge records the user", func(t *testing.T) {
		got, err := svc.AcknowledgeAlert(asUser, tenantID, alertID)
		require.NoError(t, err)
		require.NotNil(t, got.AcknowledgedByUserID)
		require.Equal(t, userID, *got.AcknowledgedByUserID)
		require.NotNil(t, got.AcknowledgedBy)
		require.Equal(t, "oncall", *got.AcknowledgedBy)
		require.NotNil(t, got.AcknowledgedAt)
	})

	t.Run("unacknowledge is tenant-scoped", func(t *testing.T) {
		_, err := svc.UnacknowledgeAlert(ctx, otherTenant, alertID)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotAcknowledged)
		require.Equal(t, "acknowledged", alertStatus(ctx, t, dbc, alertID))
	})

	t.Run("unacknowledge returns to active", func(t *testing.T) {
		got, err := svc.UnacknowledgeAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		require.Equal(t, "active", string(got.Status))
		require.Nil(t, got.AcknowledgedAt)
		require.Nil(t, got.AcknowledgedByUserID)
		require.Nil(t, got.AcknowledgedBy)
		var unackedAt *time.Time
		var ackSentAt *time.Time
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT unacknowledged_at FROM alerts WHERE id = $1`, alertID).Scan(&unackedAt))
		require.NotNil(t, unackedAt)
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1`,
			alertID).Scan(&ackSentAt))
		require.Nil(t, ackSentAt, "a later acknowledge must reach paging channels again")
		require.NotNil(t, firstAck(alertID), "the first acknowledgement survives, for SLA reports")

		_, err = svc.UnacknowledgeAlert(ctx, tenantID, alertID)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotAcknowledged)
	})

	t.Run("a re-acknowledge keeps the first acknowledgement time", func(t *testing.T) {
		first := firstAck(alertID)
		_, err := svc.AcknowledgeAlert(asUser, tenantID, alertID)
		require.NoError(t, err)
		require.Equal(t, first.UTC(), firstAck(alertID).UTC())
		_, err = svc.UnacknowledgeAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
	})

	t.Run("snooze sets the end, reported while it holds", func(t *testing.T) {
		until := time.Now().Add(4 * time.Hour)
		_, err := svc.SnoozeAlert(ctx, otherTenant, alertID, until)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotOpen)

		got, err := svc.SnoozeAlert(asUser, tenantID, alertID, until)
		require.NoError(t, err)
		require.NotNil(t, got.SnoozedUntil)
		require.WithinDuration(t, until, *got.SnoozedUntil, time.Second)
		require.Equal(t, "active", string(got.Status), "a snoozed alert is still active")
		require.NotNil(t, got.SnoozedBy)
		require.Equal(t, "oncall", *got.SnoozedBy)
		require.Equal(t, userID, *got.SnoozedByUserID)
		require.NotNil(t, got.SnoozedAt)
		started := *got.SnoozedAt

		// Extending a running snooze keeps when it began.
		got, err = svc.SnoozeAlert(ctx, tenantID, alertID, until.Add(time.Hour))
		require.NoError(t, err)
		require.Equal(t, started.UTC(), got.SnoozedAt.UTC())
		require.Nil(t, got.SnoozedByUserID, "an API-key snooze names nobody")

		_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(30*time.Second))
		require.ErrorIs(t, err, alertsvc.ErrInvalidSnooze)
		_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(8*24*time.Hour))
		require.ErrorIs(t, err, alertsvc.ErrInvalidSnooze)
	})

	t.Run("unsnooze ends it now", func(t *testing.T) {
		_, err := svc.UnsnoozeAlert(ctx, otherTenant, alertID)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotSnoozed)

		got, err := svc.UnsnoozeAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		require.Nil(t, got.SnoozedUntil)
		require.Nil(t, got.SnoozedAt)
		require.Nil(t, got.SnoozedBy)
		var end time.Time
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT snoozed_until FROM alerts WHERE id = $1`, alertID).Scan(&end))
		require.WithinDuration(t, time.Now(), end, 5*time.Second, "the end is kept: the alerter counts from it")

		_, err = svc.UnsnoozeAlert(ctx, tenantID, alertID)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotSnoozed)
	})

	t.Run("a snooze that ran out is not reported", func(t *testing.T) {
		_, err := dbc.ExecContext(ctx, `UPDATE alerts SET snoozed_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, alertID)
		require.NoError(t, err)
		got, err := svc.GetAlert(ctx, tenantID, alertID)
		require.NoError(t, err)
		require.Nil(t, got.SnoozedUntil)
	})

	t.Run("resolved alerts take none of it", func(t *testing.T) {
		resolved := insertAlert(ctx, t, dbc, tenantID, monitor("old"), "resolved", now.Add(-2*time.Hour))
		_, err := svc.SnoozeAlert(ctx, tenantID, resolved, time.Now().Add(time.Hour))
		require.ErrorIs(t, err, alertsvc.ErrAlertNotOpen)
		_, err = svc.UnacknowledgeAlert(ctx, tenantID, resolved)
		require.ErrorIs(t, err, alertsvc.ErrAlertNotAcknowledged)
	})
}

func TestBulkMuteActions_PartialResults(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "bulk-mute")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "bulk-mute-other")
	monitor := func(name string) uuid.UUID { return testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, name) }
	now := time.Now()
	active := insertAlert(ctx, t, dbc, tenantID, monitor("a"), "active", now.Add(-3*time.Minute))
	acked := insertAlert(ctx, t, dbc, tenantID, monitor("b"), "acknowledged", now.Add(-2*time.Minute))
	resolved := insertAlert(ctx, t, dbc, tenantID, monitor("c"), "resolved", now.Add(-time.Hour))
	foreign := insertAlert(ctx, t, dbc, otherTenant, testutil.InsertHTTPMonitor(ctx, t, dbc, otherTenant, "theirs"), "acknowledged", now)
	svc := alertsvc.NewService(dbc, nil)

	reasons := func(res *alertsvc.BulkResult) map[uuid.UUID]string {
		out := map[uuid.UUID]string{}
		for _, f := range res.Failed {
			out[f.ID] = f.Reason
		}
		return out
	}

	res, err := svc.BulkUnacknowledge(ctx, tenantID, []uuid.UUID{active, acked, resolved, foreign})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	require.Equal(t, acked, res.Items[0].ID)
	require.Equal(t, map[uuid.UUID]string{
		active:   alertsvc.BulkReasonNotAcknowledged,
		resolved: alertsvc.BulkReasonAlreadyResolved,
		foreign:  alertsvc.BulkReasonNotFound,
	}, reasons(res))
	require.Equal(t, "acknowledged", alertStatus(ctx, t, dbc, foreign))

	res, err = svc.BulkSnooze(ctx, tenantID, []uuid.UUID{active, acked, resolved, foreign}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, res.Items, 2)
	for _, a := range res.Items {
		require.NotNil(t, a.SnoozedUntil)
	}
	require.Equal(t, map[uuid.UUID]string{
		resolved: alertsvc.BulkReasonAlreadyResolved,
		foreign:  alertsvc.BulkReasonNotFound,
	}, reasons(res))

	res, err = svc.BulkUnsnooze(ctx, tenantID, []uuid.UUID{active, resolved})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	require.Nil(t, res.Items[0].SnoozedUntil)
	res, err = svc.BulkUnsnooze(ctx, tenantID, []uuid.UUID{active})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{active: alertsvc.BulkReasonNotSnoozed}, reasons(res))
}
