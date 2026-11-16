package scheduler

// Repeating maintenance windows cover monitors through materialized
// occurrences (docs/state-semantics.md S-M5). The API writes them up to
// maintenance.Horizon when a window is saved; this pass keeps every live
// repeating window that far ahead as time moves on.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

const (
	// The horizon is 35 days and the API materializes on every save, so an
	// hourly roll keeps it within an hour of full.
	maintenanceOccurrenceRollInterval = time.Hour
	maintenanceOccurrenceRollTimeout  = 5 * time.Minute
	maintenanceOccurrenceAdvisoryLock = int64(901_337_412)
)

type occurrenceRoller struct {
	mu      sync.Mutex
	running bool
	added   *prometheus.CounterVec
	errors  *prometheus.CounterVec
}

func (s *Scheduler) triggerOccurrenceRoll() {
	r := s.occurrences
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
		}()
		added, err := s.rollMaintenanceOccurrences(s.ctx)
		if added > 0 {
			r.added.With(prometheus.Labels{}).Add(float64(added))
		}
		if err != nil {
			r.errors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).Error("Maintenance occurrence roll failed")
			return
		}
		if added > 0 {
			s.logger.WithField("occurrences_added", added).Info("Maintenance occurrences rolled forward")
		}
	}()
}

// rollMaintenanceOccurrences runs one roll under a session advisory lock
// held on one pinned connection (maintenance_lock.go), so with several
// scheduler replicas one rolls and the others skip the tick. The roll itself
// is idempotent and locks each window row, so even an overlapping run could
// only waste work.
func (s *Scheduler) rollMaintenanceOccurrences(parent context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(parent, maintenanceOccurrenceRollTimeout)
	defer cancel()

	lock, err := acquireMaintenanceLock(ctx, s.db.DB, maintenanceOccurrenceAdvisoryLock)
	if err != nil {
		return 0, fmt.Errorf("acquire maintenance occurrence lock: %w", err)
	}
	if lock == nil {
		return 0, nil
	}
	defer func() {
		if err := lock.release(); err != nil {
			s.logger.WithError(err).Warn("Failed to release maintenance occurrence lock")
		}
	}()

	// The database clock: the one every coverage predicate compares with.
	var now time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return 0, fmt.Errorf("read database clock: %w", err)
	}
	return maintenance.RollForward(ctx, s.db, now)
}
