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
)

// The pieces of the SLA service that emailed report schedules
// (api/internal/services/reportschedules) build on.

// ReportIssueGrace is how long after a period closes a report schedule
// waits before issuing and sending it, so late check results (queue lag, a
// confirmation recheck) land in the frozen report.
const ReportIssueGrace = time.Hour

// ReportRunAt is when a report schedule of an SLA with this period and
// timezone next sends: the running period's close (S-U7, in the SLA's
// timezone) plus ReportIssueGrace.
func ReportRunAt(period, timezone string, now time.Time) (time.Time, error) {
	p, err := PeriodOf(period, timezone, now)
	if err != nil {
		return time.Time{}, err
	}
	return p.End.Add(ReportIssueGrace).UTC(), nil
}

// PeriodOf is the period of kind period in timezone that contains t.
func PeriodOf(period, timezone string, t time.Time) (sharedanalytics.Period, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return sharedanalytics.Period{}, fmt.Errorf("SLA timezone: %w", err)
	}
	return sharedanalytics.PeriodContaining(sharedanalytics.PeriodKind(period), loc, t)
}

// replanReportSchedulesTx moves the SLA's enabled report schedule onto its
// new period or timezone. Periods of the new definition that closed before
// now are never sent (sends_after), and the next send is the running
// period's close. One exception keeps a report from being lost: a period of
// the old definition that closed within the grace hour and was not claimed
// yet is carried over and sent once, as it was defined. Without the re-plan
// the schedule would keep the old definition's next_run_at: shortening the
// period would skip every period that closes before it, lengthening it
// would send a stale one. A disabled schedule is re-planned when enabled.
func replanReportSchedulesTx(ctx context.Context, q *sql.Tx, tenantID, slaID uuid.UUID, old, next definition, now time.Time) error {
	var id uuid.UUID
	var lastEnd sql.NullTime
	var sendsAfter time.Time
	var carry, carryKind, carryTZ sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT id, sends_after, last_period_end, carry_period_key, carry_period_kind, carry_timezone FROM sla_report_schedules
		WHERE tenant_id = $1 AND sla_id = $2 AND enabled
		FOR UPDATE
	`, tenantID, slaID).Scan(&id, &sendsAfter, &lastEnd, &carry, &carryKind, &carryTZ)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock report schedule: %w", err)
	}
	nextRun, err := ReportRunAt(next.Period, next.Timezone, now)
	if err != nil {
		return err
	}
	var kind, tz, key any // NULL unless a period is carried over
	if carry.Valid {
		// An earlier change already carries one; keep it.
		kind, tz, key = carryKind.String, carryTZ.String, carry.String
		nextRun = now
	} else if cur, err := PeriodOf(old.Period, old.Timezone, now); err == nil {
		loc, _ := time.LoadLocation(old.Timezone)
		closed := cur.Previous(loc)
		floor := sendsAfter
		if lastEnd.Valid && lastEnd.Time.After(floor) {
			floor = lastEnd.Time
		}
		if now.Before(closed.End.Add(ReportIssueGrace)) && closed.End.After(floor) {
			kind, tz, key = old.Period, old.Timezone, closed.Key
			nextRun = closed.End.Add(ReportIssueGrace).UTC()
		}
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE sla_report_schedules SET next_run_at = $2, sends_after = $3, carry_period_kind = $4, carry_timezone = $5,
			carry_period_key = $6, updated_at = NOW()
		WHERE id = $1
	`, id, nextRun, now, kind, tz, key); err != nil {
		return fmt.Errorf("re-plan report schedule: %w", err)
	}
	return nil
}

// IssueReportUnder freezes one closed period of the SLA as defined by kind
// and timezone, which can be the SLA's previous definition: a report
// schedule sends a period that closed just before the SLA changed as it was
// defined then. The frozen definition records that kind and timezone.
func (s *Service) IssueReportUnder(ctx context.Context, tenantID, slaID uuid.UUID, kind, timezone, periodKey string, by Issuer) (*models.SLAReport, error) {
	sla, err := s.Get(ctx, tenantID, slaID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("SLA timezone: %w", err)
	}
	period, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodKind(kind), loc, periodKey)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	under := *sla
	under.Period, under.Timezone = kind, timezone
	return s.issuePeriod(ctx, tenantID, &under, period, by)
}

// IssuedReportStarting returns the SLA's issued report of the period that
// starts at start, or ErrNotFound. Keys can repeat across timezones, so the
// period is identified by its start (as sla_reports' unique key does).
func (s *Service) IssuedReportStarting(ctx context.Context, tenantID, slaID uuid.UUID, start time.Time) (*models.SLAReport, error) {
	var data []byte
	var issue models.SLAReportIssue
	err := s.db.QueryRowContext(ctx, `
		SELECT r.data, r.id, r.issued_at, `+issuedByExpr+`
		FROM sla_reports r
		LEFT JOIN admin_users u ON u.id = r.issued_by_admin_id
		WHERE r.tenant_id = $1 AND r.sla_id = $2 AND r.period_start = $3
	`, tenantID, slaID, start.UTC()).Scan(&data, &issue.ID, &issue.IssuedAt, &issue.IssuedBy)
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
