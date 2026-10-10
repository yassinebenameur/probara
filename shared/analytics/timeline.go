package analytics

// Timeline integration (docs/state-semantics.md S-U1–S-U7): the single
// implementation behind the monitor availability headline and SLA reports.
// Every number is seconds of monitor_state_intervals clipped to a bucket.
//
// Per monitor and bucket, time falls in exactly one class:
//
//   available      up / suspect / degraded (S-U3; degraded moves to down
//                  when DegradedCountsAsDown is set — the per-SLA knob)
//   unplanned down down time outside every maintenance window reaching the
//                  monitor (directly or via one of its groups)
//   planned down   down time inside such a window (S-M3). Windows are
//                  unioned before intersecting, so overlapping windows (or a
//                  window reaching a monitor twice) count once.
//   paused         unknown intervals opened by a pause (S-P3)
//   unknown        every other unknown interval (S-F2)
//   untracked      bucket time no interval covers: before the monitor's
//                  timeline starts, or after the database clock's now
//
// The serial composite (S-U6) treats the scope as one service: down whenever
// any member is in unplanned down, otherwise excluded whenever any member is
// planned down / paused / unknown / untracked, otherwise available.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TimeRange is a half-open [Start, End) span.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Seconds is the span's length in seconds (0 for an inverted span).
func (r TimeRange) Seconds() float64 {
	if !r.End.After(r.Start) {
		return 0
	}
	return r.End.Sub(r.Start).Seconds()
}

// TimelineOpts tunes classification.
type TimelineOpts struct {
	// DegradedCountsAsDown makes degraded time unplanned/planned down instead
	// of available (S-U3's knob; off everywhere except SLAs that opt in).
	DegradedCountsAsDown bool
}

// StateSeconds is one bucket's time breakdown.
type StateSeconds struct {
	Available     float64
	UnplannedDown float64
	PlannedDown   float64
	Paused        float64
	Unknown       float64
	Window        float64
}

// Observed is time with evidence: available plus all down time. Coverage is
// Observed ÷ Window — the same definition the monitor headline reports.
func (s StateSeconds) Observed() float64 { return s.Available + s.UnplannedDown + s.PlannedDown }

// Eligible is the availability denominator (S-U2).
func (s StateSeconds) Eligible() float64 { return s.Available + s.UnplannedDown }

// Untracked is window time no interval covers.
func (s StateSeconds) Untracked() float64 {
	u := s.Window - s.Observed() - s.Paused - s.Unknown
	if u < 0 {
		return 0
	}
	return u
}

// AvailabilityPct is available ÷ eligible; ok=false when nothing is
// eligible, which renders as no-data, never 0% or 100% (S-D1).
func (s StateSeconds) AvailabilityPct() (float64, bool) {
	d := s.Eligible()
	if d <= 0 {
		return 0, false
	}
	return s.Available / d * 100, true
}

// CoveragePct is the observed share of the window.
func (s StateSeconds) CoveragePct() float64 {
	if s.Window <= 0 {
		return 0
	}
	return s.Observed() / s.Window * 100
}

// SerialSeconds is the composite service breakdown for one bucket (S-U6).
type SerialSeconds struct {
	Available float64
	Down      float64
	Excluded  float64
	Window    float64
}

func (s SerialSeconds) AvailabilityPct() (float64, bool) {
	d := s.Available + s.Down
	if d <= 0 {
		return 0, false
	}
	return s.Available / d * 100, true
}

func (s SerialSeconds) CoveragePct() float64 {
	if s.Window <= 0 {
		return 0
	}
	return (s.Available + s.Down) / s.Window * 100
}

// TimelineResult holds per-bucket breakdowns, indexed like the input buckets.
type TimelineResult struct {
	Buckets []TimeRange
	// PerMonitor[monitorID][bucketIndex]. Every requested monitor has an
	// entry, zero-filled where it has no intervals.
	PerMonitor map[uuid.UUID][]StateSeconds
	Serial     []SerialSeconds
	// TimelineStart is each monitor's earliest interval; absent when the
	// monitor has no timeline at all.
	TimelineStart map[uuid.UUID]time.Time
}

// secondsOf sums a tstzmultirange expression's length in seconds.
func secondsOf(expr string) string {
	return `(SELECT COALESCE(SUM(EXTRACT(EPOCH FROM upper(x) - lower(x))), 0) FROM unnest(` + expr + `) AS x)`
}

// IntegrateTimeline integrates the state timeline of monitorIDs over each
// bucket in one query. Buckets must be non-empty, half-open and may be
// arbitrary (a single window, or calendar days in any timezone).
func (r *Repository) IntegrateTimeline(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, buckets []TimeRange, opts TimelineOpts) (*TimelineResult, error) {
	monitorIDs = dedupeUUIDs(monitorIDs)
	out := &TimelineResult{
		Buckets:       buckets,
		PerMonitor:    make(map[uuid.UUID][]StateSeconds, len(monitorIDs)),
		Serial:        make([]SerialSeconds, len(buckets)),
		TimelineStart: make(map[uuid.UUID]time.Time, len(monitorIDs)),
	}
	for i, b := range buckets {
		out.Serial[i].Window = b.Seconds()
	}
	for _, id := range monitorIDs {
		per := make([]StateSeconds, len(buckets))
		for i, b := range buckets {
			per[i].Window = b.Seconds()
		}
		out.PerMonitor[id] = per
	}
	if len(monitorIDs) == 0 || len(buckets) == 0 {
		return out, nil
	}

	starts := make([]time.Time, len(buckets))
	ends := make([]time.Time, len(buckets))
	for i, b := range buckets {
		if !b.End.After(b.Start) {
			return nil, fmt.Errorf("bucket %d is empty", i)
		}
		starts[i] = b.Start.UTC()
		ends[i] = b.End.UTC()
	}

	tsRows, err := r.db.QueryContext(ctx, `
		SELECT monitor_id, MIN(started_at)
		FROM monitor_state_intervals
		WHERE tenant_id = $1 AND monitor_id = ANY($2)
		GROUP BY monitor_id
	`, tenantID, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("load timeline starts: %w", err)
	}
	for tsRows.Next() {
		var id uuid.UUID
		var ts time.Time
		if err := tsRows.Scan(&id, &ts); err != nil {
			tsRows.Close()
			return nil, fmt.Errorf("scan timeline start: %w", err)
		}
		out.TimelineStart[id] = ts.UTC()
	}
	if err := tsRows.Close(); err != nil {
		return nil, fmt.Errorf("iterate timeline starts: %w", err)
	}

	// $1 monitors, $2/$3 bucket bounds, $4 tenant, $5 degraded-as-down.
	// Open intervals end at the database clock's NOW(), the clock the
	// timeline is written with (monitorstate.RecordIntervalTx).
	query := `
		WITH buckets AS (
			SELECT (b.idx - 1)::int AS idx, b.bs, b.be
			FROM unnest($2::timestamptz[], $3::timestamptz[]) WITH ORDINALITY AS b(bs, be, idx)
		),
		grid AS (
			SELECT b.idx, b.bs, b.be, m.id AS monitor_id
			FROM buckets b CROSS JOIN unnest($1::uuid[]) AS m(id)
		),
		classed AS (
			SELECT b.idx, i.monitor_id,
				CASE
					WHEN i.state = 'down' OR (i.state = 'degraded' AND $5) THEN 'down'
					WHEN i.state IN ('up', 'suspect', 'degraded') THEN 'avail'
					WHEN i.reason = 'pause' THEN 'paused'
					ELSE 'unknown'
				END AS cls,
				tstzrange(GREATEST(i.started_at, b.bs), LEAST(COALESCE(i.ended_at, NOW()), b.be)) AS r
			FROM buckets b
			JOIN monitor_state_intervals i
				ON i.tenant_id = $4
				AND i.monitor_id = ANY($1)
				AND i.started_at < b.be
				AND COALESCE(i.ended_at, NOW()) > b.bs
			WHERE GREATEST(i.started_at, b.bs) < LEAST(COALESCE(i.ended_at, NOW()), b.be)
		),
		per_state AS (
			SELECT idx, monitor_id,
				range_agg(r) FILTER (WHERE cls = 'avail') AS avail,
				range_agg(r) FILTER (WHERE cls = 'down') AS down,
				range_agg(r) FILTER (WHERE cls = 'paused') AS paused,
				range_agg(r) FILTER (WHERE cls = 'unknown') AS unknown
			FROM classed
			GROUP BY idx, monitor_id
		),
		maint AS (
			SELECT g.idx, g.monitor_id,
				range_agg(tstzrange(GREATEST(mw.starts_at, g.bs), LEAST(mw.ends_at, g.be))) AS mr
			FROM grid g
			JOIN maintenance_windows mw
				ON mw.tenant_id = $4 AND mw.starts_at < g.be AND mw.ends_at > g.bs
			WHERE EXISTS (
				SELECT 1 FROM maintenance_window_monitors mwm
				WHERE mwm.maintenance_window_id = mw.id
				  AND (mwm.monitor_id = g.monitor_id OR mwm.monitor_id IN (
					SELECT mg.group_id FROM monitor_groups mg WHERE mg.monitor_id = g.monitor_id))
			)
			GROUP BY g.idx, g.monitor_id
		),
		split AS (
			SELECT g.idx, g.monitor_id,
				tstzmultirange(tstzrange(g.bs, g.be)) AS win,
				COALESCE(p.avail, '{}') AS avail,
				COALESCE(p.down, '{}') - COALESCE(mt.mr, '{}') AS unplanned,
				COALESCE(p.down, '{}') * COALESCE(mt.mr, '{}') AS planned,
				COALESCE(p.paused, '{}') AS paused,
				COALESCE(p.unknown, '{}') AS unknown
			FROM grid g
			LEFT JOIN per_state p ON p.idx = g.idx AND p.monitor_id = g.monitor_id
			LEFT JOIN maint mt ON mt.idx = g.idx AND mt.monitor_id = g.monitor_id
		),
		serial_parts AS (
			SELECT idx,
				range_agg(unplanned) AS down,
				-- Anything that is not available or unplanned down: planned
				-- down, paused, unknown and untracked time.
				range_agg(win - avail - unplanned) AS excluded
			FROM split
			GROUP BY idx
		),
		serial AS (
			SELECT s.idx, tstzmultirange(tstzrange(b.bs, b.be)) AS win, s.down, s.excluded
			FROM serial_parts s JOIN buckets b ON b.idx = s.idx
		)
		SELECT idx, monitor_id,
			` + secondsOf("avail") + `,
			` + secondsOf("unplanned") + `,
			` + secondsOf("planned") + `,
			` + secondsOf("paused") + `,
			` + secondsOf("unknown") + `
		FROM split
		UNION ALL
		SELECT idx, NULL,
			` + secondsOf("win - down - (excluded - down)") + `,
			` + secondsOf("down") + `,
			` + secondsOf("excluded - down") + `,
			0, 0
		FROM serial
	`
	rows, err := r.db.QueryContext(ctx, query, pq.Array(monitorIDs), pq.Array(starts), pq.Array(ends), tenantID, opts.DegradedCountsAsDown)
	if err != nil {
		return nil, fmt.Errorf("integrate timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var idx int
		var monitorID uuid.NullUUID
		var a, d, p, pa, u float64
		if err := rows.Scan(&idx, &monitorID, &a, &d, &p, &pa, &u); err != nil {
			return nil, fmt.Errorf("scan timeline row: %w", err)
		}
		if idx < 0 || idx >= len(buckets) {
			return nil, fmt.Errorf("timeline row for unknown bucket %d", idx)
		}
		if !monitorID.Valid {
			out.Serial[idx].Available = a
			out.Serial[idx].Down = d
			out.Serial[idx].Excluded = p
			continue
		}
		per, ok := out.PerMonitor[monitorID.UUID]
		if !ok {
			continue
		}
		per[idx].Available = a
		per[idx].UnplannedDown = d
		per[idx].PlannedDown = p
		per[idx].Paused = pa
		per[idx].Unknown = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timeline rows: %w", err)
	}
	return out, nil
}

// Outage is one contiguous down span of a monitor (or, with a nil MonitorID,
// of the serial composite), clipped to the query window.
type Outage struct {
	MonitorID *uuid.UUID
	Start     time.Time
	End       time.Time
	// PlannedSeconds is the share inside maintenance windows (S-M3).
	PlannedSeconds float64
	// StartedBefore / Ongoing mark spans clipped by the window: the outage
	// began before it, or had not ended by its end.
	StartedBefore bool
	Ongoing       bool
}

func (o Outage) Seconds() float64 { return o.End.Sub(o.Start).Seconds() }

// UnplannedSeconds is the outage's contribution to unavailability.
func (o Outage) UnplannedSeconds() float64 {
	u := o.Seconds() - o.PlannedSeconds
	if u < 0 {
		return 0
	}
	return u
}

// Outages lists merged down spans per monitor over [window.Start,
// window.End), plus the serial composite's unplanned-down spans (MonitorID
// nil) — the composite never includes planned time, so its PlannedSeconds
// is 0. Ordered by start.
func (r *Repository) Outages(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, window TimeRange, opts TimelineOpts) ([]Outage, error) {
	monitorIDs = dedupeUUIDs(monitorIDs)
	if len(monitorIDs) == 0 || !window.End.After(window.Start) {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH downs AS (
			SELECT i.monitor_id,
				range_agg(tstzrange(GREATEST(i.started_at, $2), LEAST(COALESCE(i.ended_at, NOW()), $3))) AS mr,
				bool_or(i.ended_at IS NULL) AS open_now
			FROM monitor_state_intervals i
			WHERE i.tenant_id = $4
			  AND i.monitor_id = ANY($1)
			  AND (i.state = 'down' OR (i.state = 'degraded' AND $5))
			  AND i.started_at < $3
			  AND COALESCE(i.ended_at, NOW()) > $2
			  AND GREATEST(i.started_at, $2) < LEAST(COALESCE(i.ended_at, NOW()), $3)
			GROUP BY i.monitor_id
		),
		maint AS (
			SELECT d.monitor_id,
				range_agg(tstzrange(GREATEST(mw.starts_at, $2), LEAST(mw.ends_at, $3))) AS mr
			FROM downs d
			JOIN maintenance_windows mw
				ON mw.tenant_id = $4 AND mw.starts_at < $3 AND mw.ends_at > $2
			WHERE EXISTS (
				SELECT 1 FROM maintenance_window_monitors mwm
				WHERE mwm.maintenance_window_id = mw.id
				  AND (mwm.monitor_id = d.monitor_id OR mwm.monitor_id IN (
					SELECT mg.group_id FROM monitor_groups mg WHERE mg.monitor_id = d.monitor_id))
			)
			GROUP BY d.monitor_id
		),
		spans AS (
			SELECT d.monitor_id, x AS span, d.open_now,
				`+secondsOf("tstzmultirange(x) * COALESCE(mt.mr, '{}')")+` AS planned,
				tstzmultirange(x) - COALESCE(mt.mr, '{}') AS unplanned
			FROM downs d
			LEFT JOIN maint mt ON mt.monitor_id = d.monitor_id
			CROSS JOIN LATERAL unnest(d.mr) AS x
		),
		composite AS (
			SELECT unnest(range_agg(unplanned)) AS span, bool_or(open_now) AS open_now
			FROM spans
		)
		SELECT monitor_id, lower(span), upper(span), planned,
			open_now AND upper(span) >= LEAST(NOW(), $3) - interval '1 second'
		FROM spans
		UNION ALL
		SELECT NULL, lower(span), upper(span), 0,
			open_now AND upper(span) >= LEAST(NOW(), $3) - interval '1 second'
		FROM composite
		WHERE span IS NOT NULL
		ORDER BY 2, 1 NULLS FIRST
	`, pq.Array(monitorIDs), window.Start.UTC(), window.End.UTC(), tenantID, opts.DegradedCountsAsDown)
	if err != nil {
		return nil, fmt.Errorf("list outages: %w", err)
	}
	defer rows.Close()
	var out []Outage
	for rows.Next() {
		var monitorID uuid.NullUUID
		var o Outage
		var open bool
		if err := rows.Scan(&monitorID, &o.Start, &o.End, &o.PlannedSeconds, &open); err != nil {
			return nil, fmt.Errorf("scan outage: %w", err)
		}
		if monitorID.Valid {
			id := monitorID.UUID
			o.MonitorID = &id
		}
		o.Start, o.End = o.Start.UTC(), o.End.UTC()
		o.StartedBefore = !o.Start.After(window.Start)
		o.Ongoing = open || !o.End.Before(window.End)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outages: %w", err)
	}
	return out, nil
}
