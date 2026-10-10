package scheduler

// The absence watchdog (docs/state-semantics.md S-F2/S-F4): the single
// component that turns missing evidence into explicit state. Scheduler-hosted
// and advisory-locked so exactly one instance acts fleet-wide — it replaces
// the per-API-process push/agent stale workers, whose in-process debounce
// duplicated synthetic failures across API replicas.
//
//   - Active checks: no fresh evidence for interval × 3 (floor 90s, S-F1) ⇒
//     the monitor transitions to 'unknown' (reason watchdog_stale). No
//     synthetic result row is written — absence is not evidence, and unknown
//     never opens an outage (S-F5).
//   - Multi-location: stale locations stop voting (S-A10, enforced by the
//     shared fresh-counts query); the watchdog re-aggregates monitors whose
//     locations went stale, because no result will arrive to trigger it.
//   - Push: a missed ping IS evidence of failure — that is the point of
//     heartbeat monitoring. No ping within interval + grace_period_seconds ⇒
//     a synthetic failure result through the normal machine.
//   - Agent: like push, with the default freshness window.
//
// Push/agent synthetic failures self-debounce: the synthetic result advances
// monitors.last_result_at, so the next one fires a full window later and
// consecutive failures accumulate toward down as long as the silence lasts.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

const (
	watchdogTickerInterval = 15 * time.Second
	watchdogAdvisoryLock   = 901337403
	watchdogBatchLimit     = 500
	watchdogRunTimeout     = 2 * time.Minute
)

// statusPublisher is the subset of statusupdates.Publisher the watchdog needs.
type statusPublisher interface {
	Publish(event statusupdates.Event) error
}

// SetStatusPublisher wires the NATS status-update publisher used to notify
// the API SSE stream and status pages about watchdog state changes.
func (s *Scheduler) SetStatusPublisher(p statusPublisher) {
	s.status = p
}

func (s *Scheduler) triggerWatchdog() {
	s.watchdogMu.Lock()
	if s.watchdogRunning {
		s.watchdogMu.Unlock()
		return
	}
	s.watchdogRunning = true
	s.watchdogMu.Unlock()

	go func() {
		defer func() {
			s.watchdogMu.Lock()
			s.watchdogRunning = false
			s.watchdogMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.ctx, watchdogRunTimeout)
		defer cancel()
		if err := s.runWatchdog(ctx); err != nil && ctx.Err() == nil {
			s.logger.WithError(err).Error("Watchdog run failed")
		}
	}()
}

func (s *Scheduler) runWatchdog(ctx context.Context) error {
	// Advisory locks are session-scoped: acquisition and release must ride
	// the SAME pooled connection, or the unlock lands on a different session
	// and the lock leaks until the original connection is recycled.
	lockConn, err := s.db.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer lockConn.Close()
	var locked bool
	if err := lockConn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", watchdogAdvisoryLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		if _, err := lockConn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", watchdogAdvisoryLock); err != nil {
			s.logger.WithError(err).Warn("Failed to release watchdog advisory lock")
		}
	}()

	if err := s.watchdogActivePass(ctx); err != nil {
		s.logger.WithError(err).Error("Watchdog active-check pass failed")
	}
	if err := s.watchdogQuorumPass(ctx); err != nil {
		s.logger.WithError(err).Error("Watchdog quorum pass failed")
	}
	if err := s.watchdogHeartbeatPass(ctx, "push"); err != nil {
		s.logger.WithError(err).Error("Watchdog push pass failed")
	}
	if err := s.watchdogHeartbeatPass(ctx, "agent"); err != nil {
		s.logger.WithError(err).Error("Watchdog agent pass failed")
	}
	return nil
}

// watchdogActivePass turns stale location-less active monitors unknown.
// Keyset pagination on m.id sweeps the WHOLE candidate set per run — a
// LIMIT-only scan with a stable plan would return the same batch every tick
// and starve everything behind it.
func (s *Scheduler) watchdogActivePass(ctx context.Context) error {
	var cursor uuid.UUID
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT m.id FROM monitors m
			WHERE m.enabled = TRUE AND m.deleted_at IS NULL
			  AND m.type NOT IN ('group', 'push', 'agent')
			  AND m.current_state <> 'unknown'
			  AND NOT EXISTS (SELECT 1 FROM monitor_locations ml WHERE ml.monitor_id = m.id)
			  AND COALESCE(m.last_result_at, m.created_at) < NOW() - make_interval(secs => GREATEST(m.interval_seconds * $1, $2))
			  AND m.id > $4
			ORDER BY m.id
			LIMIT $3
		`, monitorstate.StaleMultiplier, monitorstate.MinFreshnessSeconds, watchdogBatchLimit, cursor)
		if err != nil {
			return err
		}
		ids, err := scanUUIDs(rows)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := s.markMonitorUnknown(ctx, id); err != nil {
				s.logger.WithError(err).WithField("monitor_id", id).Error("Watchdog failed to mark monitor unknown")
			}
		}
		if len(ids) < watchdogBatchLimit {
			return nil
		}
		cursor = ids[len(ids)-1]
	}
}

func (s *Scheduler) markMonitorUnknown(ctx context.Context, monitorID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Re-check staleness under the monitor lock (S-O1): a result may have
	// landed between the candidate scan and now.
	var tenantID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		SELECT tenant_id FROM monitors
		WHERE id = $1 AND enabled = TRUE AND deleted_at IS NULL
		  AND current_state <> 'unknown'
		  AND COALESCE(last_result_at, created_at) < NOW() - make_interval(secs => GREATEST(interval_seconds * $2, $3))
		FOR UPDATE
	`, monitorID, monitorstate.StaleMultiplier, monitorstate.MinFreshnessSeconds).Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET current_state = 'unknown', consecutive_failures = 0,
			last_state_change_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, monitorID); err != nil {
		return err
	}
	if err := monitorstate.RecordIntervalTx(ctx, tx, tenantID, monitorID,
		monitorstate.StateUnknown, monitorstate.IntervalReasonWatchdogStale); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishStateChange(monitorID, tenantID)
	s.logger.WithField("monitor_id", monitorID).Warn("Monitor has no fresh evidence; state is now unknown")
	return nil
}

// watchdogQuorumPass re-aggregates multi-location monitors that have stale or
// never-reported locations — their aggregate can change without any result
// arriving.
func (s *Scheduler) watchdogQuorumPass(ctx context.Context) error {
	// LEFT JOIN on live locations: a monitor whose every location is disabled
	// or deleted joins nothing and must still be re-aggregated (to unknown),
	// not skipped — otherwise it freezes at its last state. Keyset-paginated:
	// no-op candidates (never-reported locations) stay in the predicate
	// forever, so a bare LIMIT would starve everything after them.
	var cursor uuid.UUID
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT DISTINCT m.id FROM monitors m
			JOIN monitor_locations ml ON ml.monitor_id = m.id
			LEFT JOIN locations l ON l.id = ml.location_id AND l.deleted_at IS NULL AND l.enabled = TRUE
			LEFT JOIN monitor_location_state mls
				ON mls.monitor_id = m.id AND mls.location_id = ml.location_id AND l.id IS NOT NULL
			WHERE m.enabled = TRUE AND m.deleted_at IS NULL
			  AND m.id > $4
			  AND (l.id IS NULL
			       OR mls.last_check_at IS NULL
			       OR mls.last_check_at < NOW() - make_interval(secs => GREATEST(m.interval_seconds * $1, $2)))
			ORDER BY m.id
			LIMIT $3
		`, monitorstate.StaleMultiplier, monitorstate.MinFreshnessSeconds, watchdogBatchLimit, cursor)
		if err != nil {
			return err
		}
		ids, err := scanUUIDs(rows)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := s.reaggregateMonitor(ctx, id); err != nil {
				s.logger.WithError(err).WithField("monitor_id", id).Error("Watchdog failed to re-aggregate monitor")
			}
		}
		if len(ids) < watchdogBatchLimit {
			return nil
		}
		cursor = ids[len(ids)-1]
	}
}

func (s *Scheduler) reaggregateMonitor(ctx context.Context, monitorID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var tenantID uuid.UUID
	var current string
	var quorum, intervalSeconds int
	err = tx.QueryRowContext(ctx, `
		SELECT tenant_id, current_state, location_quorum, interval_seconds
		FROM monitors
		WHERE id = $1 AND enabled = TRUE AND deleted_at IS NULL
		FOR UPDATE
	`, monitorID).Scan(&tenantID, &current, &quorum, &intervalSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}

	counts, maxFailures, err := monitorstate.LoadFreshLocationCounts(
		ctx, tx, monitorID, monitorstate.FreshnessHorizonSeconds(intervalSeconds))
	if err != nil {
		return err
	}
	newState := monitorstate.AggregateLocations(counts, quorum)
	if string(newState) == current {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET current_state = $1, consecutive_failures = $2,
			last_state_change_at = NOW(), updated_at = NOW()
		WHERE id = $3
	`, string(newState), maxFailures, monitorID); err != nil {
		return err
	}
	if err := monitorstate.RecordIntervalTx(ctx, tx, tenantID, monitorID,
		newState, monitorstate.IntervalReasonWatchdogStale); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishStateChange(monitorID, tenantID)
	s.logger.WithField("monitor_id", monitorID).WithField("state", newState).
		Warn("Location evidence went stale; monitor aggregate re-derived")
	return nil
}

// watchdogHeartbeatPass synthesizes failure evidence for push/agent monitors
// whose expected inbound reports stopped arriving. For push monitors the
// window is interval + grace_period_seconds from the monitor config; agents
// use the default freshness window.
func (s *Scheduler) watchdogHeartbeatPass(ctx context.Context, monitorType string) error {
	// Agents use the default freshness window, computable in SQL so
	// not-yet-due monitors never occupy the batch; push deadlines depend on
	// config JSON (grace), so SQL admits after one interval (a lower bound)
	// and Go applies the exact deadline. Keyset pagination sweeps past
	// not-yet-due candidates instead of letting them starve the batch.
	staleMultiplier, minFreshness := 1, 0
	if monitorType == "agent" {
		staleMultiplier, minFreshness = monitorstate.StaleMultiplier, monitorstate.MinFreshnessSeconds
	}

	type candidate struct {
		id       uuid.UUID
		tenantID uuid.UUID
		lastSeen time.Time
		deadline time.Time
	}
	var cands []candidate
	var cursor uuid.UUID
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT m.id, m.tenant_id, m.interval_seconds, m.config,
			       COALESCE(m.last_result_at, m.created_at)
			FROM monitors m
			WHERE m.type = $1 AND m.enabled = TRUE AND m.deleted_at IS NULL
			  AND m.id > $4
			  AND COALESCE(m.last_result_at, m.created_at) < NOW() - make_interval(secs => GREATEST(m.interval_seconds * $2, $3))
			ORDER BY m.id
			LIMIT $5
		`, monitorType, staleMultiplier, minFreshness, cursor, watchdogBatchLimit)
		if err != nil {
			return err
		}
		batchLen := 0
		for rows.Next() {
			var c candidate
			var intervalSeconds int
			var configJSON []byte
			if err := rows.Scan(&c.id, &c.tenantID, &intervalSeconds, &configJSON, &c.lastSeen); err != nil {
				rows.Close()
				return err
			}
			batchLen++
			cursor = c.id
			windowSeconds := monitorstate.FreshnessHorizonSeconds(intervalSeconds)
			if monitorType == "push" {
				// A pointer distinguishes an explicit grace of 0 (stale
				// right after the interval) from an absent/malformed one
				// (fall back to a full extra interval).
				var cfg struct {
					GracePeriodSeconds *int `json:"grace_period_seconds"`
				}
				grace := intervalSeconds
				if err := json.Unmarshal(configJSON, &cfg); err == nil && cfg.GracePeriodSeconds != nil && *cfg.GracePeriodSeconds >= 0 {
					grace = *cfg.GracePeriodSeconds
				}
				windowSeconds = intervalSeconds + grace
			}
			c.deadline = c.lastSeen.Add(time.Duration(windowSeconds) * time.Second)
			cands = append(cands, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if batchLen < watchdogBatchLimit {
			break
		}
	}
	now := time.Now()

	message := "No push received within expected interval + grace period"
	if monitorType == "agent" {
		message = "Agent has not reported metrics within the freshness window"
	}
	for _, c := range cands {
		if now.Before(c.deadline) {
			continue
		}
		transition, err := monitorstate.Record(ctx, s.db.DB, monitorstate.Result{
			MonitorID:        c.id,
			TenantID:         c.tenantID,
			JobID:            uuid.New(),
			Status:           string(sharedmodels.ResultStatusFailure),
			ResultSource:     string(sharedmodels.ResultSourceMonitor),
			ErrorMessage:     &message,
			StartedAt:        now,
			CompletedAt:      now,
			SyntheticAbsence: true,
		})
		if err != nil {
			s.logger.WithError(err).WithField("monitor_id", c.id).Error("Watchdog failed to record heartbeat staleness")
			continue
		}
		if transition.Changed {
			s.publishStateChange(c.id, c.tenantID)
		}
		s.logger.WithField("monitor_id", c.id).WithField("type", monitorType).
			Warn("Heartbeat monitor is silent; synthetic failure recorded")
	}
	return nil
}

func (s *Scheduler) publishStateChange(monitorID, tenantID uuid.UUID) {
	if s.status == nil {
		return
	}
	event := statusupdates.Event{
		Type:      "state_change",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	}
	if err := s.status.Publish(event); err != nil {
		s.logger.WithError(err).Warn("Failed to publish watchdog status update")
	}
}

func scanUUIDs(rows *sql.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
