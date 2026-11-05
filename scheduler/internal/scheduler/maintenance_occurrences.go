package scheduler

// Maintenance horizon roller (docs/state-semantics.md S-M5). Repeating
// maintenance windows are materialized as occurrences up to
// maintenance.Horizon ahead; the API writes them on create/update and this
// pass keeps the horizon rolling forward. It only adds occurrences
// (maintenance.RollForward); it never edits or prunes one, because past
// occurrences are SLA history.

import (
	"context"
	"time"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

const (
	// The horizon is 35 days, so an hourly roll keeps weeks of slack: a
	// scheduler outage shorter than that never leaves a gap.
	maintenanceRollInterval      = time.Hour
	maintenanceRollTimeout       = 2 * time.Minute
	maintenanceRollAdvisoryLock  = int64(901_337_409)
	maintenanceRollStartupJitter = 30 * time.Second
)

// runMaintenanceRoller rolls the horizon at startup and then hourly until
// the scheduler stops. Every replica runs it; the advisory lock makes one
// of them do the work.
func (s *Scheduler) runMaintenanceRoller() {
	timer := time.NewTimer(maintenanceRollStartupJitter)
	defer timer.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.ctx.Done():
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(s.ctx, maintenanceRollTimeout)
			if err := s.rollMaintenanceOccurrences(ctx); err != nil && ctx.Err() == nil {
				s.logger.WithError(err).Error("Maintenance occurrence roll failed")
			}
			cancel()
			timer.Reset(maintenanceRollInterval)
		}
	}
}

// rollMaintenanceOccurrences holds the advisory lock on one pinned
// connection (maintenance_lock.go) and extends every live repeating window
// to the horizon, measured on the database clock.
func (s *Scheduler) rollMaintenanceOccurrences(ctx context.Context) error {
	lock, err := acquireMaintenanceLock(ctx, s.db.DB, maintenanceRollAdvisoryLock)
	if err != nil {
		return err
	}
	if lock == nil {
		return nil
	}
	defer func() {
		if err := lock.release(); err != nil {
			s.logger.WithError(err).Warn("Failed to release maintenance roll advisory lock")
		}
	}()
	var now time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return err
	}
	added, err := maintenance.RollForward(ctx, s.db, now)
	if added > 0 {
		s.logger.WithField("occurrences", added).Info("Rolled maintenance occurrences forward")
	}
	return err
}
