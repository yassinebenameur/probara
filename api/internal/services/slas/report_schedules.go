package slas

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

// ReportIssueGrace is how long after a period closes a report schedule
// waits before issuing and sending it, so late check results (queue lag, a
// confirmation recheck) land in the frozen report.
const ReportIssueGrace = time.Hour

// ReportRunAt is when a report schedule of an SLA with this period and
// timezone next sends: the running period's close (S-U7, in the SLA's
// timezone) plus ReportIssueGrace. It is the one rule for a schedule's
// next_run_at, used by reportschedules on create, re-enable and after each
// run, and here when the SLA's period or timezone changes.
func ReportRunAt(period, timezone string, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("SLA timezone: %w", err)
	}
	p, err := sharedanalytics.PeriodContaining(sharedanalytics.PeriodKind(period), loc, now)
	if err != nil {
		return time.Time{}, err
	}
	return p.End.Add(ReportIssueGrace).UTC(), nil
}

// replanReportSchedulesTx points the SLA's enabled report schedules at the
// running period's close under the SLA's new period or timezone. Without
// it a schedule keeps the old definition's next_run_at: shortening the
// period (quarterly to monthly) would skip every period that closes before
// it, and lengthening it would send a stale period when it comes due.
// Disabled schedules are re-planned when they are enabled again.
func replanReportSchedulesTx(ctx context.Context, q db.Executer, tenantID, slaID uuid.UUID, period, timezone string, now time.Time) error {
	next, err := ReportRunAt(period, timezone, now)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE sla_report_schedules SET next_run_at = $3
		WHERE tenant_id = $1 AND sla_id = $2 AND enabled
	`, tenantID, slaID, next); err != nil {
		return fmt.Errorf("re-plan report schedules: %w", err)
	}
	return nil
}
