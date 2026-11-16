package slas

import (
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

// Error-budget burn (docs/state-semantics.md S-U8). Everything here reduces
// the buckets the report already integrates: the short burn windows ride as
// extra buckets on the report's one IntegrateTimeline call, and the
// burn-down accumulates its daily buckets. No other interval query.

// burnWindowLengths are the short windows' lengths, in display order.
var burnWindowLengths = []struct {
	key    string
	length time.Duration
}{
	{models.SLABurnWindowHour, time.Hour},
	{models.SLABurnWindowDay, 24 * time.Hour},
}

// burnWindows returns the short windows of a running period, ending at end
// and clipped to the period start so they measure this period's budget.
// Closed periods have none: their pace no longer matters.
func burnWindows(period sharedanalytics.Period, end time.Time, closed bool) []burnBucket {
	if closed {
		return nil
	}
	out := make([]burnBucket, 0, len(burnWindowLengths))
	for _, w := range burnWindowLengths {
		start := end.Add(-w.length)
		if start.Before(period.Start) {
			start = period.Start
		}
		if !end.After(start) {
			continue
		}
		out = append(out, burnBucket{key: w.key, rng: sharedanalytics.TimeRange{Start: start, End: end}})
	}
	return out
}

type burnBucket struct {
	key string
	rng sharedanalytics.TimeRange
}

// burnRate is (1 - A) / (1 - target) for one summarized window: nil without
// data, and nil when the target leaves no budget (a 100% target), where any
// rate would be infinite and JSON cannot carry it.
func burnRate(target float64, s models.SLAReportSummary) *float64 {
	allowed := 1 - target/100
	if s.AvailabilityPct == nil || allowed <= 0 {
		return nil
	}
	rate := (1 - *s.AvailabilityPct/100) / allowed
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return nil
	}
	if rate < 0 {
		rate = 0 // float noise on a fully available window
	}
	return &rate
}

// buildBurn assembles a report's burn section. res holds bucket 0 (the
// report window), the day buckets from index 1, then the short windows from
// shortStart on (shortStart < 0 when there are none).
func buildBurn(sla *models.SLA, res *sharedanalytics.TimelineResult, members []member, period sharedanalytics.Period,
	window sharedanalytics.TimeRange, days []sharedanalytics.TimeRange, short []burnBucket, shortStart int,
	budget models.SLAReportBudget, summary models.SLAReportSummary, closed bool, loc *time.Location) *models.SLAReportBurn {
	burn := &models.SLAReportBurn{
		Windows:  []models.SLAReportBurnWindow{},
		BurnDown: burnDown(sla, res, days, members, period, loc),
	}
	var day *models.SLAReportSummary
	var dayRange sharedanalytics.TimeRange
	for i, w := range short {
		s := summarize(sla, res, shortStart+i, members)
		burn.Windows = append(burn.Windows, models.SLAReportBurnWindow{
			Key: w.key, Start: w.rng.Start.UTC(), End: w.rng.End.UTC(),
			AvailabilityPct: s.AvailabilityPct, BurnRate: burnRate(sla.TargetPct, s),
		})
		if w.key == models.SLABurnWindowDay {
			day, dayRange = &s, w.rng
		}
	}
	burn.Windows = append(burn.Windows, models.SLAReportBurnWindow{
		Key: models.SLABurnWindowPeriod, Start: window.Start.UTC(), End: window.End.UTC(),
		AvailabilityPct: summary.AvailabilityPct, BurnRate: burnRate(sla.TargetPct, summary),
	})
	burn.Exhaustion, burn.ProjectedExhaustionAt = projectExhaustion(sla.TargetPct, budget, summary, day, dayRange, period, closed)
	return burn
}

// emptyBurn is the burn section of a report with no monitors.
func emptyBurn(window sharedanalytics.TimeRange) *models.SLAReportBurn {
	return &models.SLAReportBurn{
		Windows: []models.SLAReportBurnWindow{{
			Key: models.SLABurnWindowPeriod, Start: window.Start.UTC(), End: window.End.UTC(),
		}},
		Exhaustion: models.SLABudgetNoData,
		BurnDown:   []models.SLAReportBurnPoint{},
	}
}

// projectExhaustion classifies the full period's budget (S-U8). A running
// period projects at the pace of the last 24 hours (clipped to the period):
// budget consumed in that window per wall-clock second. The comparison stays
// in float seconds, so a slow burn cannot overflow time.Duration.
func projectExhaustion(target float64, budget models.SLAReportBudget, summary models.SLAReportSummary,
	day *models.SLAReportSummary, dayRange sharedanalytics.TimeRange, period sharedanalytics.Period, closed bool) (string, *time.Time) {
	if summary.AvailabilityPct == nil {
		return models.SLABudgetNoData, nil
	}
	full := budget.AllowedFullPeriodSeconds
	left := full - budget.ConsumedSeconds
	if budget.ConsumedSeconds > 0 && left <= 0 {
		return models.SLABudgetExhausted, nil
	}
	if closed {
		return models.SLABudgetNotProjected, nil
	}
	if day == nil || day.AvailabilityPct == nil {
		return models.SLABudgetNoData, nil
	}
	consumed := budgetFor(target, *day, period).ConsumedSeconds
	wall := dayRange.Seconds()
	if consumed <= 0 || wall <= 0 {
		return models.SLABudgetNotProjected, nil
	}
	wait := left / (consumed / wall)
	if math.IsNaN(wait) || wait >= period.End.Sub(dayRange.End).Seconds() {
		return models.SLABudgetNotProjected, nil
	}
	at := dayRange.End.Add(time.Duration(wait * float64(time.Second))).UTC()
	return models.SLABudgetProjected, &at
}

// burnDown is the S-U8 burn-down: after each day, the full period's error
// budget left. Each point summarizes the period start through that day, so
// it is exact for both aggregations (per-monitor and composite seconds add
// up across day buckets; a mean SLA re-averages each monitor's cumulative
// availability), and the last point consumes exactly the headline budget.
func burnDown(sla *models.SLA, res *sharedanalytics.TimelineResult, days []sharedanalytics.TimeRange, members []member,
	period sharedanalytics.Period, loc *time.Location) []models.SLAReportBurnPoint {
	out := make([]models.SLAReportBurnPoint, 0, len(days))
	if len(days) == 0 {
		return out
	}
	cum := &sharedanalytics.TimelineResult{
		PerMonitor: make(map[uuid.UUID][]sharedanalytics.StateSeconds, len(members)),
		Serial:     make([]sharedanalytics.SerialSeconds, 1),
	}
	for _, m := range members {
		cum.PerMonitor[m.ID] = make([]sharedanalytics.StateSeconds, 1)
	}
	for i, day := range days {
		idx := i + 1 // bucket 0 is the whole window
		s, d := &cum.Serial[0], res.Serial[idx]
		s.Available += d.Available
		s.Down += d.Down
		s.Excluded += d.Excluded
		s.Window += d.Window
		for _, m := range members {
			c, d := &cum.PerMonitor[m.ID][0], res.PerMonitor[m.ID][idx]
			c.Available += d.Available
			c.UnplannedDown += d.UnplannedDown
			c.PlannedDown += d.PlannedDown
			c.Paused += d.Paused
			c.Unknown += d.Unknown
			c.Window += d.Window
		}
		sum := summarize(sla, cum, 0, members)
		b := budgetFor(sla.TargetPct, sum, period)
		point := models.SLAReportBurnPoint{
			Date: day.Start.In(loc).Format("2006-01-02"), End: day.End.UTC(), ConsumedSeconds: b.ConsumedSeconds,
		}
		if sum.AvailabilityPct != nil && b.AllowedFullPeriodSeconds > 0 {
			pct := (b.AllowedFullPeriodSeconds - b.ConsumedSeconds) / b.AllowedFullPeriodSeconds * 100
			point.RemainingPct = &pct
		}
		out = append(out, point)
	}
	return out
}
