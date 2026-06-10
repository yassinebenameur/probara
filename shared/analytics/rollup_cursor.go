package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
)

// RollupCursor is the high-water mark of the monitor_daily_rollups job: every
// check_result at or before (LastCreatedAt, LastCheckResultID) has been folded
// into monitor_hourly_rollups / monitor_daily_rollups. A nil LastCreatedAt
// means the job has never completed a run.
//
// Callers read the cursor with this one-row lookup FIRST and then pass the
// bounds they derive from it as plain query parameters. Joining
// rollup_job_state inside the analytics query (via a bounds CTE + CROSS JOIN)
// prevents Postgres from pushing the derived bounds into index conditions on
// check_results.created_at, degrading every such CTE branch to a full
// sequential scan.
//
// Concurrency: the cursor may advance between this read and the main query,
// and the consequences differ by caller shape.
//
//   - Callers that cap their rollup reads at the Go-computed RollupEnd
//     (loadExactRolling24hSummary, batchUptimeSummary) cannot double count:
//     their rollup region ends at date_trunc('hour', LastCreatedAt) and the
//     rollup job only mutates buckets at or after that hour, so buckets below
//     RollupEnd are immutable once the cursor is read.
//
//   - Tuple-comparison callers (loadHourlyBucketSeries24h, batchHourlyUptime,
//     GetGlobalHourlyUptime) read ALL in-window rollup buckets — including the
//     partial bucket for the cursor's current hour — and exclude raw rows via
//     the stale cursor tuple (created_at, id) > (LastCreatedAt, LastCheckResultID).
//     The scheduler commits rollup upserts and the cursor advance atomically
//     per batch, so if a batch commits between the two queries, raw rows in
//     (oldCursor, newCursor] are counted twice: once in the freshly-upserted
//     rollup buckets and once in the raw branch. The over-count is transient
//     and bounded — at most one scheduler batch (<=5000 rows) — and
//     self-corrects on the next request, which sees the new cursor. This is an
//     accepted trade-off for display-only data; wrapping both queries in one
//     REPEATABLE READ transaction would eliminate it but isn't worth the
//     complexity.
type RollupCursor struct {
	LastCreatedAt     *time.Time
	LastCheckResultID uuid.UUID // uuid.Nil when unset; only meaningful alongside LastCreatedAt
}

// LoadRollupCursor reads the monitor_daily_rollups cursor (one-row PK lookup).
// A missing row yields the zero RollupCursor ("no rollups yet").
func LoadRollupCursor(ctx context.Context, q db.Querier) (RollupCursor, error) {
	var cur RollupCursor
	var lastCreatedAt sql.NullTime
	var lastID uuid.NullUUID
	err := q.QueryRowContext(ctx, `
		SELECT last_created_at, last_check_result_id
		FROM rollup_job_state
		WHERE job_name = 'monitor_daily_rollups'
	`).Scan(&lastCreatedAt, &lastID)
	if err != nil {
		if err == sql.ErrNoRows {
			return cur, nil
		}
		return cur, fmt.Errorf("failed to load rollup cursor: %w", err)
	}
	if lastCreatedAt.Valid {
		ts := lastCreatedAt.Time.UTC()
		cur.LastCreatedAt = &ts
	}
	if lastID.Valid {
		cur.LastCheckResultID = lastID.UUID
	}
	return cur, nil
}

// RollupEnd is the exclusive end of the hourly-rollup region for a window whose
// leading partial hour ends at leadingEdgeEnd and whose trailing partial hour
// starts at trailingEdgeStart. SQL equivalent:
//
//	LEAST(trailing_edge_start, COALESCE(date_trunc('hour', last_created_at), leading_edge_end))
//
// When the cursor is unset (or lags behind the window) this collapses to
// leadingEdgeEnd, meaning the rollup contributes nothing and the whole window
// comes from raw check_results.
func (c RollupCursor) RollupEnd(leadingEdgeEnd, trailingEdgeStart time.Time) time.Time {
	end := leadingEdgeEnd
	if c.LastCreatedAt != nil {
		end = c.LastCreatedAt.UTC().Truncate(time.Hour)
	}
	if trailingEdgeStart.Before(end) {
		end = trailingEdgeStart
	}
	return end
}

// RawTailStart returns where the trailing raw scan begins: the rollup region's
// end, clamped to no earlier than leadingEdgeEnd so the leading raw range
// [w_start, leadingEdgeEnd) and the trailing raw range stay disjoint even when
// the cursor lags behind the window. SQL equivalent:
//
//	GREATEST(rollup_end, leading_edge_end)
func (c RollupCursor) RawTailStart(leadingEdgeEnd, trailingEdgeStart time.Time) time.Time {
	start := c.RollupEnd(leadingEdgeEnd, trailingEdgeStart)
	if start.Before(leadingEdgeEnd) {
		start = leadingEdgeEnd
	}
	return start
}

// RawStart is the inclusive start of the raw past-cursor region for an
// hour-aligned window starting at startHour. SQL equivalent:
//
//	COALESCE(GREATEST(start_hour, last_created_at), start_hour)
func (c RollupCursor) RawStart(startHour time.Time) time.Time {
	if c.LastCreatedAt != nil && c.LastCreatedAt.After(startHour) {
		return c.LastCreatedAt.UTC()
	}
	return startHour
}
