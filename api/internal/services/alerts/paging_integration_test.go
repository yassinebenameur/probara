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

// Acknowledge records the actor; unacknowledge returns the alert to active,
// stamps unacknowledged_at, clears the actor and the one-shot ack slot so a
// re-ack reaches paging channels again; and every action is tenant scoped
// and refuses alerts in the wrong state.
func TestPaging_UnacknowledgeAndSnooze(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "paging")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "paging-other")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "api")
	foreignMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, otherTenant, "theirs")
	alertID := insertAlert(ctx, t, dbc, tenantID, monitorID, "active", time.Now().Add(-time.Minute))
	foreign := insertAlert(ctx, t, dbc, otherTenant, foreignMonitor, "acknowledged", time.Now())

	channelID := uuid.New()
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'pager', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID)
	require.NoError(t, err)

	svc := alertsvc.NewService(dbc, nil)
	actor := alertsvc.Actor{Via: alertsvc.ViaAPI}

	// Unack of an active alert is a state error.
	_, err = svc.UnacknowledgeAlert(ctx, tenantID, alertID, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotAcknowledged)

	acked, err := svc.AcknowledgeAlert(ctx, tenantID, alertID, actor)
	require.NoError(t, err)
	require.NotNil(t, acked.AcknowledgedVia)
	require.Equal(t, alertsvc.ViaAPI, *acked.AcknowledgedVia)
	require.NotNil(t, acked.MutedReason)
	require.Equal(t, "acknowledged", *acked.MutedReason)

	// The paging channel was told about the ack.
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, acknowledged_sent_at, created_at, updated_at)
		VALUES ($1, $2, NOW(), 'created', NOW(), NOW(), NOW())`, alertID, channelID)
	require.NoError(t, err)

	// Another tenant cannot unack it, nor can this tenant unack theirs.
	_, err = svc.UnacknowledgeAlert(ctx, otherTenant, alertID, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotAcknowledged)
	_, err = svc.UnacknowledgeAlert(ctx, tenantID, foreign, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotAcknowledged)

	unacked, err := svc.UnacknowledgeAlert(ctx, tenantID, alertID, actor)
	require.NoError(t, err)
	require.Equal(t, "active", string(unacked.Status))
	require.NotNil(t, unacked.UnacknowledgedAt)
	require.Nil(t, unacked.AcknowledgedAt)
	require.Nil(t, unacked.AcknowledgedVia)
	require.Nil(t, unacked.MutedReason)
	var ackSent *time.Time
	require.NoError(t, dbc.QueryRowContext(ctx,
		`SELECT acknowledged_sent_at FROM alert_notification_states WHERE alert_id = $1`, alertID).Scan(&ackSent))
	require.Nil(t, ackSent, "unack must re-arm the one-shot acknowledgement")

	// Snooze bounds, tenant scope, and the muted reason.
	_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(30*time.Second), actor)
	require.ErrorIs(t, err, alertsvc.ErrInvalidSnooze)
	_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(8*24*time.Hour), actor)
	require.ErrorIs(t, err, alertsvc.ErrInvalidSnooze)
	_, err = svc.SnoozeAlert(ctx, otherTenant, alertID, time.Now().Add(time.Hour), actor)
	require.ErrorIs(t, err, alertsvc.ErrNotOpen)
	snoozed, err := svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(time.Hour), actor)
	require.NoError(t, err)
	require.NotNil(t, snoozed.MutedReason)
	require.Equal(t, "snoozed", *snoozed.MutedReason)

	// Unsnooze ends it now (kept as the clock's resume point).
	unsnoozed, err := svc.UnsnoozeAlert(ctx, tenantID, alertID, actor)
	require.NoError(t, err)
	require.Nil(t, unsnoozed.MutedReason)
	require.NotNil(t, unsnoozed.SnoozedUntil)
	require.False(t, unsnoozed.SnoozedUntil.After(time.Now()))
	_, err = svc.UnsnoozeAlert(ctx, tenantID, alertID, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotSnoozed)

	// Resolve ends a running snooze; a resolved alert cannot be snoozed.
	_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(time.Hour), actor)
	require.NoError(t, err)
	resolved, err := svc.ResolveAlert(ctx, tenantID, alertID, actor)
	require.NoError(t, err)
	require.False(t, resolved.SnoozedUntil.After(time.Now()))
	_, err = svc.SnoozeAlert(ctx, tenantID, alertID, time.Now().Add(time.Hour), actor)
	require.ErrorIs(t, err, alertsvc.ErrNotOpen)
}

func TestPaging_BulkUnacknowledge(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "bulk-unack")
	monitor := func(name string) uuid.UUID { return testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, name) }
	now := time.Now()
	acked := insertAlert(ctx, t, dbc, tenantID, monitor("ack"), "acknowledged", now)
	active := insertAlert(ctx, t, dbc, tenantID, monitor("active"), "active", now)
	resolved := insertAlert(ctx, t, dbc, tenantID, monitor("res"), "resolved", now.Add(-time.Hour))

	svc := alertsvc.NewService(dbc, nil)
	result, err := svc.BulkUnacknowledge(ctx, tenantID, []uuid.UUID{acked, active, resolved}, alertsvc.Actor{Via: alertsvc.ViaUI})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, acked, result.Items[0].ID)
	require.Equal(t, "active", alertStatus(ctx, t, dbc, acked))
	reasons := map[uuid.UUID]string{}
	for _, f := range result.Failed {
		reasons[f.ID] = f.Reason
	}
	require.Equal(t, alertsvc.BulkReasonNotAcknowledged, reasons[active])
	require.Equal(t, alertsvc.BulkReasonAlreadyResolved, reasons[resolved])
}

// Unsnooze, like every paging action, cannot touch an alert of a deleted
// monitor (hidden from every read) or another tenant's alert.
func TestPaging_UnsnoozeRespectsVisibility(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "unsnooze-visible")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "unsnooze-other")
	gone := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "gone")
	live := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "live")
	hidden := insertAlert(ctx, t, dbc, tenantID, gone, "active", time.Now())
	visible := insertAlert(ctx, t, dbc, tenantID, live, "active", time.Now())
	for _, id := range []uuid.UUID{hidden, visible} {
		_, err := dbc.ExecContext(ctx, `UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour' WHERE id = $1`, id)
		require.NoError(t, err)
	}
	_, err := dbc.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	svc := alertsvc.NewService(dbc, nil)
	actor := alertsvc.Actor{Via: alertsvc.ViaAPI}
	_, err = svc.UnsnoozeAlert(ctx, tenantID, hidden, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotSnoozed)
	var stillSnoozed bool
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT snoozed_until > NOW() FROM alerts WHERE id = $1`, hidden).Scan(&stillSnoozed))
	require.True(t, stillSnoozed, "an alert of a deleted monitor was unsnoozed")

	_, err = svc.UnsnoozeAlert(ctx, otherTenant, visible, actor)
	require.ErrorIs(t, err, alertsvc.ErrNotSnoozed)
	_, err = svc.UnsnoozeAlert(ctx, tenantID, visible, actor)
	require.NoError(t, err)
}
