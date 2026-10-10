package monitorstate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Evidence freshness (S-F1, docs/state-semantics.md): a stream's evidence is
// fresh for interval × StaleMultiplier (floor MinFreshnessSeconds) after its
// platform receipt time, then stale. The multiplier matches the mesh
// staleness precedent (staleAfterIntervals = 3).
const (
	StaleMultiplier     = 3
	MinFreshnessSeconds = 90
)

// FreshnessHorizonSeconds returns the freshness window for a check interval.
func FreshnessHorizonSeconds(intervalSeconds int) int {
	h := intervalSeconds * StaleMultiplier
	if h < MinFreshnessSeconds {
		return MinFreshnessSeconds
	}
	return h
}

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// LoadFreshLocationCounts assembles the spatial-rule input for one monitor:
// per-location states over the currently selected, live locations, counting
// only locations with FRESH evidence (S-A10) — a silent location's last
// state must not keep voting. Selected still counts every live selected
// location whether or not it is fresh, so quorum clamping is unaffected.
// Also returns the max consecutive failures across fresh locations.
//
// This is the single source of the aggregation input, shared by the results
// ingest (on every location result) and the absence watchdog (when results
// stop arriving) — the two must never disagree.
func LoadFreshLocationCounts(ctx context.Context, q queryer, monitorID uuid.UUID, freshnessSeconds int) (LocationCounts, int, error) {
	var counts LocationCounts
	var maxFailures int
	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE mls.current_state = 'down'),
			COUNT(*) FILTER (WHERE mls.current_state = 'suspect'),
			COUNT(*) FILTER (WHERE mls.current_state = 'up'),
			COUNT(ml.location_id),
			COALESCE(MAX(mls.consecutive_failures), 0)
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id
			AND l.deleted_at IS NULL
			AND l.enabled = TRUE
		LEFT JOIN monitor_location_state mls
			ON mls.monitor_id = ml.monitor_id AND mls.location_id = ml.location_id
			AND mls.last_check_at >= NOW() - make_interval(secs => $2)
		WHERE ml.monitor_id = $1
	`, monitorID, freshnessSeconds).Scan(&counts.Down, &counts.Suspect, &counts.Up, &counts.Selected, &maxFailures)
	if err != nil {
		return LocationCounts{}, 0, fmt.Errorf("aggregate location states: %w", err)
	}
	return counts, maxFailures, nil
}
