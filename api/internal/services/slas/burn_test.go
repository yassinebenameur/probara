package slas

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

// Error-budget burn tests pin docs/state-semantics.md S-U8.

const day = 86400.0

func ptr[T any](v T) *T { return &v }

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

// TestBurnRate pins S-U8: burn rate = (1 - A) / (1 - target).
func TestBurnRate(t *testing.T) {
	require.InDelta(t, 2.0, *burnRate(99.9, models.SLAReportSummary{AvailabilityPct: ptr(99.8)}), 1e-9)
	require.InDelta(t, 1.0, *burnRate(99.9, models.SLAReportSummary{AvailabilityPct: ptr(99.9)}), 1e-9, "exactly on budget")
	require.InDelta(t, 0.0, *burnRate(99.9, models.SLAReportSummary{AvailabilityPct: ptr(100.0)}), 1e-9)
	require.Nil(t, burnRate(99.9, models.SLAReportSummary{}), "no data, no burn rate (S-D1)")
	// A 100% target leaves no budget: any rate would be infinite, which
	// JSON cannot carry, so there is none.
	require.Nil(t, burnRate(100, models.SLAReportSummary{AvailabilityPct: ptr(99.0)}))
	require.Nil(t, burnRate(100, models.SLAReportSummary{AvailabilityPct: ptr(100.0)}))
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
			require.Equal(t, days[2].End, points[2].End)

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

// TestBurnDownEmptyAndZeroBudget: a period with nothing observed renders as
// no data, never as a full budget; a 100% target has no budget to chart.
func TestBurnDownEmptyAndZeroBudget(t *testing.T) {
	period, days, res, members := burnFixture(t)
	empty := &sharedanalytics.TimelineResult{
		PerMonitor: map[uuid.UUID][]sharedanalytics.StateSeconds{members[0].ID: make([]sharedanalytics.StateSeconds, 4), members[1].ID: make([]sharedanalytics.StateSeconds, 4)},
		Serial:     make([]sharedanalytics.SerialSeconds, 4),
	}
	points := burnDown(&models.SLA{TargetPct: 99.9, Aggregation: models.SLAAggregationSerial}, empty, days, members, period, time.UTC)
	require.Len(t, points, 3)
	for _, p := range points {
		require.Nil(t, p.RemainingPct, "nothing observed yet renders as no data, never 100%%")
	}
	require.Empty(t, burnDown(&models.SLA{TargetPct: 99.9}, empty, nil, members, period, time.UTC))
	require.NotNil(t, burnDown(&models.SLA{TargetPct: 99.9}, empty, nil, members, period, time.UTC), "never null in JSON")

	for _, p := range burnDown(&models.SLA{TargetPct: 100, Aggregation: models.SLAAggregationSerial}, res, days, members, period, time.UTC) {
		require.Nil(t, p.RemainingPct, "no budget, no remaining share")
	}
}

func TestBurnWindowsClipToThePeriod(t *testing.T) {
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)

	mid := period.Start.Add(10 * 24 * time.Hour)
	ws := burnWindows(period, mid, false)
	require.Len(t, ws, 2)
	require.Equal(t, models.SLABurnWindowHour, ws[0].key)
	require.Equal(t, mid.Add(-time.Hour), ws[0].rng.Start)
	require.Equal(t, models.SLABurnWindowDay, ws[1].key)
	require.Equal(t, mid.Add(-24*time.Hour), ws[1].rng.Start)

	// Partial period: 30 minutes in, both windows start at the period start.
	early := period.Start.Add(30 * time.Minute)
	ws = burnWindows(period, early, false)
	require.Len(t, ws, 2)
	for _, w := range ws {
		require.Equal(t, period.Start, w.rng.Start)
		require.Equal(t, early, w.rng.End)
	}

	require.Empty(t, burnWindows(period, period.End, true), "a closed period has no short windows")
}

func TestProjectExhaustion(t *testing.T) {
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)
	tenDays := period.Start.Add(10 * 24 * time.Hour)
	dayRange := sharedanalytics.TimeRange{Start: tenDays.Add(-24 * time.Hour), End: tenDays}
	// so far: consumed seconds of budget over eligible seconds observed.
	so := func(consumed, eligible float64) models.SLAReportSummary {
		return models.SLAReportSummary{AvailabilityPct: ptr((1 - consumed/eligible) * 100), EligibleSeconds: eligible}
	}
	// A last-24h window that consumed down seconds of budget.
	recent := func(down float64) *models.SLAReportSummary {
		s := so(down, day)
		return &s
	}
	running := func(s models.SLAReportSummary, d *models.SLAReportSummary) (string, *time.Time) {
		return projectExhaustion(99.9, s, d, dayRange, period, tenDays, false)
	}
	closed := func(s models.SLAReportSummary) string {
		state, at := projectExhaustion(99.9, s, nil, sharedanalytics.TimeRange{}, period, period.End, true)
		require.Nil(t, at)
		return state
	}

	// Ten fully covered days in: the budget is 0.1% of 10 observed days plus
	// 20 to come (2592s). 1296s left, burning 129.6s a day: ten more days.
	state, at := running(so(1296, 10*day), recent(129.6))
	require.Equal(t, models.SLABudgetProjected, state)
	require.WithinDuration(t, tenDays.Add(10*24*time.Hour), *at, time.Second)

	state, at = running(so(1296, 10*day), recent(10))
	require.Equal(t, models.SLABudgetNotProjected, state, "a slow burn outlasts the period")
	require.Nil(t, at)
	state, _ = running(so(1296, 10*day), recent(0))
	require.Equal(t, models.SLABudgetNotProjected, state, "a clean last day projects nothing, whatever was spent before")
	state, _ = running(so(2592, 10*day), recent(600))
	require.Equal(t, models.SLABudgetExhausted, state, "even a clean rest of the period cannot meet the target")
	state, _ = running(models.SLAReportSummary{}, nil)
	require.Equal(t, models.SLABudgetNoData, state, "nothing observed in the period")
	state, _ = running(so(500, 10*day), &models.SLAReportSummary{})
	require.Equal(t, models.SLABudgetNoData, state, "nothing observed in the last 24 hours")
	state, _ = running(so(1e-9, 10*day), recent(1e-12))
	require.Equal(t, models.SLABudgetNotProjected, state, "a tiny burn cannot overflow time.Duration")

	// Closed: exhausted exactly when the headline is breached.
	require.Equal(t, models.SLABudgetExhausted, closed(so(2593, 30*day)))
	require.Equal(t, models.SLABudgetNotProjected, closed(so(1296, 30*day)), "budget left: it lasted the period")

	// A 100% target: no budget at all, so any downtime exhausts it.
	state, _ = projectExhaustion(100, so(1, 10*day), recent(1), dayRange, period, tenDays, false)
	require.Equal(t, models.SLABudgetExhausted, state)
	state, _ = projectExhaustion(100, so(0, 10*day), recent(0), dayRange, period, tenDays, false)
	require.Equal(t, models.SLABudgetNotProjected, state)
}

// TestClosedPeriodBurnAgreesWithTheHeadline: a monthly 99.9% SLA whose
// monitor was paused for the first half of September and down 30 minutes
// in the second. Only observed time earns budget (1296s), so the period is
// breached; the budget section must say so too: 139% used, burn rate 1.39x,
// exhausted, and the burn-down's last point equals the headline's remaining
// share. Measured against the whole calendar month it would have read
// "Lasted the period" beside "Breached".
func TestClosedPeriodBurnAgreesWithTheHeadline(t *testing.T) {
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodMonthly, time.UTC, "2026-09")
	require.NoError(t, err)
	days := sharedanalytics.DayBuckets(time.UTC, period.Start, period.End)
	require.Len(t, days, 30)
	m := member{ID: uuid.New(), Name: "checkout"}
	res := &sharedanalytics.TimelineResult{
		PerMonitor: map[uuid.UUID][]sharedanalytics.StateSeconds{m.ID: make([]sharedanalytics.StateSeconds, 31)},
		Serial:     make([]sharedanalytics.SerialSeconds, 31),
	}
	for i := range days {
		d := sharedanalytics.StateSeconds{Window: day}
		sd := sharedanalytics.SerialSeconds{Window: day}
		switch {
		case i < 15:
			d.Paused, sd.Excluded = day, day
		case i == 20:
			d.Available, d.UnplannedDown = day-1800, 1800
			sd.Available, sd.Down = day-1800, 1800
		default:
			d.Available, sd.Available = day, day
		}
		res.PerMonitor[m.ID][i+1], res.Serial[i+1] = d, sd
		w, sw := &res.PerMonitor[m.ID][0], &res.Serial[0]
		w.Available += d.Available
		w.UnplannedDown += d.UnplannedDown
		w.Paused += d.Paused
		w.Window += d.Window
		sw.Available += sd.Available
		sw.Down += sd.Down
		sw.Excluded += sd.Excluded
		sw.Window += sd.Window
	}
	for _, agg := range []string{models.SLAAggregationSerial, models.SLAAggregationMean} {
		t.Run(agg, func(t *testing.T) {
			sla := &models.SLA{TargetPct: 99.9, Aggregation: agg}
			summary := summarize(sla, res, 0, []member{m})
			budget := budgetFor(sla.TargetPct, summary, period)
			require.False(t, *summary.Met, "breached")
			require.InDelta(t, 1296, budget.AllowedSeconds, 1e-6)
			require.InDelta(t, 1800.0/1296*100, budget.ConsumedSeconds/budget.AllowedSeconds*100, 1e-6, "139% used")

			b := buildBurn(sla, res, []member{m}, period, period.Range(), days, nil, -1, summary, true, time.UTC)
			require.Equal(t, models.SLABudgetExhausted, b.Exhaustion)
			require.InDelta(t, 1800.0/1296, *b.Windows[0].BurnRate, 1e-9)
			last := b.BurnDown[len(b.BurnDown)-1]
			require.InDelta(t, *budget.RemainingPct, *last.RemainingPct, 1e-9, "the burn-down ends on the headline")
			require.Nil(t, b.BurnDown[0].RemainingPct, "nothing observed while paused")
			// Mid-month, before the outage, the whole budget the period
			// could still earn is intact.
			require.InDelta(t, 100, *b.BurnDown[16].RemainingPct, 1e-9)

			r := &models.SLAReport{Period: models.SLAReportPeriod{IsClosed: true}, Burn: b}
			require.Equal(t, "Exhausted: the period's error budget is spent", ExhaustionText(r, time.UTC))
		})
	}
}

// TestBuildBurnRunningPeriod drives the whole burn section from timeline
// buckets laid out the way buildReport requests them, for a period 3 days in.
func TestBuildBurnRunningPeriod(t *testing.T) {
	period, days, res, members := burnFixture(t)
	end := days[2].End
	short := burnWindows(period, end, false)
	// Short windows as buckets 4 (last hour, clean) and 5 (last 24h = day 3).
	shortStart := len(res.Serial)
	res.Serial = append(res.Serial, sharedanalytics.SerialSeconds{Available: 3600, Window: 3600}, res.Serial[3])
	for _, m := range members {
		res.PerMonitor[m.ID] = append(res.PerMonitor[m.ID], sharedanalytics.StateSeconds{Available: 3600, Window: 3600}, res.PerMonitor[m.ID][3])
	}
	sla := &models.SLA{TargetPct: 99.9, Aggregation: models.SLAAggregationSerial}
	summary := summarize(sla, res, 0, members)
	window := sharedanalytics.TimeRange{Start: period.Start, End: end}

	b := buildBurn(sla, res, members, period, window, days, short, shortStart, summary, false, time.UTC)
	require.Equal(t, []string{models.SLABurnWindowHour, models.SLABurnWindowDay, models.SLABurnWindowPeriod},
		[]string{b.Windows[0].Key, b.Windows[1].Key, b.Windows[2].Key})
	require.InDelta(t, 0, *b.Windows[0].BurnRate, 1e-9)
	require.InDelta(t, (600/day)/0.001, *b.Windows[1].BurnRate, 1e-9)
	require.InDelta(t, (2400/(3*day))/0.001, *b.Windows[2].BurnRate, 1e-9)
	// 192s left (2592 - 2400) at 600s a day: 7.68 hours after the 24h window ends.
	require.Equal(t, models.SLABudgetProjected, b.Exhaustion)
	require.WithinDuration(t, end.Add(time.Duration(192.0/600.0*day*float64(time.Second))), *b.ProjectedExhaustionAt, time.Second)
	require.Len(t, b.BurnDown, 3)

	// Closed, the same data has only the period window and no projection.
	closed := buildBurn(sla, res, members, period, window, days, nil, -1, summary, true, time.UTC)
	require.Len(t, closed.Windows, 1)
	require.Equal(t, models.SLABudgetNotProjected, closed.Exhaustion)
	require.Nil(t, closed.ProjectedExhaustionAt)
}

// TestBurnSurvivesJSON: a 100% target or an empty period must still encode
// (an Inf or NaN would make json.Marshal, and so issuing, fail).
func TestBurnSurvivesJSON(t *testing.T) {
	period, days, res, members := burnFixture(t)
	for _, target := range []float64{100, 99.9} {
		sla := &models.SLA{TargetPct: target, Aggregation: models.SLAAggregationMean}
		summary := summarize(sla, res, 0, members)
		budget := budgetFor(target, summary, period)
		window := sharedanalytics.TimeRange{Start: period.Start, End: days[2].End}
		r := &models.SLAReport{Budget: budget, Summary: summary,
			Burn: buildBurn(sla, res, members, period, window, days, nil, -1, summary, false, time.UTC)}
		_, err := json.Marshal(r)
		require.NoError(t, err)
	}
	_, err := json.Marshal(&models.SLAReport{Burn: emptyBurn(sharedanalytics.TimeRange{Start: period.Start, End: period.End})})
	require.NoError(t, err)
}

// burnReport is legacyReport recomputed with the burn section: the golden
// pins the CSV's trailing burn rows.
func burnReport() *models.SLAReport {
	r := legacyReport()
	r.Issued = nil
	left := (2592.0 - 3600.0) / 2592.0 * 100
	r.Burn = &models.SLAReportBurn{
		Windows: []models.SLAReportBurnWindow{{
			Key: models.SLABurnWindowPeriod, Start: r.Period.Start, End: r.Period.End,
			AvailabilityPct: r.Summary.AvailabilityPct, BurnRate: ptr(3600.0 / 2592.0),
		}},
		Exhaustion: models.SLABudgetExhausted,
		BurnDown: []models.SLAReportBurnPoint{
			{Date: "2026-09-09", End: time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC), ConsumedSeconds: 0, RemainingPct: ptr(100.0)},
			{Date: "2026-09-10", End: time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC), ConsumedSeconds: 3600, RemainingPct: &left},
		},
	}
	return r
}

// TestReportWithBurnExports pins the burn section: last in the CSV, after
// every row a report computed before burn existed has; the PDF renders.
func TestReportWithBurnExports(t *testing.T) {
	before := legacyReport()
	before.Issued = nil
	var legacy bytes.Buffer
	require.NoError(t, WriteCSV(&legacy, before))

	r := burnReport()
	var csvBuf bytes.Buffer
	require.NoError(t, WriteCSV(&csvBuf, r))
	require.True(t, bytes.HasPrefix(csvBuf.Bytes(), legacy.Bytes()), "no existing row moves")
	require.Contains(t, csvBuf.String(), "\r\nBudget exhaustion,exhausted,\r\n")
	golden(t, "burn-report.csv", csvBuf.Bytes())

	doc, err := json.Marshal(r)
	require.NoError(t, err)
	golden(t, "burn-report.json", doc)

	var pdfBuf bytes.Buffer
	require.NoError(t, WritePDF(&pdfBuf, r))
	require.True(t, bytes.HasPrefix(pdfBuf.Bytes(), []byte("%PDF")))

	// A running period with a projection and a day without data.
	r.Period.IsClosed = false
	r.Burn.Exhaustion = models.SLABudgetProjected
	r.Burn.ProjectedExhaustionAt = ptr(time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC))
	r.Burn.BurnDown = append(r.Burn.BurnDown, models.SLAReportBurnPoint{Date: "2026-09-11", End: time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC)})
	require.Equal(t, "Runs out 20 Sep 2026 10:00 at the last 24 hours' pace", ExhaustionText(r, reportLocation(r)))
	pdfBuf.Reset()
	require.NoError(t, WritePDF(&pdfBuf, r))
}

func TestExhaustionText(t *testing.T) {
	r := burnReport()
	loc := time.UTC
	require.Equal(t, "", ExhaustionText(legacyReport(), loc), "no burn section, no sentence")
	require.Equal(t, "Exhausted: the period's error budget is spent", ExhaustionText(r, loc))
	r.Burn.Exhaustion = models.SLABudgetNotProjected
	require.Equal(t, "Lasted the period", ExhaustionText(r, loc))
	r.Period.IsClosed = false
	require.Equal(t, "Not projected to run out this period", ExhaustionText(r, loc))
	r.Burn.Exhaustion = models.SLABudgetNoData
	require.Equal(t, "No data to project", ExhaustionText(r, loc))
	require.Equal(t, "1.39x", BurnRateText(ptr(3600.0/2592.0)))
	require.Equal(t, "-", BurnRateText(nil))
}
