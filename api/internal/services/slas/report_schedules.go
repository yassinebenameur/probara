package slas

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

// The pieces of the SLA service that emailed report schedules
// (api/internal/services/reportschedules) build on.

// ReportIssueGrace is how long after a period closes a report schedule
// waits before issuing and sending it, so late check results (queue lag, a
// confirmation recheck) land in the frozen report.
const ReportIssueGrace = time.Hour

// ReportRunAt is when a report schedule of an SLA with this period and
// timezone next sends: the running period's close (S-U7, in the SLA's
// timezone) plus ReportIssueGrace. It is the one rule for a schedule's
// next_run_at: reportschedules uses it on create, re-enable and after each
// run, and Update when the SLA's period or timezone changes.
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

// LastClosedPeriod is the most recent period of the SLA that ended at or
// before now.
func LastClosedPeriod(sla *models.SLA, now time.Time) (sharedanalytics.Period, error) {
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return sharedanalytics.Period{}, fmt.Errorf("SLA timezone: %w", err)
	}
	cur, err := sharedanalytics.PeriodContaining(sharedanalytics.PeriodKind(sla.Period), loc, now)
	if err != nil {
		return sharedanalytics.Period{}, err
	}
	return cur.Previous(loc), nil
}

// replanReportSchedulesTx points the SLA's enabled report schedule at the
// running period's close under the SLA's new period or timezone. Without
// it the schedule keeps the old definition's next_run_at: shortening the
// period (quarterly to monthly) would skip every period that closes before
// it, and lengthening it would send a stale period when it comes due. A
// disabled schedule is re-planned when it is enabled again.
func replanReportSchedulesTx(ctx context.Context, q db.Executer, tenantID, slaID uuid.UUID, period, timezone string, now time.Time) error {
	next, err := ReportRunAt(period, timezone, now)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE sla_report_schedules SET next_run_at = $3, updated_at = NOW()
		WHERE tenant_id = $1 AND sla_id = $2 AND enabled
	`, tenantID, slaID, next); err != nil {
		return fmt.Errorf("re-plan report schedule: %w", err)
	}
	return nil
}

// IssuedReportForPeriod returns the SLA's issued report of a period, or
// ErrNotFound.
func (s *Service) IssuedReportForPeriod(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string) (*models.SLAReport, error) {
	var data []byte
	var issue models.SLAReportIssue
	err := s.db.QueryRowContext(ctx, `
		SELECT r.data, r.id, r.issued_at, `+issuedByExpr+`
		FROM sla_reports r
		LEFT JOIN admin_users u ON u.id = r.issued_by_admin_id
		WHERE r.tenant_id = $1 AND r.sla_id = $2 AND r.period_key = $3
	`, tenantID, slaID, periodKey).Scan(&data, &issue.ID, &issue.IssuedAt, &issue.IssuedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get SLA report: %w", err)
	}
	var report models.SLAReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode SLA report: %w", err)
	}
	report.Issued = &issue
	return &report, nil
}
