//go:build !unit

package alerts_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// recoveryRecorder stands in for the incident automation and records which
// alerts BulkResolve reported as recovered inside its transaction.
type recoveryRecorder struct{ recovered []uuid.UUID }

func (r *recoveryRecorder) EnsureIncidentForAlertTx(context.Context, *sql.Tx, uuid.UUID, *models.AlertWithDetails) error {
	return nil
}

func (r *recoveryRecorder) RecordAlertRecoveryIfNeededTx(_ context.Context, _ *sql.Tx, _ uuid.UUID, alertID uuid.UUID) error {
	r.recovered = append(r.recovered, alertID)
	return nil
}

func insertAlert(ctx context.Context, t *testing.T, dbc *shareddb.Client, tenantID, monitorID uuid.UUID, status string, triggeredAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var resolvedAt any
	if status == "resolved" {
		resolvedAt = triggeredAt.Add(10 * time.Minute)
	}
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, resolved_at,
			failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, NOW(), NOW())
	`, id, tenantID, monitorID, status, triggeredAt, resolvedAt)
	require.NoError(t, err)
	return id
}

func alertStatus(ctx context.Context, t *testing.T, dbc *shareddb.Client, id uuid.UUID) string {
	t.Helper()
	var status string
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, id).Scan(&status))
	return status
}

func TestBulkAcknowledgeAndResolve_PartialResults(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "bulk")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "bulk-other")
	// One open alert per monitor and kind (idx_alerts_one_open_per_monitor_kind).
	monitor := func(name string) uuid.UUID { return testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, name) }
	goneMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "gone")
	foreignMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, otherTenant, "theirs")
	now := time.Now()

	active1 := insertAlert(ctx, t, dbc, tenantID, monitor("a1"), "active", now.Add(-3*time.Minute))
	active2 := insertAlert(ctx, t, dbc, tenantID, monitor("a2"), "active", now.Add(-2*time.Minute))
	acked := insertAlert(ctx, t, dbc, tenantID, monitor("ack"), "acknowledged", now.Add(-time.Minute))
	resolved := insertAlert(ctx, t, dbc, tenantID, monitor("res"), "resolved", now.Add(-time.Hour))
	foreign := insertAlert(ctx, t, dbc, otherTenant, foreignMonitor, "active", now)
	hidden := insertAlert(ctx, t, dbc, tenantID, goneMonitor, "active", now)
	_, err := dbc.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, goneMonitor)
	require.NoError(t, err)
	missing := uuid.New()

	incidents := &recoveryRecorder{}
	svc := alertsvc.NewService(dbc, incidents)

	t.Run("acknowledge changes only active alerts and explains the rest", func(t *testing.T) {
		res, err := svc.BulkAcknowledge(ctx, tenantID,
			[]uuid.UUID{active2, acked, active1, resolved, foreign, hidden, missing, active1})
		require.NoError(t, err)

		require.Len(t, res.Items, 2)
		require.Equal(t, active2, res.Items[0].ID, "items keep request order")
		require.Equal(t, active1, res.Items[1].ID, "a repeated id is applied once")
		for _, item := range res.Items {
			require.Equal(t, models.AlertStatusAcknowledged, item.Status)
			require.NotNil(t, item.AcknowledgedAt)
			require.NotNil(t, item.MonitorName, "items carry the full alert detail")
		}

		require.Equal(t, []alertsvc.BulkFailure{
			{ID: acked, Reason: alertsvc.BulkReasonAlreadyAcknowledged},
			{ID: resolved, Reason: alertsvc.BulkReasonAlreadyResolved},
			{ID: foreign, Reason: alertsvc.BulkReasonNotFound},
			{ID: hidden, Reason: alertsvc.BulkReasonNotFound},
			{ID: missing, Reason: alertsvc.BulkReasonNotFound},
		}, res.Failed)

		require.Equal(t, "active", alertStatus(ctx, t, dbc, foreign), "another tenant's alert is never touched")
		require.Equal(t, "active", alertStatus(ctx, t, dbc, hidden), "a deleted monitor's alert is left alone")
	})

	t.Run("resolve closes open alerts and records incident recovery for each", func(t *testing.T) {
		res, err := svc.BulkResolve(ctx, tenantID, []uuid.UUID{active1, acked, resolved, foreign})
		require.NoError(t, err)

		require.Len(t, res.Items, 2)
		require.Equal(t, active1, res.Items[0].ID)
		require.Equal(t, acked, res.Items[1].ID)
		for _, item := range res.Items {
			require.Equal(t, models.AlertStatusResolved, item.Status)
			require.NotNil(t, item.ResolvedAt)
		}
		require.ElementsMatch(t, []uuid.UUID{active1, acked}, incidents.recovered)
		require.Equal(t, []alertsvc.BulkFailure{
			{ID: resolved, Reason: alertsvc.BulkReasonAlreadyResolved},
			{ID: foreign, Reason: alertsvc.BulkReasonNotFound},
		}, res.Failed)
		require.Equal(t, "active", alertStatus(ctx, t, dbc, foreign))
	})

	t.Run("a batch where nothing applies still succeeds", func(t *testing.T) {
		res, err := svc.BulkResolve(ctx, tenantID, []uuid.UUID{resolved})
		require.NoError(t, err)
		require.Empty(t, res.Items)
		require.NotNil(t, res.Items, "items encode as [] not null")
		require.Len(t, res.Failed, 1)
	})
}

func TestListAlerts_ServerSort(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "sort")
	zebra := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "Zebra")
	alpha := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "alpha")
	now := time.Now().Truncate(time.Second)

	// Two alerts share a trigger time so the id tiebreaker is exercised.
	oldResolved := insertAlert(ctx, t, dbc, tenantID, zebra, "resolved", now.Add(-5*time.Hour))
	tieA := insertAlert(ctx, t, dbc, tenantID, alpha, "active", now.Add(-time.Hour))
	tieB := insertAlert(ctx, t, dbc, tenantID, zebra, "acknowledged", now.Add(-time.Hour))
	newest := insertAlert(ctx, t, dbc, tenantID, alpha, "resolved", now.Add(-time.Minute))

	svc := alertsvc.NewService(dbc, nil)
	ids := func(sort alertsvc.ListSort, params *models.AlertListParams) []uuid.UUID {
		t.Helper()
		if params == nil {
			params = &models.AlertListParams{PageSize: 100}
		}
		res, err := svc.ListAlerts(ctx, tenantID, params, sort)
		require.NoError(t, err)
		out := make([]uuid.UUID, len(res.Items))
		for i, a := range res.Items {
			out[i] = a.ID
		}
		return out
	}
	maxID := func(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
		if a.String() > b.String() {
			return a, b
		}
		return b, a
	}
	hi, lo := maxID(tieA, tieB)

	t.Run("default is newest first with an id tiebreaker", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{newest, hi, lo, oldResolved}, ids(alertsvc.ListSort{}, nil))
	})

	t.Run("oldest first", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{oldResolved, lo, hi, newest}, ids(alertsvc.ListSort{Column: alertsvc.SortTriggeredAt}, nil))
	})

	t.Run("status ranks active, acknowledged, resolved; ties newest first", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{tieA, tieB, newest, oldResolved}, ids(alertsvc.ListSort{Column: alertsvc.SortStatus}, nil))
	})

	t.Run("status sort under a one-status filter reads newest first", func(t *testing.T) {
		resolvedOnly := models.AlertStatusResolved
		got := ids(alertsvc.ListSort{Column: alertsvc.SortStatus, Desc: true}, &models.AlertListParams{Status: &resolvedOnly, PageSize: 100})
		require.Equal(t, []uuid.UUID{newest, oldResolved}, got)
	})

	t.Run("monitor sorts case-insensitively by subject", func(t *testing.T) {
		got := ids(alertsvc.ListSort{Column: alertsvc.SortMonitor}, nil)
		require.Equal(t, []uuid.UUID{newest, tieA, tieB, oldResolved}, got, "alpha (newest first) before Zebra")
	})

	t.Run("duration longest first counts open alerts to now", func(t *testing.T) {
		got := ids(alertsvc.ListSort{Column: alertsvc.SortDuration, Desc: true}, nil)
		// Open for ~1h (two), resolved after 10 minutes (two).
		require.ElementsMatch(t, []uuid.UUID{tieA, tieB}, got[:2])
		require.Equal(t, []uuid.UUID{newest, oldResolved}, got[2:], "equal durations read newest first")
	})

	t.Run("pages are disjoint and complete under a sort", func(t *testing.T) {
		seen := map[uuid.UUID]bool{}
		for page := 1; page <= 2; page++ {
			for _, id := range ids(alertsvc.ListSort{Column: alertsvc.SortStatus}, &models.AlertListParams{Page: page, PageSize: 2}) {
				require.False(t, seen[id], "an alert appeared on two pages")
				seen[id] = true
			}
		}
		require.Len(t, seen, 4)
	})
}
