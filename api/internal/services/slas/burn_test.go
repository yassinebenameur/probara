package slas

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

const day = 86400.0

// burnFixture is three days of September 2026 (UTC) for two monitors: a
// 30-minute outage on monitor a on day 2, a 10-minute one on b on day 3.
func burnFixture(t *testing.T) (sharedanalytics.Period, []sharedanalytics.TimeRange, *sharedanalytics.TimelineResult, []member) {
	t.Helper()
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)
	days := sharedanalytics.DayBuckets(time.UTC, period.Start, period.Start.Add(72*time.Hour))
	require.Len(t, days, 3)
	a, b := member{ID: uuid.New(), Name: "a"}, member{ID: uuid.New(), Name: "b"}
	downA := []float64{0, 1800, 0}
	downB := []float64{0, 0, 600}
	res := &sharedanalytics.TimelineResult{
		PerMonitor: map[uuid.UUID][]sharedanalytics.StateSeconds{a.ID: make([]sharedanalytics.StateSeconds, 4), b.ID: make([]sharedanalytics.StateSeconds, 4)},
		Serial:     make([]sharedanalytics.SerialSeconds, 4),
	}
	for i := range days {
		idx := i + 1
		res.PerMonitor[a.ID][idx] = sharedanalytics.StateSeconds{Available: day - downA[i], UnplannedDown: downA[i], Window: day}
		res.PerMonitor[b.ID][idx] = sharedanalytics.StateSeconds{Available: day - downB[i], UnplannedDown: downB[i], Window: day}
		down := downA[i] + downB[i]
		res.Serial[idx] = sharedanalytics.SerialSeconds{Available: day - down, Down: down, Window: day}
		for _, m := range []member{a, b} {
			w := &res.PerMonitor[m.ID][0]
			d := res.PerMonitor[m.ID][idx]
			w.Available += d.Available
			w.UnplannedDown += d.UnplannedDown
			w.Window += d.Window
		}
		res.Serial[0].Available += res.Serial[idx].Available
		res.Serial[0].Down += res.Serial[idx].Down
		res.Serial[0].Window += res.Serial[idx].Window
	}
	return period, days, res, []member{a, b}
}

// TestBudgetBurnRate pins S-U8: burn rate = (1 - A) / (1 - target).
func TestBudgetBurnRate(t *testing.T) {
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)
	pct := 99.8
	b := budgetFor(99.9, models.SLAReportSummary{AvailabilityPct: &pct, EligibleSeconds: 1000 * day}, period)
	require.NotNil(t, b.BurnRate)
	require.InDelta(t, (1-0.998)/(1-0.999), *b.BurnRate, 1e-9)
	require.InDelta(t, 2.0, *b.BurnRate, 1e-9)

	none := budgetFor(99.9, models.SLAReportSummary{}, period)
	require.Nil(t, none.BurnRate, "no data, no burn rate (S-D1)")
}

// TestBurnDownIsCumulativeAndEndsAtTheHeadline pins the burn-down for both
// aggregations: each point is the period start through that day, measured
// against the full period's allowance, and the last point consumes exactly
// what the headline budget does.
func TestBurnDownIsCumulativeAndEndsAtTheHeadline(t *testing.T) {
	period, days, res, members := burnFixture(t)
	full := 0.001 * period.End.Sub(period.Start).Seconds() // 2592s at 99.9%

	for _, agg := range []string{models.SLAAggregationSerial, models.SLAAggregationMean} {
		t.Run(agg, func(t *testing.T) {
			sla := &models.SLA{TargetPct: 99.9, Aggregation: agg}
			points := burnDown(sla, res, days, members, period, time.UTC)
			require.Len(t, points, 3)
			require.Equal(t, []string{"2026-09-01", "2026-09-02", "2026-09-03"},
				[]string{points[0].Date, points[1].Date, points[2].Date})

			headline := budgetFor(sla.TargetPct, summarize(sla, res, 0, members), period)
			require.InDelta(t, headline.ConsumedSeconds, points[2].ConsumedSeconds, 1e-6)
			for _, p := range points {
				require.NotNil(t, p.RemainingPct)
				require.InDelta(t, (full-p.ConsumedSeconds)/full*100, *p.RemainingPct, 1e-9)
			}
			require.InDelta(t, 0, points[0].ConsumedSeconds, 1e-9)
			require.InDelta(t, 100, *points[0].RemainingPct, 1e-9)
			if agg == models.SLAAggregationSerial {
				// Serial consumption is the composite's down time.
				require.InDelta(t, 1800, points[1].ConsumedSeconds, 1e-6)
				require.InDelta(t, 2400, points[2].ConsumedSeconds, 1e-6)
			} else {
				// Mean: each monitor's cumulative availability, averaged.
				require.InDelta(t, 900, points[1].ConsumedSeconds, 1e-6)
				require.InDelta(t, 1200, points[2].ConsumedSeconds, 1e-6)
			}
		})
	}
}

func TestBurnDownWithoutDataIsNil(t *testing.T) {
	period, days, _, members := burnFixture(t)
	empty := &sharedanalytics.TimelineResult{
		PerMonitor: map[uuid.UUID][]sharedanalytics.StateSeconds{members[0].ID: make([]sharedanalytics.StateSeconds, 4), members[1].ID: make([]sharedanalytics.StateSeconds, 4)},
		Serial:     make([]sharedanalytics.SerialSeconds, 4),
	}
	points := burnDown(&models.SLA{TargetPct: 99.9, Aggregation: models.SLAAggregationSerial}, empty, days, members, period, time.UTC)
	require.Len(t, points, 3)
	for _, p := range points {
		require.Nil(t, p.RemainingPct, "nothing observed yet renders as no data, never 100%%")
	}
	require.Nil(t, burnDown(&models.SLA{TargetPct: 99.9}, empty, nil, members, period, time.UTC))
}

func TestProjectExhaustion(t *testing.T) {
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)
	full := 2592.0
	rate := 1.0
	tenDays := period.Start.Add(10 * 24 * time.Hour)
	budget := func(consumed float64) models.SLAReportBudget {
		return models.SLAReportBudget{AllowedFullPeriodSeconds: full, ConsumedSeconds: consumed, BurnRate: &rate}
	}

	// 1296s consumed in 10 days: the other 1296s last 10 more days.
	got := projectExhaustion(budget(1296), period, tenDays)
	require.NotNil(t, got)
	require.Equal(t, period.Start.Add(20*24*time.Hour), *got)

	require.Nil(t, projectExhaustion(budget(500), period, tenDays), "slow burn outlasts the period")
	require.Nil(t, projectExhaustion(budget(0), period, tenDays), "nothing consumed")
	require.Nil(t, projectExhaustion(budget(full+1), period, tenDays), "already spent")
	require.Nil(t, projectExhaustion(budget(1296), period, period.End), "closed period")
	require.Nil(t, projectExhaustion(models.SLAReportBudget{AllowedFullPeriodSeconds: full, ConsumedSeconds: 1296}, period, tenDays), "no data")
	require.Nil(t, projectExhaustion(budget(1e-12), period, period.Start.Add(time.Second)), "a tiny burn cannot overflow")
}

// TestReportWithBurnExports pins the burn section: last in the CSV, after
// every row a report computed before burn existed has.
func TestReportWithBurnExports(t *testing.T) {
	r := legacyReport()
	r.Issued = nil
	rate := 3600.0 / 2592.0
	left := (2592.0 - 3600.0) / 2592.0 * 100
	r.Budget.BurnRate = &rate
	r.BurnDown = []models.SLAReportBurnPoint{
		{Date: "2026-09-09", ConsumedSeconds: 0, RemainingPct: ptr(100.0)},
		{Date: "2026-09-10", ConsumedSeconds: 3600, RemainingPct: &left},
	}

	before := legacyReport()
	before.Issued = nil
	var legacy bytes.Buffer
	require.NoError(t, WriteCSV(&legacy, before))
	var csvBuf bytes.Buffer
	require.NoError(t, WriteCSV(&csvBuf, r))
	require.True(t, bytes.HasPrefix(csvBuf.Bytes(), legacy.Bytes()), "no existing row moves")
	require.Contains(t, csvBuf.String(), "\r\n\r\nBudget burn rate,1.3889\r\nProjected budget exhaustion,\r\n")
	golden(t, "burn-report.csv", csvBuf.Bytes())

	var pdfBuf bytes.Buffer
	require.NoError(t, WritePDF(&pdfBuf, r))
	require.True(t, bytes.HasPrefix(pdfBuf.Bytes(), []byte("%PDF")))
}

func ptr[T any](v T) *T { return &v }
