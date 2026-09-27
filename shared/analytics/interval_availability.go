package analytics

// Time-based availability (docs/state-semantics.md S-U1–S-U5): the headline
// number is an integration of monitor_state_intervals over the window, not a
// sample count. Per monitor:
//
//   available   = time in up / suspect / degraded (S-U3: degraded and
//                 mid-confirmation time count as available)
//   unavailable = time in down, minus overlap with maintenance windows —
//                 planned downtime leaves both numerator and denominator
//                 (S-M3). The overlap sum is clamped to total down time;
//                 KNOWN LIMIT: maintenance windows that overlap each other
//                 (or reach a monitor both directly and via its group) count
//                 the shared segment twice within that clamp, over-excluding
//                 planned downtime. Range-union dedup is deferred.
//   excluded    = unknown time (absence of evidence, including paused time,
//                 which the timeline records as unknown intervals — S-P3)
//
//   availability % = available / (available + unplanned down)
//   coverage %     = (available + down) / window   — the observed share
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
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
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

	var uncovered int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM unnest($1::uuid[]) AS m(id)
		WHERE NOT EXISTS (
			SELECT 1 FROM monitor_state_intervals i
			WHERE i.monitor_id = m.id AND i.started_at <= $2
		)
	`, pq.Array(monitorIDs), start).Scan(&uncovered); err != nil {
		return IntervalAvailability{}, false, fmt.Errorf("check timeline coverage: %w", err)
	}
	if uncovered > 0 {
		return IntervalAvailability{}, false, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		WITH clipped AS (
			SELECT i.monitor_id, i.state,
				GREATEST(i.started_at, $2::timestamptz) AS s,
				LEAST(COALESCE(i.ended_at, NOW()), $3::timestamptz) AS e
			FROM monitor_state_intervals i
			WHERE i.tenant_id = $4
			  AND i.monitor_id = ANY($1)
			  AND i.started_at < $3
			  AND COALESCE(i.ended_at, NOW()) > $2
		),
		planned AS (
			SELECT c.monitor_id,
				SUM(EXTRACT(EPOCH FROM LEAST(c.e, mw.ends_at) - GREATEST(c.s, mw.starts_at))) AS seconds
			FROM clipped c
			JOIN maintenance_windows mw ON mw.tenant_id = $4
				AND mw.starts_at < c.e AND mw.ends_at > c.s
			JOIN maintenance_window_monitors mwm ON mwm.maintenance_window_id = mw.id
				AND (mwm.monitor_id = c.monitor_id OR mwm.monitor_id IN (
					SELECT mg.group_id FROM monitor_groups mg WHERE mg.monitor_id = c.monitor_id))
			WHERE c.state = 'down'
			GROUP BY c.monitor_id
		)
		SELECT
			c.monitor_id,
			COALESCE(SUM(EXTRACT(EPOCH FROM c.e - c.s)) FILTER (WHERE c.state IN ('up','suspect','degraded')), 0),
			COALESCE(SUM(EXTRACT(EPOCH FROM c.e - c.s)) FILTER (WHERE c.state = 'down'), 0),
			COALESCE(MAX(p.seconds), 0)
		FROM clipped c
		LEFT JOIN planned p ON p.monitor_id = c.monitor_id
		GROUP BY c.monitor_id
	`, pq.Array(monitorIDs), start, end, tenantID)
	if err != nil {
		return IntervalAvailability{}, false, fmt.Errorf("integrate state intervals: %w", err)
	}
	defer rows.Close()

	windowSeconds := end.Sub(start).Seconds()
	perMonitorAvailability := make([]float64, 0, len(monitorIDs))
	perMonitorCoverage := make([]float64, 0, len(monitorIDs))
	seen := 0
	for rows.Next() {
		var monitorID uuid.UUID
		var avail, down, planned float64
		if err := rows.Scan(&monitorID, &avail, &down, &planned); err != nil {
			return IntervalAvailability{}, false, fmt.Errorf("scan interval availability: %w", err)
		}
		seen++
		if planned > down {
			planned = down
		}
		unplannedDown := down - planned
		if denom := avail + unplannedDown; denom > 0 {
			perMonitorAvailability = append(perMonitorAvailability, (avail/denom)*100)
		}
		perMonitorCoverage = append(perMonitorCoverage, ((avail+down)/windowSeconds)*100)
	}
	if err := rows.Err(); err != nil {
		return IntervalAvailability{}, false, fmt.Errorf("iterate interval availability: %w", err)
	}
	// A covered monitor always has clipped rows (the timeline is gapless from
	// its first interval); anything else means the coverage check raced a
	// concurrent write — fall back to sampled rather than misreport.
	if seen < len(monitorIDs) {
		return IntervalAvailability{}, false, nil
	}

	out := IntervalAvailability{
		AvailabilityPct: average(perMonitorAvailability),
		CoveragePct:     average(perMonitorCoverage),
		HasData:         len(perMonitorAvailability) > 0,
	}
	return out, true, nil
}
