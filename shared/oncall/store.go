package oncall

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
)

// ErrScheduleNotFound is returned by LoadAt for a schedule that is not the
// tenant's (or no longer exists).
var ErrScheduleNotFound = errors.New("on-call schedule not found")

// LoadAt reads tenantID's schedule scheduleID from the database (migration
// 000112) with its rotations, their members, and the overrides covering at:
// enough for OnCallAt(at). The alerter calls it to page "whoever is on
// call" when an escalation step fires.
func LoadAt(ctx context.Context, q db.Querier, tenantID, scheduleID uuid.UUID, at time.Time) (Schedule, error) {
	var tz string
	err := q.QueryRowContext(ctx, `SELECT timezone FROM oncall_schedules WHERE id = $1 AND tenant_id = $2`,
		scheduleID, tenantID).Scan(&tz)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrScheduleNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("load schedule: %w", err)
	}
	s := Schedule{Location: time.UTC}
	if loc, err := time.LoadLocation(tz); err == nil {
		s.Location = loc
	}

	rows, err := q.QueryContext(ctx, `
		SELECT r.id, r.kind, r.handoff_minute, r.handoff_weekday, r.start_at, r.end_at, m.user_id
		FROM oncall_rotations r
		LEFT JOIN oncall_rotation_members m ON m.rotation_id = r.id
		WHERE r.schedule_id = $1
		ORDER BY r.position, m.position`, scheduleID)
	if err != nil {
		return Schedule{}, fmt.Errorf("load rotations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r Rotation
		var kind string
		var weekday sql.NullInt64
		var member uuid.NullUUID
		if err := rows.Scan(&r.ID, &kind, &r.HandoffMinute, &weekday, &r.StartAt, &r.EndAt, &member); err != nil {
			return Schedule{}, fmt.Errorf("scan rotation: %w", err)
		}
		r.Kind = RotationKind(kind)
		if weekday.Valid {
			r.HandoffWeekday = time.Weekday(weekday.Int64)
		}
		if n := len(s.Rotations); n == 0 || s.Rotations[n-1].ID != r.ID {
			s.Rotations = append(s.Rotations, r)
		}
		if member.Valid {
			last := &s.Rotations[len(s.Rotations)-1]
			last.Members = append(last.Members, member.UUID)
		}
	}
	if err := rows.Err(); err != nil {
		return Schedule{}, err
	}

	ovRows, err := q.QueryContext(ctx, `
		SELECT id, user_id, starts_at, ends_at, created_at
		FROM oncall_overrides
		WHERE schedule_id = $1 AND starts_at <= $2 AND ends_at > $2`, scheduleID, at)
	if err != nil {
		return Schedule{}, fmt.Errorf("load overrides: %w", err)
	}
	defer ovRows.Close()
	for ovRows.Next() {
		var o Override
		if err := ovRows.Scan(&o.ID, &o.UserID, &o.Start, &o.End, &o.CreatedAt); err != nil {
			return Schedule{}, fmt.Errorf("scan override: %w", err)
		}
		s.Overrides = append(s.Overrides, o)
	}
	return s, ovRows.Err()
}
