package slas

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

const (
	// maxCustomDays bounds a custom range (one daily bucket per day).
	maxCustomDays = 400
	// maxReportOutages caps the per-monitor outage list; counts and MTTR
	// still cover every outage.
	maxReportOutages = 500
)

// ReportQuery selects a report's period: Period (a key of the SLA's kind),
// or From/To (local dates, inclusive), or neither for the running period.
type ReportQuery struct {
	Period string
	From   string
	To     string
}

// Report computes a live report.
func (s *Service) Report(ctx context.Context, tenantID, slaID uuid.UUID, q ReportQuery) (*models.SLAReport, error) {
	sla, err := s.Get(ctx, tenantID, slaID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return nil, fmt.Errorf("SLA timezone: %w", err)
	}
	now := s.now()
	kind := sharedanalytics.PeriodKind(sla.Period)

	var period sharedanalytics.Period
	custom := false
	switch {
	case q.From != "" || q.To != "":
		if q.From == "" || q.To == "" {
			return nil, invalid("from and to must be given together")
		}
		if q.Period != "" {
			return nil, invalid("use either period or from/to, not both")
		}
		if period, err = sharedanalytics.CustomPeriod(loc, q.From, q.To); err != nil {
			return nil, invalid("%s", err.Error())
		}
		if len(sharedanalytics.DayBuckets(loc, period.Start, period.End)) > maxCustomDays {
			return nil, invalid("a custom range spans at most %d days", maxCustomDays)
		}
		custom = true
	case q.Period != "":
		if period, err = sharedanalytics.ParsePeriod(kind, loc, q.Period); err != nil {
			return nil, invalid("%s", err.Error())
		}
	default:
		if period, err = sharedanalytics.PeriodContaining(kind, loc, now); err != nil {
			return nil, err
		}
	}
	if !period.Start.Before(now) {
		return nil, invalid("period %s has not started yet", period.Key)
	}
	return s.buildReport(ctx, tenantID, sla, period, custom, now)
}

func (s *Service) buildReport(ctx context.Context, tenantID uuid.UUID, sla *models.SLA, period sharedanalytics.Period, custom bool, now time.Time) (*models.SLAReport, error) {
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return nil, fmt.Errorf("SLA timezone: %w", err)
	}
	members, err := s.resolveMembers(ctx, tenantID, sla)
	if err != nil {
		return nil, err
	}
	end := period.End
	if end.After(now) {
		end = now
	}
	window := sharedanalytics.TimeRange{Start: period.Start, End: end}
	opts := sharedanalytics.TimelineOpts{DegradedCountsAsDown: sla.DegradedCountsAsDown}

	report := &models.SLAReport{
		SLAID: sla.ID,
		Definition: models.SLAReportDefinition{
			Name: sla.Name, Description: sla.Description, TargetPct: sla.TargetPct, Aggregation: sla.Aggregation,
			Period: sla.Period, Timezone: sla.Timezone, DegradedCountsAsDown: sla.DegradedCountsAsDown, Tags: sla.Tags,
		},
		Period: models.SLAReportPeriod{
			Key: period.Key, Start: period.Start.UTC(), End: period.End.UTC(), EffectiveEnd: end.UTC(),
			IsClosed: !period.End.After(now), IsCustom: custom,
		},
		GeneratedAt:    now,
		Daily:          []models.SLAReportDay{},
		Monitors:       []models.SLAReportMonitor{},
		ServiceOutages: []models.SLAReportOutage{},
		Outages:        []models.SLAReportOutage{},
		Notes:          []string{},
	}
	if !custom {
		report.Period.PreviousKey = period.Previous(loc).Key
		if next := period.Next(loc); next.Start.Before(now) {
			report.Period.NextKey = next.Key
		}
	}
	if !report.Period.IsClosed {
		report.Notes = append(report.Notes, "The period is still running; figures are to date.")
	}
	if len(members) == 0 {
		report.Notes = append(report.Notes, "No monitors match this SLA.")
		report.Budget = budgetFor(sla.TargetPct, report.Summary, period)
		return report, nil
	}

	days := sharedanalytics.DayBuckets(loc, window.Start, window.End)
	buckets := append([]sharedanalytics.TimeRange{window}, days...)
	ids := memberIDs(members)
	res, err := s.analytics.IntegrateTimeline(ctx, tenantID, ids, buckets, opts)
	if err != nil {
		return nil, err
	}

	report.Summary = summarize(sla, res, 0, members)
	report.Budget = budgetFor(sla.TargetPct, report.Summary, period)
	for i, day := range days {
		d := summarize(sla, res, i+1, members)
		report.Daily = append(report.Daily, models.SLAReportDay{
			Date:            day.Start.In(loc).Format("2006-01-02"),
			Start:           day.Start.UTC(),
			End:             day.End.UTC(),
			HasData:         d.HasData,
			AvailabilityPct: d.AvailabilityPct,
			CoveragePct:     d.CoveragePct,
			DownSeconds:     d.DownSeconds,
		})
	}

	outages, err := s.analytics.Outages(ctx, tenantID, ids, window, opts)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(members))
	for _, m := range members {
		names[m.ID] = m.Name
	}
	outageCount := map[uuid.UUID]int{}
	var perMonitor, composite []sharedanalytics.Outage
	for _, o := range outages {
		if o.MonitorID == nil {
			composite = append(composite, o)
			continue
		}
		perMonitor = append(perMonitor, o)
		if o.UnplannedSeconds() > 0 {
			outageCount[*o.MonitorID]++
		}
	}

	for _, m := range members {
		ss := res.PerMonitor[m.ID][0]
		row := models.SLAReportMonitor{
			ID: m.ID, Name: m.Name, Type: m.Type,
			CoveragePct:          ss.CoveragePct(),
			AvailableSeconds:     ss.Available,
			UnplannedDownSeconds: ss.UnplannedDown,
			PlannedDownSeconds:   ss.PlannedDown,
			PausedSeconds:        ss.Paused,
			UnknownSeconds:       ss.Unknown,
			UntrackedSeconds:     ss.Untracked(),
			OutageCount:          outageCount[m.ID],
		}
		if pct, ok := ss.AvailabilityPct(); ok {
			row.HasData = true
			row.AvailabilityPct = &pct
		}
		if ts, ok := res.TimelineStart[m.ID]; ok {
			t := ts
			row.TimelineStart = &t
			if ts.After(window.Start) && ts.Before(window.End) {
				report.Notes = append(report.Notes, fmt.Sprintf("%s has no state history before %s; the earlier part of the period is untracked.",
					m.Name, ts.In(loc).Format("2006-01-02 15:04 MST")))
			}
		} else {
			report.Notes = append(report.Notes, fmt.Sprintf("%s has no state history in this period.", m.Name))
		}
		report.Monitors = append(report.Monitors, row)
	}

	serial := sla.Aggregation == models.SLAAggregationSerial
	if serial {
		for _, o := range composite {
			report.ServiceOutages = append(report.ServiceOutages, outageRow(o, ""))
		}
	}
	sort.SliceStable(perMonitor, func(i, j int) bool { return perMonitor[i].Start.Before(perMonitor[j].Start) })
	for _, o := range perMonitor {
		if len(report.Outages) >= maxReportOutages {
			report.OutagesTruncated = true
			break
		}
		report.Outages = append(report.Outages, outageRow(o, names[*o.MonitorID]))
	}

	// MTTR over the outages the SLA is judged by: the service's for serial,
	// each monitor's for mean. Only spans wholly inside the period have a
	// known duration.
	judged := perMonitor
	if serial {
		judged = composite
	}
	var repaired []float64
	for _, o := range judged {
		if o.UnplannedSeconds() <= 0 {
			continue
		}
		report.Response.OutageCount++
		if !o.StartedBefore && !o.Ongoing {
			repaired = append(repaired, o.Seconds())
		}
	}
	report.Response.MTTRSeconds = meanPtr(repaired)

	alertMonitorIDs := append(append([]uuid.UUID{}, ids...), sla.MonitorIDs...)
	if err := s.alertResponse(ctx, tenantID, alertMonitorIDs, window, &report.Response); err != nil {
		return nil, err
	}
	return report, nil
}

func outageRow(o sharedanalytics.Outage, name string) models.SLAReportOutage {
	return models.SLAReportOutage{
		MonitorID:        o.MonitorID,
		MonitorName:      name,
		Start:            o.Start,
		End:              o.End,
		DurationSeconds:  o.Seconds(),
		PlannedSeconds:   o.PlannedSeconds,
		UnplannedSeconds: o.UnplannedSeconds(),
		StartedBefore:    o.StartedBefore,
		Ongoing:          o.Ongoing,
	}
}

// alertResponse fills MTTA from availability alerts triggered in the window,
// including group roll-up alerts on the SLA's explicit group monitors.
func (s *Service) alertResponse(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, window sharedanalytics.TimeRange, out *models.SLAReportResponse) error {
	var mtta sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(acknowledged_at),
			AVG(EXTRACT(EPOCH FROM acknowledged_at - triggered_at)) FILTER (WHERE acknowledged_at IS NOT NULL)
		FROM alerts
		WHERE tenant_id = $1 AND kind = 'availability' AND monitor_id = ANY($2)
		  AND triggered_at >= $3 AND triggered_at < $4
	`, tenantID, pq.Array(monitorIDs), window.Start.UTC(), window.End.UTC()).Scan(&out.AlertCount, &out.AcknowledgedCount, &mtta); err != nil {
		return fmt.Errorf("load SLA alert response: %w", err)
	}
	if mtta.Valid {
		v := mtta.Float64
		out.MTTASeconds = &v
	}
	return nil
}

// summarize reduces one bucket to the SLA's composite: the serial
// composite's seconds (S-U6), or per-monitor means for mean SLAs (the D2
// convention: each monitor one data point).
func summarize(sla *models.SLA, res *sharedanalytics.TimelineResult, idx int, members []member) models.SLAReportSummary {
	var out models.SLAReportSummary
	if sla.Aggregation == models.SLAAggregationSerial {
		s := res.Serial[idx]
		out.WindowSeconds = s.Window
		out.AvailableSeconds = s.Available
		out.DownSeconds = s.Down
		out.ExcludedSeconds = s.Excluded
		out.EligibleSeconds = s.Available + s.Down
		out.CoveragePct = s.CoveragePct()
		if pct, ok := s.AvailabilityPct(); ok {
			out.HasData = true
			out.AvailabilityPct = &pct
		}
	} else {
		var pcts, coverage []float64
		var avail, down, eligible float64
		for _, m := range members {
			ss := res.PerMonitor[m.ID][idx]
			out.WindowSeconds = ss.Window
			avail += ss.Available
			down += ss.UnplannedDown
			eligible += ss.Eligible()
			coverage = append(coverage, ss.CoveragePct())
			if pct, ok := ss.AvailabilityPct(); ok {
				pcts = append(pcts, pct)
			}
		}
		if n := float64(len(members)); n > 0 {
			out.AvailableSeconds = avail / n
			out.DownSeconds = down / n
			out.EligibleSeconds = eligible / n
			out.ExcludedSeconds = out.WindowSeconds - out.EligibleSeconds
		}
		if cov := meanPtr(coverage); cov != nil {
			out.CoveragePct = *cov
		}
		if pct := meanPtr(pcts); pct != nil {
			out.HasData = true
			out.AvailabilityPct = pct
		}
	}
	if out.AvailabilityPct != nil {
		met := metTarget(*out.AvailabilityPct, sla.TargetPct)
		out.Met = &met
	}
	return out
}

// metTarget compares with a tolerance far below display precision, so a
// period that lands exactly on target is not failed by float rounding.
func metTarget(pct, target float64) bool { return pct+1e-9 >= target }

// budgetFor derives the error budget. consumed = (1 - availability) x
// eligible, which is exactly the composite's down time for serial SLAs and
// keeps "budget left ≥ 0" equivalent to "target met" for mean SLAs.
func budgetFor(target float64, s models.SLAReportSummary, period sharedanalytics.Period) models.SLAReportBudget {
	allowedShare := 1 - target/100
	b := models.SLAReportBudget{
		AllowedSeconds:           allowedShare * s.EligibleSeconds,
		AllowedFullPeriodSeconds: allowedShare * period.End.Sub(period.Start).Seconds(),
	}
	if s.AvailabilityPct == nil {
		return b
	}
	b.ConsumedSeconds = (1 - *s.AvailabilityPct/100) * s.EligibleSeconds
	b.RemainingSeconds = b.AllowedSeconds - b.ConsumedSeconds
	if b.AllowedSeconds > 0 {
		pct := b.RemainingSeconds / b.AllowedSeconds * 100
		b.RemainingPct = &pct
	}
	return b
}

func meanPtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	m := sum / float64(len(values))
	return &m
}

// fileStem is a download filename stem: "<sla-name>-<period>".
func fileStem(r *models.SLAReport) string {
	var b strings.Builder
	for _, c := range strings.ToLower(r.Definition.Name + "-" + r.Period.Key) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		case c == '.' || c == ' ' || c == '_':
			b.WriteRune('-')
		}
	}
	stem := strings.Trim(b.String(), "-")
	if stem == "" {
		return "sla-report"
	}
	return "sla-" + stem
}
