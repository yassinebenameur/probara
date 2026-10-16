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

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient), false)
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

	t.Run("suspected causes", func(t *testing.T) {
		causeOf := func(t *testing.T, slaID uuid.UUID, q ReportQuery) *models.SLAReport {
			t.Helper()
			r, err := svc.Report(ctx, tenantID, slaID, q)
			require.NoError(t, err)
			require.NotNil(t, r.SuspectedCauses, "every report computed now carries the breakdown")
			return r
		}
		r := causeOf(t, serial.ID, ReportQuery{Period: "2026-09"})
		require.Equal(t, 1, r.SuspectedCauses.OutageCount)
		require.Equal(t, models.SLACauseUntriaged, r.SuspectedCauses.Buckets[0].Kind, "a resolved alert with no triage is untriaged")
		require.Equal(t, models.SLACauseUntriaged, r.Outages[0].CauseKind)

		var alertID, triageID uuid.UUID
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT id FROM alerts WHERE monitor_id = $1`, b).Scan(&alertID))
		require.NoError(t, dbClient.QueryRowContext(ctx, `
			INSERT INTO alert_triage (tenant_id, alert_id, monitor_id, status, hypothesis, confidence, completed_at)
			VALUES ($1, $2, $3, 'ready', 'dns_failure', 'low', NOW()) RETURNING id
		`, tenantID, alertID, b).Scan(&triageID))
		r = causeOf(t, serial.ID, ReportQuery{Period: "2026-09"})
		require.Equal(t, 1, r.SuspectedCauses.TriagedOutageCount)
		require.Equal(t, models.SLACauseInconclusive, r.Outages[0].CauseKind,
			"a low-confidence hypothesis is not a suspected cause (ai.IsConfident)")

		_, err := dbClient.ExecContext(ctx, `UPDATE alert_triage SET confidence = 'high' WHERE id = $1`, triageID)
		require.NoError(t, err)
		r = causeOf(t, serial.ID, ReportQuery{Period: "2026-09"})
		require.Equal(t, 1, r.SuspectedCauses.TriagedOutageCount)
		require.Equal(t, "dns_failure", r.SuspectedCauses.Buckets[0].Key)
		require.Equal(t, "DNS resolution failure", r.Outages[0].CauseTitle)

		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, verdict, incident_label, answers, request, catalogue_version)
			VALUES ($1, 'alert_triage', $2, $3, 'corrected', 'tls_problem', '{}', '{}', 'test')
		`, tenantID, triageID.String(), triageID)
		require.NoError(t, err)
		r = causeOf(t, serial.ID, ReportQuery{Period: "2026-09"})
		require.Equal(t, "tls_problem", r.SuspectedCauses.Buckets[0].Key, "the operator label wins over the hypothesis")
		require.Equal(t, 1, r.SuspectedCauses.Buckets[0].CorrectedOutageCount)

		// An open alert inside the lookback is pending while the workspace
		// has auto-triage on, untriaged once it is off.
		c := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "search")
		interval(ctx, t, dbClient, tenantID, c, "up", aug1, at(now.Add(-time.Hour)))
		interval(ctx, t, dbClient, tenantID, c, "down", now.Add(-time.Hour), at(nov1))
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, 'availability', 'active', $4, 3, NOW(), NOW())
		`, uuid.New(), tenantID, c, now.Add(-50*time.Minute))
		require.NoError(t, err)
		live, err := svc.Create(ctx, tenantID, &models.CreateSLARequest{Name: "Search", TargetPct: 99.9, MonitorIDs: []string{c.String()}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = svc.Delete(ctx, tenantID, live.ID) })

		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO ai_settings (tenant_id, enabled, auto_triage_enabled) VALUES ($1, TRUE, TRUE)
			ON CONFLICT (tenant_id) DO UPDATE SET enabled = TRUE, auto_triage_enabled = TRUE
		`, tenantID)
		require.NoError(t, err)
		r = causeOf(t, live.ID, ReportQuery{})
		require.Equal(t, 1, r.SuspectedCauses.PendingOutageCount)
		require.Equal(t, models.SLACausePending, r.Outages[0].CauseKind)
		require.Contains(t, strings.Join(r.Notes, "\n"), "still pending")
		require.Equal(t, 0, r.SuspectedCauses.TriagedOutageCount)
		require.NotContains(t, strings.Join(r.Notes, "\n"), "listed as Triage pending", "the page and PDF hide the breakdown with nothing triaged")

		_, err = dbClient.ExecContext(ctx, `UPDATE ai_settings SET auto_triage_enabled = FALSE WHERE tenant_id = $1`, tenantID)
		require.NoError(t, err)
		r = causeOf(t, live.ID, ReportQuery{})
		require.Equal(t, models.SLACauseUntriaged, r.Outages[0].CauseKind)
		require.NoError(t, svc.Delete(ctx, tenantID, live.ID))
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

		// The suspected causes are frozen with it: the "suspected causes"
		// subtest left B's outage corrected to tls_problem.
		outageOf := func(r *models.SLAReport) models.SLAReportOutage {
			t.Helper()
			for _, o := range r.Outages {
				if o.MonitorID != nil && *o.MonitorID == b {
					return o
				}
			}
			t.Fatalf("no outage for monitor %s", b)
			return models.SLAReportOutage{}
		}
		require.NotNil(t, issued.SuspectedCauses)
		require.Equal(t, "tls_problem", issued.SuspectedCauses.Buckets[0].Key)
		require.Equal(t, 1, issued.SuspectedCauses.Buckets[0].CorrectedOutageCount)
		require.Equal(t, "tls_problem", outageOf(issued).CauseKey)

		_, err = svc.IssueReport(ctx, tenantID, serial.ID, "2026-09", Issuer{})
		require.ErrorIs(t, err, ErrConflict)

		// Relabelling changes the live report, never the issued one.
		_, err = dbClient.ExecContext(ctx, `
			UPDATE ai_labels SET verdict = 'confirmed', incident_label = 'dns_failure'
			WHERE tenant_id = $1 AND subject_type = 'alert_triage'`, tenantID)
		require.NoError(t, err)
		relabelled, err := svc.Report(ctx, tenantID, serial.ID, ReportQuery{Period: "2026-09"})
		require.NoError(t, err)
		require.Equal(t, "dns_failure", outageOf(relabelled).CauseKey)
		require.Equal(t, 1, relabelled.SuspectedCauses.Buckets[0].ConfirmedOutageCount)
		reread, err := svc.GetIssuedReport(ctx, tenantID, issued.Issued.ID)
		require.NoError(t, err)
		require.Equal(t, issued.SuspectedCauses, reread.SuspectedCauses)
		want, got := outageOf(issued), outageOf(reread)
		require.Equal(t, []string{want.CauseKind, want.CauseKey, want.CauseTitle}, []string{got.CauseKind, got.CauseKey, got.CauseTitle})

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

		// Another tenant's triage, label and AI settings rows that point at
		// this tenant's alerts never reach its causes (CauseJoin's and
		// ClaimableExpr's tenant predicates). X: an open alert inside the
		// lookback with no triage of its own, in a workspace with
		// auto-triage off. Y: a ready triage of its own, no label.
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO ai_settings (tenant_id, enabled, auto_triage_enabled) VALUES ($1, TRUE, FALSE), ($2, TRUE, TRUE)
			ON CONFLICT (tenant_id) DO UPDATE SET enabled = EXCLUDED.enabled, auto_triage_enabled = EXCLUDED.auto_triage_enabled
		`, tenantID, otherTenant)
		require.NoError(t, err)
		x := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "isolation-x")
		y := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "isolation-y")
		alertOf := map[uuid.UUID]uuid.UUID{}
		for _, m := range []uuid.UUID{x, y} {
			interval(ctx, t, dbClient, tenantID, m, "up", aug1, at(now.Add(-time.Hour)))
			interval(ctx, t, dbClient, tenantID, m, "down", now.Add(-time.Hour), at(nov1))
			id := uuid.New()
			_, err = dbClient.ExecContext(ctx, `
				INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
				VALUES ($1, $2, $3, 'availability', 'active', $4, 3, NOW(), NOW())
			`, id, tenantID, m, now.Add(-50*time.Minute))
			require.NoError(t, err)
			alertOf[m] = id
		}
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO alert_triage (tenant_id, alert_id, monitor_id, status, hypothesis, confidence, completed_at)
			VALUES ($1, $2, $3, 'ready', 'dns_failure', 'high', NOW())
		`, otherTenant, alertOf[x], x)
		require.NoError(t, err)
		var yTriage uuid.UUID
		require.NoError(t, dbClient.QueryRowContext(ctx, `
			INSERT INTO alert_triage (tenant_id, alert_id, monitor_id, status, hypothesis, confidence, completed_at)
			VALUES ($1, $2, $3, 'ready', 'dns_failure', 'high', NOW()) RETURNING id
		`, tenantID, alertOf[y], y).Scan(&yTriage))
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, verdict, incident_label, answers, request, catalogue_version)
			VALUES ($1, 'alert_triage', $2, $3, 'corrected', 'tls_problem', '{}', '{}', 'test')
		`, otherTenant, yTriage.String(), yTriage)
		require.NoError(t, err)

		iso, err := svc.Create(ctx, tenantID, &models.CreateSLARequest{Name: "Isolation", TargetPct: 99.9,
			MonitorIDs: []string{x.String(), y.String()}})
		require.NoError(t, err)
		r, err := svc.Report(ctx, tenantID, iso.ID, ReportQuery{})
		require.NoError(t, err)
		require.NoError(t, svc.Delete(ctx, tenantID, iso.ID))
		causes := map[uuid.UUID]string{}
		for _, o := range r.Outages {
			if o.MonitorID != nil {
				causes[*o.MonitorID] = o.CauseKey
			}
		}
		require.Equal(t, map[uuid.UUID]string{x: models.SLACauseUntriaged, y: "dns_failure"}, causes,
			"X ignores the foreign triage and the foreign workspace's auto-triage; Y ignores the foreign label")
		for _, bucket := range r.SuspectedCauses.Buckets {
			require.Zero(t, bucket.CorrectedOutageCount, bucket.Key)
		}
	})

	require.NoError(t, svc.Delete(ctx, tenantID, serial.ID))
	_, err = svc.Get(ctx, tenantID, serial.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
