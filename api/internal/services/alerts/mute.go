package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// Unacknowledge and snooze, the two ways out of and around the
// acknowledge mute (shared/alertrouting mute.go): an acknowledged or snoozed
// alert sends no reminder and no further escalation. Lifting either (unacknowledge,
// unsnooze, or the snooze running out) stamps the moment the alerter counts
// unfired escalation tiers and reminders from.

// Snooze bounds. A snooze never touches maintenance windows or SLA
// accounting, so it is capped: an open alert nobody looks at should start
// notifying again rather than stay silent indefinitely.
const (
	MinSnooze = time.Minute
	MaxSnooze = 7 * 24 * time.Hour
)

// Errors of the mute actions. ErrInvalidSnooze maps to 400, the others to
// 404 like acknowledge/resolve ("not found or in the wrong state").
var (
	ErrAlertNotAcknowledged = errors.New("alert not found or not acknowledged")
	ErrAlertNotOpen         = errors.New("alert not found or already resolved")
	ErrAlertNotSnoozed      = errors.New("alert not found or not snoozed")
	ErrInvalidSnooze        = errors.New("a snooze must end between 1 minute and 7 days from now")
)

// Bulk reasons of the mute actions (BulkFailure.Reason), next to not_found
// and already_resolved.
const (
	// BulkReasonNotAcknowledged: unacknowledge on an alert that is active.
	BulkReasonNotAcknowledged = "not_acknowledged"
	// BulkReasonNotSnoozed: unsnooze on an open alert that is not snoozed.
	BulkReasonNotSnoozed = "not_snoozed"
)

// actingUserID is the signed-in user behind the request, nil for an API key.
func actingUserID(ctx context.Context) *uuid.UUID {
	raw, ok := ctxpkg.GetAdminID(ctx)
	if !ok {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// unackSQL (after bulkTargetCTE over acknowledged alerts) returns them to
// active: unacknowledged_at restarts the tier and reminder clocks, and the
// acknowledgement (time, user) is cleared, since the payload reports the
// current one; first_acknowledged_at stays, for SLA reports. The one-shot acknowledged_sent_at goes too, so a later
// acknowledge reaches paging channels again. A provider already told about
// the first acknowledge (PagerDuty, Opsgenie) is not un-acknowledged: their
// event APIs have no such action. The database clock stamps it, the clock
// the alerter's mute SQL reads.
const unackSQL = `
	, changed AS (
		UPDATE alerts a
		SET status = 'active', unacknowledged_at = NOW(), updated_at = NOW(),
			acknowledged_at = NULL, acknowledged_by_user_id = NULL
		FROM target
		WHERE a.id = target.id
		RETURNING a.id
	), reset AS (
		UPDATE alert_notification_states ans
		SET acknowledged_sent_at = NULL, updated_at = NOW()
		FROM changed
		WHERE ans.alert_id = changed.id AND ans.acknowledged_sent_at IS NOT NULL
	)
	SELECT id FROM changed`

// snoozeSQL (after bulkTargetCTE over open alerts) sets the snooze end $4 and
// the snoozing user $5, replacing a running snooze (whose start it keeps). It
// is independent of acknowledgement.
const snoozeSQL = `
	UPDATE alerts a
	SET snoozed_until = $4, snoozed_by_user_id = $5, updated_at = NOW(),
		snoozed_at = CASE WHEN a.snoozed_until > NOW() THEN a.snoozed_at ELSE NOW() END
	FROM target
	WHERE a.id = target.id
	RETURNING a.id`

// unsnoozeSQL (after bulkTargetCTE over open alerts) ends a running snooze
// now. The end is set to the database's NOW() rather than cleared: the
// alerter counts unfired tiers and reminders from a snooze's end.
const unsnoozeSQL = `
	UPDATE alerts a
	SET snoozed_until = NOW(), updated_at = NOW()
	FROM target
	WHERE a.id = target.id AND a.snoozed_until > NOW()
	RETURNING a.id`

var openStatuses = []string{string(models.AlertStatusActive), string(models.AlertStatusAcknowledged)}

// ValidateSnoozeEnd refuses a snooze end outside MinSnooze..MaxSnooze from now.
func ValidateSnoozeEnd(until time.Time) error {
	if d := time.Until(until); d < MinSnooze || d > MaxSnooze {
		return ErrInvalidSnooze
	}
	return nil
}

// UnacknowledgeAlert returns an acknowledged alert to active (unackSQL).
func (s *Service) UnacknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	changed, err := s.applyMute(ctx, tenantID, []uuid.UUID{alertID}, []string{string(models.AlertStatusAcknowledged)}, unackSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to unacknowledge alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrAlertNotAcknowledged
	}
	return s.getAlert(ctx, tenantID, alertID)
}

// SnoozeAlert mutes an open alert's reminders and further escalation until
// until (ValidateSnoozeEnd), recording the acting user as the snoozer.
func (s *Service) SnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, until time.Time) (*models.AlertWithDetails, error) {
	if err := ValidateSnoozeEnd(until); err != nil {
		return nil, err
	}
	changed, err := s.applyMute(ctx, tenantID, []uuid.UUID{alertID}, openStatuses, snoozeSQL, until, actingUserID(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to snooze alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrAlertNotOpen
	}
	return s.getAlert(ctx, tenantID, alertID)
}

// UnsnoozeAlert ends an open alert's running snooze now (unsnoozeSQL).
func (s *Service) UnsnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	changed, err := s.applyMute(ctx, tenantID, []uuid.UUID{alertID}, openStatuses, unsnoozeSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to unsnooze alert: %w", err)
	}
	if len(changed) == 0 {
		return nil, ErrAlertNotSnoozed
	}
	return s.getAlert(ctx, tenantID, alertID)
}

// BulkUnacknowledge returns every listed acknowledged alert to active; the
// others are reported in Failed (not_found, not_acknowledged,
// already_resolved).
func (s *Service) BulkUnacknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error) {
	ids := dedupeIDs(alertIDs)
	changed, err := s.applyMute(ctx, tenantID, ids, []string{string(models.AlertStatusAcknowledged)}, unackSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk unacknowledge alerts: %w", err)
	}
	return s.bulkResultWith(ctx, tenantID, ids, changed, func(status models.AlertStatus) string {
		if status == models.AlertStatusResolved {
			return BulkReasonAlreadyResolved
		}
		return BulkReasonNotAcknowledged
	})
}

// BulkSnooze snoozes every listed open alert until until; the others are
// reported in Failed (not_found, already_resolved).
func (s *Service) BulkSnooze(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID, until time.Time) (*BulkResult, error) {
	if err := ValidateSnoozeEnd(until); err != nil {
		return nil, err
	}
	ids := dedupeIDs(alertIDs)
	changed, err := s.applyMute(ctx, tenantID, ids, openStatuses, snoozeSQL, until, actingUserID(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to bulk snooze alerts: %w", err)
	}
	return s.bulkResultWith(ctx, tenantID, ids, changed, func(models.AlertStatus) string { return BulkReasonAlreadyResolved })
}

// BulkUnsnooze ends the running snooze of every listed open alert; the others
// are reported in Failed (not_found, not_snoozed, already_resolved).
func (s *Service) BulkUnsnooze(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error) {
	ids := dedupeIDs(alertIDs)
	changed, err := s.applyMute(ctx, tenantID, ids, openStatuses, unsnoozeSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk unsnooze alerts: %w", err)
	}
	return s.bulkResultWith(ctx, tenantID, ids, changed, func(status models.AlertStatus) string {
		if status == models.AlertStatusResolved {
			return BulkReasonAlreadyResolved
		}
		return BulkReasonNotSnoozed
	})
}

// applyMute runs one mute statement over bulkTargetCTE, so the single and
// bulk actions share its scoping: this tenant, the given statuses, never an
// alert of a deleted monitor, rows locked in id order.
func (s *Service) applyMute(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID, statuses []string, stmt string, extra ...any) ([]uuid.UUID, error) {
	args := append([]any{pq.Array(uuidStrings(ids)), tenantID, pq.Array(statuses)}, extra...)
	return collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+stmt, args...))
}
