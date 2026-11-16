package monitorstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
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

// EvidenceWindowSeconds is the full S-F1 window for one monitor: the default
// horizon, except push monitors, which are fresh for interval +
// grace_period_seconds from their config. The absence watchdog and every
// read-side "is this evidence recent?" report share it, measured from
// monitors.last_result_at (platform receipt), never a worker timestamp.
func EvidenceWindowSeconds(monitorType string, intervalSeconds int, config []byte) int {
	if monitorType != "push" {
		return FreshnessHorizonSeconds(intervalSeconds)
	}
	// A pointer distinguishes an explicit grace of 0 (stale right after the
	// interval) from an absent/malformed one (fall back to a full extra
	// interval).
	var cfg struct {
		GracePeriodSeconds *int `json:"grace_period_seconds"`
	}
	grace := intervalSeconds
	if err := json.Unmarshal(config, &cfg); err == nil && cfg.GracePeriodSeconds != nil && *cfg.GracePeriodSeconds >= 0 {
		grace = *cfg.GracePeriodSeconds
	}
	return intervalSeconds + grace
}

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// FreshLocations is the spatial-rule input for one location-bound monitor.
type FreshLocations struct {
	Counts LocationCounts
	// MaxFailures is the max consecutive failures across fresh locations
	// (S-A9).
	MaxFailures int
	// DownSuccesses holds each fresh down location's consecutive successes
	// toward confirming its recovery (S-T7), the input of RecoveryProgress.
	DownSuccesses []int
}

// LoadFreshLocationCounts assembles the spatial-rule input for one monitor:
// per-location states over the currently selected, live locations, counting
// only locations with FRESH evidence (S-A10) — a silent location's last
// state must not keep voting. Selected still counts every live selected
// location whether or not it is fresh, so quorum clamping is unaffected.
// A location down only because it owes a recovery confirmation since its
// silence or its addition (recovery_pending, no failure since) does not vote
// either: its passes must neither drag a healthy aggregate down nor count it
// up before they confirm (S-T7).
//
// This is the single source of the aggregation input, shared by the results
// ingest (on every location result) and the absence watchdog (when results
// stop arriving) — the two must never disagree.
func LoadFreshLocationCounts(ctx context.Context, q queryer, monitorID uuid.UUID, freshnessSeconds int) (FreshLocations, error) {
	var f FreshLocations
	var downSuccesses pq.Int64Array
	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE mls.current_state = 'down' AND NOT mls.recovery_pending),
			COUNT(*) FILTER (WHERE mls.current_state = 'suspect'),
			COUNT(*) FILTER (WHERE mls.current_state = 'up'),
			COUNT(ml.location_id),
			COALESCE(MAX(mls.consecutive_failures), 0),
			COALESCE(array_agg(mls.consecutive_successes) FILTER (WHERE mls.current_state = 'down' AND NOT mls.recovery_pending), '{}')
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id
			AND l.deleted_at IS NULL
			AND l.enabled = TRUE
		LEFT JOIN monitor_location_state mls
			ON mls.monitor_id = ml.monitor_id AND mls.location_id = ml.location_id
			AND mls.last_check_at >= NOW() - make_interval(secs => $2)
		WHERE ml.monitor_id = $1
	`, monitorID, freshnessSeconds).Scan(&f.Counts.Down, &f.Counts.Suspect, &f.Counts.Up, &f.Counts.Selected,
		&f.MaxFailures, &downSuccesses)
	if err != nil {
		return FreshLocations{}, fmt.Errorf("aggregate location states: %w", err)
	}
	f.DownSuccesses = make([]int, len(downSuccesses))
	for i, n := range downSuccesses {
		f.DownSuccesses[i] = int(n)
	}
	return f, nil
}

// Location liveness (S-F6, docs/state-semantics.md). Location workers
// heartbeat every ~15s into locations.last_seen_at, stamped with the
// platform's receipt time by the scheduler's heartbeat consumer. Two windows
// read it:
//
//   - connected (the UI badge): seen within LocationConnectedWindow, which
//     tolerates a few missed beats; a seen, enabled location past it is
//     disconnected;
//   - offline (the location_offline alert, the API's `offline` flag, the UI's
//     "Offline"): enabled, not deleted, seen at least once, silent for
//     LocationOfflineAfter, and enabled for at least that long.
//
// A never-seen location is neither connected nor offline: no worker was ever
// deployed, so nothing was lost. Offline is a fleet signal and never a
// monitor-state input; monitors keep following S-A10 and S-F2.
const (
	LocationConnectedWindow = time.Minute
	// LocationOfflineAfter is one platform-wide threshold, not a workspace
	// setting: three minutes is about a dozen missed beats, so a restarting
	// worker or a lost core-NATS beat never trips it. It also applies from
	// locations.enabled_at: a location re-enabled before its worker is back
	// gets the same grace as a worker that just went quiet.
	LocationOfflineAfter = 180 * time.Second
	// LocationOfflineAlertHorizon bounds which offline locations may open an
	// alert: one silent for longer (abandoned before the alert existed, or
	// left enabled after its site closed) is not news, so an upgrade does not
	// page for it.
	LocationOfflineAlertHorizon = 7 * 24 * time.Hour
)

// LocationConnected reports whether a heartbeat at lastSeenAt still shows the
// location as connected at now.
func LocationConnected(lastSeenAt *time.Time, now time.Time) bool {
	return lastSeenAt != nil && now.Sub(*lastSeenAt) < LocationConnectedWindow
}

// LocationDisconnected is the Go twin of LocationDisconnectedPredicate.
func LocationDisconnected(enabled, deleted bool, lastSeenAt *time.Time, now time.Time) bool {
	return enabled && !deleted && lastSeenAt != nil && !LocationConnected(lastSeenAt, now)
}

// LocationOffline is the Go twin of LocationOfflinePredicate.
func LocationOffline(enabled, deleted bool, lastSeenAt *time.Time, enabledAt, now time.Time) bool {
	if !enabled || deleted || lastSeenAt == nil {
		return false
	}
	return now.Sub(*lastSeenAt) >= LocationOfflineAfter && now.Sub(enabledAt) >= LocationOfflineAfter
}

// LocationOfflinePredicate returns a SQL boolean that is TRUE when the
// location aliased by locAlias is offline (S-F6). It is the alerter's
// open/resolve rule for location_offline alerts, the alerter's fleet gauge
// and the API's `offline` flag; LocationOffline is its Go twin
// (TestLocationPredicatesMatchGo pins the pair at the boundaries). Never NULL.
func LocationOfflinePredicate(locAlias string) string {
	return fmt.Sprintf(`(%[1]s.enabled AND %[1]s.deleted_at IS NULL
		AND %[1]s.last_seen_at IS NOT NULL
		AND %[1]s.last_seen_at <= NOW() - make_interval(secs => %[2]d)
		AND %[1]s.enabled_at <= NOW() - make_interval(secs => %[2]d))`,
		locAlias, int(LocationOfflineAfter/time.Second))
}

// LocationDisconnectedPredicate returns a SQL boolean that is TRUE when the
// location aliased by locAlias is enabled, not deleted, was seen before and
// has missed LocationConnectedWindow: the SQL form of "seen, and not
// LocationConnected" (Go twin LocationDisconnected). It turns true long
// before LocationOfflinePredicate, which is why the mesh hold starts from it:
// edges into a dead site fail after a few probes, before the site counts as
// offline.
func LocationDisconnectedPredicate(locAlias string) string {
	return fmt.Sprintf(`(%[1]s.enabled AND %[1]s.deleted_at IS NULL
		AND %[1]s.last_seen_at IS NOT NULL
		AND %[1]s.last_seen_at <= NOW() - make_interval(secs => %[2]d))`,
		locAlias, int(LocationConnectedWindow/time.Second))
}

// LocationWithinOfflineHorizonPredicate returns a SQL boolean that is TRUE
// when the location aliased by locAlias was seen within
// LocationOfflineAlertHorizon, the precondition for opening its offline
// alert. FALSE (not NULL) for a never-seen location.
func LocationWithinOfflineHorizonPredicate(locAlias string) string {
	return fmt.Sprintf(`COALESCE(%[1]s.last_seen_at >= NOW() - make_interval(secs => %[2]d), FALSE)`,
		locAlias, int(LocationOfflineAlertHorizon/time.Second))
}
