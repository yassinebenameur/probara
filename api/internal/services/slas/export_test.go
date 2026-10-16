package slas

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/ai"
)

var updateGolden = flag.Bool("update", false, "rewrite api/internal/services/slas/testdata golden files")

// legacyReport is a report document as issued before suspected causes
// existed: no suspected_causes and no outage cause fields.
func legacyReport() *models.SLAReport {
	monitorID := uuid.MustParse("6f1c2a7e-0d4b-4a8e-9a51-1d2f3e4a5b6c")
	pct, cov, mttr, mtta := 99.86111, 100.0, 3600.0, 600.0
	met := false
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	down := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	return &models.SLAReport{
		SLAID: uuid.MustParse("0e9d8c7b-6a5f-4e3d-8c2b-1a0f9e8d7c6b"),
		Definition: models.SLAReportDefinition{
			Name: "Checkout platform", TargetPct: 99.9, Aggregation: models.SLAAggregationMean, Period: "monthly",
			Timezone: "Europe/Paris", Tags: []string{"payments"},
		},
		Period: models.SLAReportPeriod{
			Key: "2026-09", Start: start, End: end, EffectiveEnd: end, IsClosed: true, PreviousKey: "2026-08", NextKey: "2026-10",
		},
		GeneratedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Issued: &models.SLAReportIssue{ID: uuid.MustParse("9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"),
			IssuedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), IssuedBy: "admin"},
		Summary: models.SLAReportSummary{HasData: true, AvailabilityPct: &pct, CoveragePct: cov, Met: &met,
			WindowSeconds: 2592000, AvailableSeconds: 2588400, DownSeconds: 3600, EligibleSeconds: 2592000},
		Budget: models.SLAReportBudget{AllowedSeconds: 2592, ConsumedSeconds: 3600, RemainingSeconds: -1008, AllowedFullPeriodSeconds: 2592},
		Daily: []models.SLAReportDay{{Date: "2026-09-10", Start: down.Truncate(24 * time.Hour), End: down.Truncate(24 * time.Hour).Add(24 * time.Hour),
			HasData: true, AvailabilityPct: &pct, CoveragePct: 100, DownSeconds: 3600}},
		Monitors: []models.SLAReportMonitor{{ID: monitorID, Name: "checkout", Type: "http", HasData: true, AvailabilityPct: &pct,
			CoveragePct: 100, AvailableSeconds: 2588400, UnplannedDownSeconds: 3600, OutageCount: 1}},
		ServiceOutages: []models.SLAReportOutage{},
		Outages: []models.SLAReportOutage{{MonitorID: &monitorID, MonitorName: "checkout", Start: down, End: down.Add(time.Hour),
			DurationSeconds: 3600, UnplannedSeconds: 3600}},
		Response: models.SLAReportResponse{OutageCount: 1, MTTRSeconds: &mttr, AlertCount: 1, AcknowledgedCount: 1, MTTASeconds: &mtta},
		Notes:    []string{"checkout has no state history before 2026-09-02 00:00 CEST; the earlier part of the period is untracked."},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// TestLegacyIssuedReportRendersUnchanged pins a report issued before
// suspected causes existed: the goldens were written by the exporter as it
// stood before the breakdown was added, so the JSON document round-trips
// byte for byte and the CSV is exactly what it was.
func TestLegacyIssuedReportRendersUnchanged(t *testing.T) {
	doc, err := json.Marshal(legacyReport())
	require.NoError(t, err)
	golden(t, "legacy-report.json", doc)

	var decoded models.SLAReport
	require.NoError(t, json.Unmarshal(doc, &decoded))
	again, err := json.Marshal(&decoded)
	require.NoError(t, err)
	require.Equal(t, string(doc), string(again), "a legacy document re-encodes unchanged")

	var csvBuf bytes.Buffer
	require.NoError(t, WriteCSV(&csvBuf, &decoded))
	golden(t, "legacy-report.csv", csvBuf.Bytes())

	var pdfBuf bytes.Buffer
	require.NoError(t, WritePDF(&pdfBuf, &decoded))
	require.True(t, bytes.HasPrefix(pdfBuf.Bytes(), []byte("%PDF")))
}

// TestReportWithCausesExports pins the cause additions: two trailing outage
// columns, a trailing cause section, and the PDF caveat's lookback from
// ai.TriageLookback.
func TestReportWithCausesExports(t *testing.T) {
	r := legacyReport()
	r.Issued = nil
	r.Outages[0].CauseKind, r.Outages[0].CauseKey, r.Outages[0].CauseTitle = models.SLACauseSuspected, "dns_failure", "DNS resolution failure"
	r.SuspectedCauses = &models.SLAReportSuspectedCauses{OutageCount: 1, TriagedOutageCount: 1, UnplannedSeconds: 3600,
		Buckets: []models.SLAReportCauseBucket{{Kind: models.SLACauseSuspected, Key: "dns_failure", Title: "DNS resolution failure",
			OutageCount: 1, AlertCount: 1, MonitorCount: 1, UnplannedSeconds: 3600, ConfirmedOutageCount: 1}}}

	var csvBuf bytes.Buffer
	require.NoError(t, WriteCSV(&csvBuf, r))
	out := csvBuf.String()
	require.Contains(t, out, "Started before period,Ongoing,Cause key,Suspected cause\r\n")
	require.Contains(t, out, ",false,false,dns_failure,DNS resolution failure\r\n")
	require.Contains(t, out, "\r\n\r\nAttributed monitor outages,1\r\nTriaged monitor outages,1\r\nPending monitor outages,0\r\n"+
		"Unplanned seconds (summed per monitor),3600\r\n")
	require.True(t, strings.HasSuffix(out, "\r\nDNS resolution failure,dns_failure,suspected,1,1,1,3600,1,0\r\n"), "the cause section is last")

	require.Contains(t, causesCaveat(r.SuspectedCauses), "older than the "+lookbackPhrase(ai.TriageLookback)+" lookback")
	require.Equal(t, "6-hour", lookbackPhrase(6*time.Hour))
	require.Equal(t, "90-minute", lookbackPhrase(90*time.Minute))
	require.Contains(t, causesCaveat(r.SuspectedCauses), "1 of 1 monitor outages had a triaged alert.")
	var pdfBuf bytes.Buffer
	require.NoError(t, WritePDF(&pdfBuf, r))
	require.True(t, bytes.HasPrefix(pdfBuf.Bytes(), []byte("%PDF")))
}

// TestCSVSuspectedCauseSection pins the shape of a report computed with the
// breakdown: the outage cause columns (a no-alert outage included) and the
// trailing section, two-cell summary rows then one row per bucket.
func TestCSVSuspectedCauseSection(t *testing.T) {
	r := legacyReport()
	r.Issued = nil
	second := uuid.MustParse("7a2d3b8f-1e5c-4b9f-8a62-2e3f4a5b6c7d")
	quiet := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	r.Outages[0].CauseKind, r.Outages[0].CauseKey, r.Outages[0].CauseTitle = models.SLACauseSuspected, "dns_failure", "DNS resolution failure"
	r.Outages = append(r.Outages, models.SLAReportOutage{MonitorID: &second, MonitorName: "search", Start: quiet, End: quiet.Add(10 * time.Minute),
		DurationSeconds: 600, UnplannedSeconds: 600, CauseKind: models.SLACauseNoAlert, CauseKey: models.SLACauseNoAlert, CauseTitle: "No alert"})
	r.SuspectedCauses = &models.SLAReportSuspectedCauses{OutageCount: 2, TriagedOutageCount: 1, UnplannedSeconds: 4200,
		Buckets: []models.SLAReportCauseBucket{
			{Kind: models.SLACauseSuspected, Key: "dns_failure", Title: "DNS resolution failure",
				OutageCount: 1, AlertCount: 1, MonitorCount: 1, UnplannedSeconds: 3600, CorrectedOutageCount: 1},
			{Kind: models.SLACauseNoAlert, Key: models.SLACauseNoAlert, Title: "No alert",
				OutageCount: 1, MonitorCount: 1, UnplannedSeconds: 600},
		}}

	var csvBuf bytes.Buffer
	require.NoError(t, WriteCSV(&csvBuf, r))
	golden(t, "causes-report.csv", csvBuf.Bytes())
}
