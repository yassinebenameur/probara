package monitorstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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

// LocationStreaks are the monitor-level streak counters of a multi-location
// monitor, each the max over its fresh locations: MaxFailures drives display
// and the suspect fast recheck (S-A9), MaxSuccesses the furthest-along
// unconfirmed recovery (S-T7) for display and the recovery fast recheck. They
// never feed the spatial rule itself.
type LocationStreaks struct {
	MaxFailures  int
	MaxSuccesses int
}

// LoadFreshLocationCounts assembles the spatial-rule input for one monitor:
// per-location states over the currently selected, live locations, counting
// only locations with FRESH evidence (S-A10) — a silent location's last
// state must not keep voting. Selected still counts every live selected
// location whether or not it is fresh, so quorum clamping is unaffected.
// Also returns the streak counters across fresh locations.
//
// This is the single source of the aggregation input, shared by the results
// ingest (on every location result) and the absence watchdog (when results
// stop arriving) — the two must never disagree.
func LoadFreshLocationCounts(ctx context.Context, q queryer, monitorID uuid.UUID, freshnessSeconds int) (LocationCounts, LocationStreaks, error) {
	var counts LocationCounts
	var streaks LocationStreaks
	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE mls.current_state = 'down'),
			COUNT(*) FILTER (WHERE mls.current_state = 'suspect'),
			COUNT(*) FILTER (WHERE mls.current_state = 'up'),
			COUNT(ml.location_id),
			COALESCE(MAX(mls.consecutive_failures), 0),
			COALESCE(MAX(mls.consecutive_successes), 0)
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id
			AND l.deleted_at IS NULL
			AND l.enabled = TRUE
		LEFT JOIN monitor_location_state mls
			ON mls.monitor_id = ml.monitor_id AND mls.location_id = ml.location_id
			AND mls.last_check_at >= NOW() - make_interval(secs => $2)
		WHERE ml.monitor_id = $1
	`, monitorID, freshnessSeconds).Scan(&counts.Down, &counts.Suspect, &counts.Up, &counts.Selected,
		&streaks.MaxFailures, &streaks.MaxSuccesses)
	if err != nil {
		return LocationCounts{}, LocationStreaks{}, fmt.Errorf("aggregate location states: %w", err)
	}
	return counts, streaks, nil
}

// Location liveness (S-F6, docs/state-semantics.md). Location workers
// heartbeat every ~15s into locations.last_seen_at (platform receipt time,
// the scheduler's heartbeat consumer). Two windows read it:
//
//   - connected (UI badge): seen within LocationConnectedWindow, which
//     tolerates a few missed beats without flapping;
//   - offline (the location_offline alert): enabled, seen at least once,
//     and silent for the tenant's location_offline_after_seconds (default
//     DefaultLocationOfflineAfterSeconds, floor MinLocationOfflineAfterSeconds).
//
// A never-seen location is neither connected nor offline: it was never
// deployed, so there is nothing to have lost. Offline is a fleet signal,
// never a monitor-state input — monitors keep following S-A10 and S-F2.
//
// The offline alert only opens for a location seen within
// LocationOfflineAlertHorizon: a location silent for longer (for example
// one abandoned before the alert existed) is not news, so an upgrade does
// not page for it.
const (
	LocationConnectedWindow            = time.Minute
	DefaultLocationOfflineAfterSeconds = 180
	MinLocationOfflineAfterSeconds     = 60
	LocationOfflineAlertHorizon        = 7 * 24 * time.Hour
)

// LocationConnected reports whether a heartbeat at lastSeenAt still shows the
// location as connected at now.
func LocationConnected(lastSeenAt *time.Time, now time.Time) bool {
	return lastSeenAt != nil && now.Sub(*lastSeenAt) < LocationConnectedWindow
}

// LocationOffline is the Go twin of LocationOfflinePredicate: an enabled,
// live location, seen at least once, silent for at least offlineAfterSeconds
// (clamped to the floor) at now.
func LocationOffline(enabled, deleted bool, lastSeenAt *time.Time, offlineAfterSeconds int, now time.Time) bool {
	if !enabled || deleted || lastSeenAt == nil {
		return false
	}
	if offlineAfterSeconds < MinLocationOfflineAfterSeconds {
		offlineAfterSeconds = MinLocationOfflineAfterSeconds
	}
	return now.Sub(*lastSeenAt) >= time.Duration(offlineAfterSeconds)*time.Second
}

// LocationOfflinePredicate returns a SQL boolean that is TRUE when the
// location aliased by locAlias is offline under the threshold of the tenant
// aliased by tenantAlias (S-F6). It is the alerter's open/resolve rule for
// location_offline alerts and the API's `offline` flag; LocationOffline is
// its Go twin (TestLocationOfflinePredicateMatchesGo pins the pair).
func LocationOfflinePredicate(locAlias, tenantAlias string) string {
	return fmt.Sprintf(`(%[1]s.enabled AND %[1]s.deleted_at IS NULL
		AND %[1]s.last_seen_at IS NOT NULL
		AND %[1]s.last_seen_at <= NOW() - make_interval(secs => GREATEST(%[2]s.location_offline_after_seconds, %[3]d)))`,
		locAlias, tenantAlias, MinLocationOfflineAfterSeconds)
}

// LocationDisconnectedPredicate returns a SQL boolean that is TRUE when the
// location aliased by locAlias was seen before but has missed
// LocationConnectedWindow: the SQL twin of "seen, and not LocationConnected".
// It turns true long before LocationOfflinePredicate, which is why mesh
// dispatch holds on it (S-F6): edges into a dead site fail after a few
// probes, before the site counts as offline.
func LocationDisconnectedPredicate(locAlias string) string {
	return fmt.Sprintf(`(%[1]s.enabled AND %[1]s.deleted_at IS NULL
		AND %[1]s.last_seen_at IS NOT NULL
		AND %[1]s.last_seen_at <= NOW() - make_interval(secs => %[2]d))`,
		locAlias, int(LocationConnectedWindow/time.Second))
}

// LocationWithinOfflineHorizonPredicate returns a SQL boolean that is TRUE
// when the location aliased by locAlias was seen within
// LocationOfflineAlertHorizon, the precondition for opening its offline
// alert.
func LocationWithinOfflineHorizonPredicate(locAlias string) string {
	return fmt.Sprintf(`(%[1]s.last_seen_at >= NOW() - make_interval(secs => %[2]d))`,
		locAlias, int(LocationOfflineAlertHorizon/time.Second))
}
