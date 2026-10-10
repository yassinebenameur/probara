package slas

// SLA service tests (docs/state-semantics.md S-U6, S-U7): definitions,
// membership resolution, live vs issued reports and exports.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func interval(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID, state string, start time.Time, end *time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, $3, 'result', $4, $5)
	`, tenantID, monitorID, state, start, end)
	require.NoError(t, err)
}

func at(t time.Time) *time.Time { return &t }

func TestSLAService(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "sla")
	a := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	b := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "checkout")
	group := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "payments")
	testutil.AddMonitorToGroup(ctx, t, dbClient, b, group)
	_, err := dbClient.ExecContext(ctx, `UPDATE monitors SET tags = ARRAY['payments'] WHERE id = $1`, group)
	require.NoError(t, err)
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "sla-other")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "foreign")

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	// A up throughout; B (reached through the tagged group) down one hour on
	// Sept 10. Intervals are closed past the fixed clock: an open interval
	// ends at the database's real NOW(), which this test does not control.
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	downStart := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	interval(ctx, t, dbClient, tenantID, a, "up", aug1, at(nov1))
	interval(ctx, t, dbClient, tenantID, b, "up", aug1, at(downStart))
	interval(ctx, t, dbClient, tenantID, b, "down", downStart, at(downStart.Add(time.Hour)))
	interval(ctx, t, dbClient, tenantID, b, "up", downStart.Add(time.Hour), at(nov1))
	_, err = dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, acknowledged_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', $4, $5, $6, 3, NOW(), NOW())
	`, uuid.New(), tenantID, b, downStart.Add(5*time.Minute), downStart.Add(15*time.Minute), downStart.Add(time.Hour))
	require.NoError(t, err)

	t.Run("create validates", func(t *testing.T) {
		cases := map[string]models.CreateSLARequest{
			"at least one monitor or tag": {Name: "x", TargetPct: 99.9},
			"target_pct":                  {Name: "x", TargetPct: 100, Tags: []string{"payments"}},
			"IANA timezone":               {Name: "x", TargetPct: 99.9, Timezone: "Mars/Olympus", Tags: []string{"payments"}},
			"period must be":              {Name: "x", TargetPct: 99.9, Period: "daily", Tags: []string{"payments"}},
			"monitors not found":          {Name: "x", TargetPct: 99.9, MonitorIDs: []string{foreign.String()}},
			"name is required":            {Name: "  ", TargetPct: 99.9, Tags: []string{"payments"}},
		}
		for want, req := range cases {
			req := req
			_, err := svc.Create(ctx, tenantID, &req)
			var bad Invalid
			require.ErrorAs(t, err, &bad, want)
			require.Contains(t, err.Error(), want)
		}
	})

	serial, err := svc.Create(ctx, tenantID, &models.CreateSLARequest{
		Name: "Checkout platform", TargetPct: 99.9, MonitorIDs: []string{a.String()}, Tags: []string{"payments", " payments "},
	})
	require.NoError(t, err)
	require.Equal(t, "serial", serial.Aggregation)
	require.Equal(t, "monthly", serial.Period)
	require.Equal(t, "UTC", serial.Timezone)
	require.Equal(t, []string{"payments"}, serial.Tags)

	_, err = svc.Create(ctx, tenantID, &models.CreateSLARequest{Name: "Checkout platform", TargetPct: 99, Tags: []string{"payments"}})
	require.ErrorIs(t, err, ErrConflict)

	t.Run("serial report", func(t *testing.T) {
		r, err := svc.Report(ctx, tenantID, serial.ID, ReportQuery{Period: "2026-09"})
		require.NoError(t, err)
		require.True(t, r.Period.IsClosed)
		require.Equal(t, "2026-08", r.Period.PreviousKey)
		require.Equal(t, "2026-10", r.Period.NextKey)
		require.Len(t, r.Monitors, 2, "group expands to its member, never itself")
		require.NotNil(t, r.Summary.AvailabilityPct)
		require.InDelta(t, 719.0/720*100, *r.Summary.AvailabilityPct, 1e-6)
		require.False(t, *r.Summary.Met)
		require.InDelta(t, 3600, r.Budget.ConsumedSeconds, 1e-6)
		require.InDelta(t, 0.001*720*3600, r.Budget.AllowedSeconds, 1e-3)
		require.Less(t, r.Budget.RemainingSeconds, 0.0)
		require.Len(t, r.Daily, 30)
		require.InDelta(t, 3600, r.Daily[9].DownSeconds, 1e-6)
		require.Len(t, r.ServiceOutages, 1)
		require.Equal(t, 1, r.Response.OutageCount)
		require.InDelta(t, 3600, *r.Response.MTTRSeconds, 1e-6)
		require.Equal(t, 1, r.Response.AlertCount)
		require.InDelta(t, 600, *r.Response.MTTASeconds, 1e-6)

		var csvBuf, pdfBuf bytes.Buffer
		require.NoError(t, WriteCSV(&csvBuf, r))
		require.Contains(t, csvBuf.String(), "Availability %")
		require.Contains(t, csvBuf.String(), "checkout")
		require.NoError(t, WritePDF(&pdfBuf, r))
		require.True(t, bytes.HasPrefix(pdfBuf.Bytes(), []byte("%PDF")))
		require.Equal(t, "sla-checkout-platform-2026-09.pdf", FileName(r, FormatPDF))
	})

	t.Run("mean aggregation", func(t *testing.T) {
		agg := models.SLAAggregationMean
		mean, err := svc.Update(ctx, tenantID, serial.ID, &models.UpdateSLARequest{Aggregation: &agg})
		require.NoError(t, err)
		r, err := svc.Report(ctx, tenantID, mean.ID, ReportQuery{Period: "2026-09"})
		require.NoError(t, err)
		require.InDelta(t, (100+719.0/720*100)/2, *r.Summary.AvailabilityPct, 1e-6)
		require.True(t, *r.Summary.Met)
		require.GreaterOrEqual(t, r.Budget.RemainingSeconds, 0.0, "budget left ≥ 0 exactly when met")
		require.Empty(t, r.ServiceOutages)
		ser := models.SLAAggregationSerial
		_, err = svc.Update(ctx, tenantID, serial.ID, &models.UpdateSLARequest{Aggregation: &ser})
		require.NoError(t, err)
	})

	t.Run("running period and custom range", func(t *testing.T) {
		r, err := svc.Report(ctx, tenantID, serial.ID, ReportQuery{})
		require.NoError(t, err)
		require.Equal(t, "2026-10", r.Period.Key)
		require.False(t, r.Period.IsClosed)
		require.True(t, r.Period.EffectiveEnd.Equal(now))
		require.Empty(t, r.Period.NextKey, "November has not started")

		r, err = svc.Report(ctx, tenantID, serial.ID, ReportQuery{From: "2026-09-10", To: "2026-09-10"})
		require.NoError(t, err)
		require.True(t, r.Period.IsCustom)
		require.InDelta(t, 23.0/24*100, *r.Summary.AvailabilityPct, 1e-6)

		_, err = svc.Report(ctx, tenantID, serial.ID, ReportQuery{Period: "2026-11"})
		require.ErrorAs(t, err, new(Invalid))
		_, err = svc.Report(ctx, tenantID, serial.ID, ReportQuery{From: "2025-01-01", To: "2026-09-01"})
		require.ErrorAs(t, err, new(Invalid))
	})

	t.Run("list and monitor filter", func(t *testing.T) {
		all, err := svc.List(ctx, tenantID, uuid.Nil)
		require.NoError(t, err)
		require.Len(t, all, 1)
		require.NotNil(t, all[0].Current)
		require.Equal(t, 2, all[0].Current.MonitorCount)
		byMember, err := svc.List(ctx, tenantID, b)
		require.NoError(t, err)
		require.Len(t, byMember, 1)
		none, err := svc.List(ctx, tenantID, uuid.New())
		require.NoError(t, err)
		require.Empty(t, none)
	})

	t.Run("issued reports are frozen", func(t *testing.T) {
		_, err := svc.IssueReport(ctx, tenantID, serial.ID, "2026-10", Issuer{})
		require.ErrorAs(t, err, new(Invalid), "running period cannot be issued")

		issued, err := svc.IssueReport(ctx, tenantID, serial.ID, "", Issuer{})
		require.NoError(t, err)
		require.Equal(t, "2026-09", issued.Period.Key, "default is the last closed period")
		require.NotNil(t, issued.Issued)
		frozen := *issued.Summary.AvailabilityPct

		_, err = svc.IssueReport(ctx, tenantID, serial.ID, "2026-09", Issuer{})
		require.ErrorIs(t, err, ErrConflict)

		// Wiping history changes the live report, never the issued one.
		_, err = dbClient.ExecContext(ctx, `DELETE FROM monitor_state_intervals WHERE monitor_id = $1 AND state = 'down'`, b)
		require.NoError(t, err)
		live, err := svc.Report(ctx, tenantID, serial.ID, ReportQuery{Period: "2026-09"})
		require.NoError(t, err)
		require.NotEqual(t, frozen, *live.Summary.AvailabilityPct)

		again, err := svc.GetIssuedReport(ctx, tenantID, issued.Issued.ID)
		require.NoError(t, err)
		require.Equal(t, frozen, *again.Summary.AvailabilityPct)

		refs, err := svc.ListReports(ctx, tenantID, serial.ID)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		require.True(t, strings.EqualFold(refs[0].PeriodKey, "2026-09"))
	})

	t.Run("tenant isolation", func(t *testing.T) {
		_, err := svc.Get(ctx, otherTenant, serial.ID)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = svc.Report(ctx, otherTenant, serial.ID, ReportQuery{})
		require.ErrorIs(t, err, ErrNotFound)
		refs, err := svc.ListReports(ctx, tenantID, serial.ID)
		require.NoError(t, err)
		_, err = svc.GetIssuedReport(ctx, otherTenant, refs[0].ID)
		require.ErrorIs(t, err, ErrNotFound)
		require.ErrorIs(t, svc.Delete(ctx, otherTenant, serial.ID), ErrNotFound)
	})

	require.NoError(t, svc.Delete(ctx, tenantID, serial.ID))
	_, err = svc.Get(ctx, tenantID, serial.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
