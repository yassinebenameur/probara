package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// deliverNotification is the ONLY path that sends a channel notification and
// records it in alert_notification_states. It claims the (alert, channel,
// event) slot in a transaction first, sends while holding that claim, and
// commits only after the send succeeded.
//
// Why a transaction and not "record after send": the alerter runs with several
// replicas and no leader election, so two processes evaluate the same open
// alert within milliseconds of each other. Reading the state, sending, then
// upserting lets both see "not sent yet" and both deliver. Claiming first makes
// Postgres the arbiter — the second replica blocks on the row until the first
// commits, then re-evaluates the claim predicate against the committed row and
// finds the slot already taken. Rolling the transaction back on a failed send
// releases the claim, so a transient channel error still retries on the next
// cycle exactly as it did before.
//
// It returns true only when this call actually sent.
func (a *Alerter) deliverNotification(
	ctx context.Context,
	eventType string,
	binding policyBinding,
	alert *alertRecord,
	groupInfo *groupDetail,
	channel alertChannel,
	now time.Time,
	reminderInterval time.Duration,
) (bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin notification claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize with resolution as well as other sends. A dispatcher may have
	// loaded an open alert just before another replica resolved it; it must
	// not send a stale DOWN after that recovery. This lock also makes the
	// fired-channel state visible before resolution can commit.
	var status string
	var resolvedAt *time.Time
	if err := tx.QueryRowContext(ctx, `SELECT status, resolved_at FROM alerts WHERE id = $1 FOR UPDATE`, alert.ID).Scan(&status, &resolvedAt); err != nil {
		return false, fmt.Errorf("lock notification alert: %w", err)
	}
	if (eventType == "resolved") != (status == "resolved") {
		return false, nil
	}
	if eventType == "resolved" {
		alert.ResolvedAt = resolvedAt
	}

	claimed, err := claimNotificationTx(ctx, tx, alert.ID, channel.ID, eventType, now, reminderInterval)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	if err := a.sendFunc(ctx, channel, eventType, binding, alert, groupInfo, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit notification claim: %w", err)
	}
	return true, nil
}

// claimNotificationTx reserves the right to send eventType for one
// (alert, channel) pair, inside tx. It encodes the due rules that used to live
// in Go (`state == nil`, `last_event_type != resolved`, reminder interval
// elapsed) as the claim predicate, so a concurrent claimant that re-evaluates
// after our commit sees the slot taken:
//
//   - created:  insert wins; any existing row means someone already fired.
//   - reminder: update wins only when the row is not resolved and
//     last_sent_at is at least reminderInterval old.
//   - resolved: insert or update wins unless the row already says resolved.
//
// Returns true when a row came back, i.e. the caller owns the send.
func claimNotificationTx(ctx context.Context, tx *sql.Tx, alertID, channelID uuid.UUID, eventType string, now time.Time, reminderInterval time.Duration) (bool, error) {
	var query string
	args := []interface{}{alertID, channelID, now}
	switch eventType {
	case "created":
		query = `
			INSERT INTO alert_notification_states (
				alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at
			) VALUES ($1, $2, $3, 'created', NOW(), NOW())
			ON CONFLICT (alert_id, channel_id) DO NOTHING
			RETURNING alert_id`
	case "reminder":
		if reminderInterval <= 0 {
			return false, nil
		}
		query = `
			UPDATE alert_notification_states
			SET last_sent_at = $3::timestamptz, last_event_type = 'reminder', updated_at = NOW()
			WHERE alert_id = $1 AND channel_id = $2
			  AND last_event_type <> 'resolved'
			  AND last_sent_at <= $3::timestamptz - ($4::double precision * INTERVAL '1 second')
			RETURNING alert_id`
		args = append(args, reminderInterval.Seconds())
	case "resolved":
		query = `
			INSERT INTO alert_notification_states (
				alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at
			) VALUES ($1, $2, $3, 'resolved', NOW(), NOW())
			ON CONFLICT (alert_id, channel_id) DO UPDATE
			SET last_sent_at = EXCLUDED.last_sent_at, last_event_type = 'resolved', updated_at = NOW()
			WHERE alert_notification_states.last_event_type <> 'resolved'
			RETURNING alert_id`
	default:
		return false, fmt.Errorf("unknown notification event type %q", eventType)
	}

	var claimedAlertID uuid.UUID
	err := tx.QueryRowContext(ctx, query, args...).Scan(&claimedAlertID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim %s notification: %w", eventType, err)
	}
	return true, nil
}
