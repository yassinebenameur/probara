package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/monitorstate"
)

type queryExecer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// pullRecoveringRecheck brings a recovering monitor's next check forward to
// the fast recheck (S-T7): the run after a pass was scheduled at dispatch,
// before the pass made the monitor recovering, and would otherwise cost a
// whole interval of downtime per pass. The scheduler keeps the same cadence
// for the runs after that (nextCheckDelay). monitorstate.FastRecheckExempt
// (SIP test calls, domain monitors, passive types) keeps its interval.
func pullRecoveringRecheck(ctx context.Context, q queryExecer, monitorID uuid.UUID) error {
	var monitorType string
	var config json.RawMessage
	var recovering bool
	if err := q.QueryRowContext(ctx, `
		SELECT m.type, m.config, `+monitorstate.RecoveringPredicate("m")+`
		FROM monitors m WHERE m.id = $1
	`, monitorID).Scan(&monitorType, &config, &recovering); err != nil {
		return fmt.Errorf("read recovering monitor: %w", err)
	}
	if !recovering || monitorstate.FastRecheckExempt(monitorType, config) {
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE monitors
		SET next_run_at = LEAST(COALESCE(next_run_at, NOW()),
			NOW() + make_interval(secs => LEAST(interval_seconds, $2)))
		WHERE id = $1
	`, monitorID, int(monitorstate.FastRecheckInterval.Seconds())); err != nil {
		return fmt.Errorf("pull recovering recheck: %w", err)
	}
	return nil
}
