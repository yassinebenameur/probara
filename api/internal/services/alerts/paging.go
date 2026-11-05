package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Snooze bounds. A snooze mutes paging without touching maintenance windows
// or SLA accounting, so it is capped: an alert nobody looks at for a week
// should page again rather than stay silent forever.
const (
	MinSnooze = time.Minute
	MaxSnooze = 7 * 24 * time.Hour
)

// Errors of the paging actions. The handlers map ErrInvalidSnooze to 400 and
// the rest to 404, matching acknowledge/resolve ("not found or wrong state").
var (
	ErrNotAcknowledged = errors.New("alert not found or not acknowledged")
	ErrNotOpen         = errors.New("alert not found or not open")
	ErrNotSnoozed      = errors.New("alert not found or not snoozed")
	ErrInvalidSnooze   = fmt.Errorf("snooze must last between %s and %s", MinSnooze, MaxSnooze)
)

// BulkReasonNotAcknowledged: unacknowledge on an alert that is not
// acknowledged (active).
const BulkReasonNotAcknowledged = "not_acknowledged"

// nullableVia stores an empty Via as NULL.
func nullableVia(actor Actor) *string {
	if actor.Via == "" {
		return nil
	}
	return &actor.Via
}

// unackSQL moves acknowledged alerts in target back to active. It stamps
// unacknowledged_at, from which the alerter counts unfired escalation tiers
// and reminders again on the routes the ack held (alertrouting.ResumedAt),
// and clears the acknowledging actor. The one-shot acknowledged_sent_at goes too, so a
// re-acknowledge reaches paging channels again. Providers that were told
// about the first ack (PagerDuty, Opsgenie) are not un-acknowledged: their
// Events APIs have no such action.
const unackSQL = `
	, changed AS (
		UPDATE alerts a
		SET status = 'active', unacknowledged_at = $4, updated_at = $4,
			acknowledged_at = NULL, acknowledged_by_user_id = NULL, acknowledged_via = NULL
		FROM target
		WHERE a.id = target.id
		RETURNING a.id
	), reset AS (
		UPDATE alert_notification_states ans
		SET acknowledged_sent_at = NULL, updated_at = $4
		FROM changed
		WHERE ans.alert_id = changed.id AND ans.acknowledged_sent_at IS NOT NULL
	)
	SELECT id FROM changed`

// UnacknowledgeAlert returns an acknowledged alert to active (see unackSQL).
// actor is accepted for symmetry with acknowledge; the audit log records it.
func (s *Service) UnacknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor Actor) (*models.AlertWithDetails, error) {
	_ = actor
	changed, err := collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+unackSQL,
		pq.Array([]string{alertID.String()}), tenantID, pq.Array([]string{"acknowledged"}), time.Now()))
	if err != nil {
		return nil, fmt.Errorf("failed to unacknowledge alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrNotAcknowledged
	}
	return s.getAlert(ctx, tenantID, alertID)
}

// BulkUnacknowledge returns every listed acknowledged alert to active; the
// others are reported in Failed (not found, not acknowledged, already
// resolved).
func (s *Service) BulkUnacknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID, actor Actor) (*BulkResult, error) {
	_ = actor
	ids := dedupeIDs(alertIDs)
	changed, err := collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+unackSQL,
		pq.Array(uuidStrings(ids)), tenantID, pq.Array([]string{"acknowledged"}), time.Now()))
	if err != nil {
		return nil, fmt.Errorf("failed to bulk unacknowledge alerts: %w", err)
	}
	result, err := s.bulkResult(ctx, tenantID, ids, changed)
	if err != nil {
		return nil, err
	}
	// bulkResult explains failures in acknowledge terms; for unack an
	// active alert is the "wrong state" case.
	if len(result.Failed) > 0 {
		statuses, err := s.alertStatuses(ctx, tenantID, result.Failed)
		if err != nil {
			return nil, err
		}
		for i, f := range result.Failed {
			if statuses[f.ID] == models.AlertStatusActive {
				result.Failed[i].Reason = BulkReasonNotAcknowledged
			}
		}
	}
	return result, nil
}

func (s *Service) alertStatuses(ctx context.Context, tenantID uuid.UUID, failed []BulkFailure) (map[uuid.UUID]models.AlertStatus, error) {
	ids := make([]string, len(failed))
	for i, f := range failed {
		ids[i] = f.ID.String()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.status FROM alerts a
		LEFT JOIN monitors m ON m.id = a.monitor_id
		WHERE a.id = ANY($1::uuid[]) AND a.tenant_id = $2 AND `+alertVisibleClause,
		pq.Array(ids), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to read alert statuses: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]models.AlertStatus{}
	for rows.Next() {
		var id uuid.UUID
		var status models.AlertStatus
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

// SnoozeAlert mutes an open alert's escalation tiers, reminders and
// suspected-cause follow-ups until until (MinSnooze..MaxSnooze from now).
// It replaces any running snooze, is independent of acknowledgement, and
// never writes a maintenance window, so SLA planned downtime is untouched.
// Resolve ends it.
func (s *Service) SnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, until time.Time, actor Actor) (*models.AlertWithDetails, error) {
	now := time.Now()
	if d := until.Sub(now); d < MinSnooze || d > MaxSnooze {
		return nil, ErrInvalidSnooze
	}
	changed, err := collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+`
		UPDATE alerts a
		SET snoozed_until = $4, snoozed_by_user_id = $5, updated_at = $6
		FROM target
		WHERE a.id = target.id
		RETURNING a.id
	`, pq.Array([]string{alertID.String()}), tenantID, pq.Array([]string{"active", "acknowledged"}), until, actor.UserID, now))
	if err != nil {
		return nil, fmt.Errorf("failed to snooze alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrNotOpen
	}
	return s.getAlert(ctx, tenantID, alertID)
}

// UnsnoozeAlert ends a running snooze now. snoozed_until is set to the
// current time rather than cleared: the alerter counts unfired tiers and
// reminders from the end of a snooze, exactly as from an unacknowledge.
func (s *Service) UnsnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor Actor) (*models.AlertWithDetails, error) {
	_ = actor
	// bulkTargetCTE scopes it like every other paging action: this tenant,
	// open, and not on a deleted monitor. The database clock ends the
	// snooze, the same clock alertrouting.SnoozedPredicate reads.
	changed, err := collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+`
		UPDATE alerts a
		SET snoozed_until = NOW(), updated_at = NOW()
		FROM target
		WHERE a.id = target.id AND a.snoozed_until > NOW()
		RETURNING a.id
	`, pq.Array([]string{alertID.String()}), tenantID, pq.Array([]string{"active", "acknowledged"})))
	if err != nil {
		return nil, fmt.Errorf("failed to unsnooze alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrNotSnoozed
	}
	return s.getAlert(ctx, tenantID, alertID)
}
