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
// sequential scan. The two-step read is safe under concurrency: the rollup
// region derived from a cursor ends at date_trunc('hour', LastCreatedAt) and
// the rollup job only mutates buckets at or after that hour, so a cursor that
// advances between the two queries cannot cause double counting.
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
