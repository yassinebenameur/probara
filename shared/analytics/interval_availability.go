package analytics

// Time-based availability headline (docs/state-semantics.md S-U1–S-U5): a
// one-bucket IntegrateTimeline (timeline.go) reduced to the headline fields.
// Per monitor:
//
//   availability % = available / (available + unplanned down)
//   coverage %     = (available + down) / window   — the observed share
//
// Planned downtime (overlap with maintenance windows, unioned so overlapping
// windows count once) leaves both numerator and denominator (S-M3); unknown
// and paused time is excluded and surfaces as lost coverage (S-U2, S-P3).
//
// Scope math matches the sampled convention (correctness-notes.md D2): each
// monitor contributes one data point; the scope value is the unweighted mean.
//
// S-U5 cutover: a window is interval-covered only when EVERY monitor's
// timeline reaches back to the window start; otherwise the caller keeps
// sampled math, labeled method="sampled". Group monitors have no timeline
// and always fall back.

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// IntervalAvailability is the time-based headline for one scope.
type IntervalAvailability struct {
	AvailabilityPct float64
	CoveragePct     float64
	// HasData is false when the whole window is unknown time for every
	// monitor in scope — there is nothing to render but no-data (S-D1).
	HasData bool
}

// computeIntervalAvailability returns (result, covered, err). covered=false
// means at least one monitor's timeline starts after the window start and
// the caller must keep sampled math (S-U5).
func (r *Repository) computeIntervalAvailability(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, start, end time.Time) (IntervalAvailability, bool, error) {
	if len(monitorIDs) == 0 || !end.After(start) {
		return IntervalAvailability{}, false, nil
	}
	res, err := r.IntegrateTimeline(ctx, tenantID, monitorIDs, []TimeRange{{Start: start, End: end}}, TimelineOpts{})
	if err != nil {
		return IntervalAvailability{}, false, err
	}

	perMonitorAvailability := make([]float64, 0, len(res.PerMonitor))
	perMonitorCoverage := make([]float64, 0, len(res.PerMonitor))
	for id, buckets := range res.PerMonitor {
		ts, ok := res.TimelineStart[id]
		if !ok || ts.After(start) {
			return IntervalAvailability{}, false, nil
		}
		s := buckets[0]
		// A covered monitor always has observed or excluded time (the
		// timeline is gapless from its first interval); nothing at all means
		// the coverage check raced a concurrent write — fall back to sampled
		// rather than misreport.
		if s.Observed()+s.Paused+s.Unknown <= 0 {
			return IntervalAvailability{}, false, nil
		}
		if pct, ok := s.AvailabilityPct(); ok {
			perMonitorAvailability = append(perMonitorAvailability, pct)
		}
		perMonitorCoverage = append(perMonitorCoverage, s.CoveragePct())
	}

	return IntervalAvailability{
		AvailabilityPct: average(perMonitorAvailability),
		CoveragePct:     average(perMonitorCoverage),
		HasData:         len(perMonitorAvailability) > 0,
	}, true, nil
}
