package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
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
// cycle exactly as it did before; a plugin.Permanent error keeps the claim.
//
// stopOnAck says whether an acknowledgement silences created/reminder on
// this route (DispatchGate.stopOnAck); it is re-checked against the alert row
// under the lock, together with the snooze, so an ack or snooze that commits
// before the claim always wins (claimStatusAllows). Pass false for events the
// ack never holds (resolved, acknowledged, suspected_cause).
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
	stopOnAck bool,
) (bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin notification claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize with resolution. A dispatcher may have loaded an open alert
	// just before another replica resolved it; it must not send a stale DOWN
	// after that recovery. This lock also makes the fired-channel state
	// visible before resolution can commit.
	//
	// FOR SHARE, not FOR UPDATE: resolution is an UPDATE of this row, so a
	// share lock blocks it just the same. Concurrent sends need no mutual
	// exclusion here — two claimants of the same (alert, channel) slot
	// serialize on the alert_notification_states row in claimNotificationTx.
	// An exclusive lock would also conflict with the share lock the async
	// worker holds through its provider call, stalling this loop (and every
	// other channel of the alert) on webhook latency.
	//
	// The same lock serializes with acknowledge, unacknowledge and snooze
	// (each an UPDATE of this row), so the ack/snooze gate read here is the
	// committed truth for the whole send.
	var status string
	var resolvedAt, resumedAt *time.Time
	var snoozed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT status, resolved_at, `+alertrouting.SnoozedPredicate("al")+`, `+alertrouting.DispatchResumedAtExpr("al")+`
		FROM alerts al WHERE al.id = $1 FOR SHARE`, alert.ID).Scan(&status, &resolvedAt, &snoozed, &resumedAt); err != nil {
		return false, fmt.Errorf("lock notification alert: %w", err)
	}
	if !claimStatusAllows(eventType, status, snoozed, stopOnAck) {
		return false, nil
	}
	if eventType == "resolved" {
		alert.ResolvedAt = resolvedAt
	}

	claimed, round, err := claimNotificationTx(ctx, tx, alert.ID, channel.ID, eventType, now, reminderInterval, resumedAt)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	// A rolled-back claim is retried by whichever replica claims next; the
	// key lets that retry skip the recipients this attempt already reached.
	ctx = withRecipientKey(ctx, notifications.RecipientEventKey(eventType, round))
	// The async path re-checks the ack gate in the delivery worker, which
	// may send long after this claim (JetStream backoff).
	ctx = withStopOnAck(ctx, stopOnAck)
	if err := a.sendFunc(ctx, channel, eventType, binding, alert, groupInfo, now); err != nil {
		if plugin.IsPermanent(err) {
			// The provider rejected the channel itself (revoked key, deleted
			// webhook). Keep the claim so the next cycle does not re-send and
			// fail identically every evaluation interval; the error is still
			// returned so it is logged.
			if cerr := tx.Commit(); cerr != nil {
				return false, fmt.Errorf("commit notification claim after permanent failure: %w", cerr)
			}
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit notification claim: %w", err)
	}
	return true, nil
}

// claimStatusAllows is the alert-row half of the claim: whether eventType may
// go out given the alert's locked status and snooze. Resolves go only to a
// resolved alert and everything else only to an open one; an acknowledgement
// only while acknowledged; and gateHolds applies the ack/snooze mutes.
// shouldSendNotification's gate check and dueEvent mirror it
// (TestClaimStatusMatchesPreFilter).
func claimStatusAllows(eventType, status string, snoozed, stopOnAck bool) bool {
	if (eventType == "resolved") != (status == "resolved") {
		return false
	}
	if eventType == "acknowledged" && status != "acknowledged" {
		return false
	}
	return !gateHolds(eventType, status == "acknowledged", snoozed, stopOnAck)
}

// claimNotificationTx reserves the right to send eventType for one
// (alert, channel) pair, inside tx. It encodes the due rules that used to live
// in Go (`state == nil`, `last_event_type != resolved`, reminder interval
// elapsed) as the claim predicate, so a concurrent claimant that re-evaluates
// after our commit sees the slot taken:
//
//   - created:  insert wins; any existing row means someone already fired.
//   - reminder: update wins only when the row is not resolved and
//     last_sent_at — and resumedAt, the alert's last unacknowledge or snooze
//     end (nil if never) — is at least reminderInterval old.
//   - resolved: insert or update wins unless the row already says resolved.
//   - acknowledged: update wins once (acknowledged_sent_at IS NULL) on an
//     existing, unresolved row; it leaves last_event_type alone so reminders
//     and the resolve still flow.
//   - suspected_cause: the same one-shot shape on suspected_cause_sent_at.
//
// The one-shot columns never touch last_event_type or last_sent_at, so the
// created/reminder/resolved timing is identical with or without them.
//
// Returns true when a row came back, i.e. the caller owns the send. For a
// reminder it also returns the last_sent_at the claim replaced: the round's
// identity in the per-recipient ledger (notifications.RecipientEventKey). A
// transient failure rolls the claim back, so the retry finds the same value;
// the next round finds the time this one committed.
func claimNotificationTx(ctx context.Context, tx *sql.Tx, alertID, channelID uuid.UUID, eventType string, now time.Time, reminderInterval time.Duration, resumedAt *time.Time) (bool, time.Time, error) {
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
			return false, time.Time{}, nil
		}
		// prev is the same row as read by the statement's snapshot, i.e. the
		// last_sent_at being replaced. The due predicate stays on s, which a
		// blocked concurrent claimant re-checks against the committed row.
		query = `
			UPDATE alert_notification_states s
			SET last_sent_at = $3::timestamptz, last_event_type = 'reminder', updated_at = NOW()
			FROM alert_notification_states prev
			WHERE s.alert_id = $1 AND s.channel_id = $2
			  AND prev.alert_id = s.alert_id AND prev.channel_id = s.channel_id
			  AND s.last_event_type <> 'resolved'
			  AND s.last_sent_at <= $3::timestamptz - ($4::double precision * INTERVAL '1 second')
			  AND ($5::timestamptz IS NULL OR $5::timestamptz <= $3::timestamptz - ($4::double precision * INTERVAL '1 second'))
			RETURNING s.alert_id, prev.last_sent_at`
		args = append(args, reminderInterval.Seconds(), resumedAt)
	case "resolved":
		query = `
			INSERT INTO alert_notification_states (
				alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at
			) VALUES ($1, $2, $3, 'resolved', NOW(), NOW())
			ON CONFLICT (alert_id, channel_id) DO UPDATE
			SET last_sent_at = EXCLUDED.last_sent_at, last_event_type = 'resolved', updated_at = NOW()
			WHERE alert_notification_states.last_event_type <> 'resolved'
			RETURNING alert_id`
	case "acknowledged":
		// One-shot, and only for a channel that was actually paged: the row
		// must exist (created fired) and the alert must not have resolved.
		query = `
			UPDATE alert_notification_states
			SET acknowledged_sent_at = $3::timestamptz, updated_at = NOW()
			WHERE alert_id = $1 AND channel_id = $2
			  AND acknowledged_sent_at IS NULL
			  AND last_event_type <> 'resolved'
			RETURNING alert_id`
	case "suspected_cause":
		// Advisory follow-up: one-shot, and only for a channel that was
		// paged for this still-open alert (see dispatchSuspectedCauses).
		query = `
			UPDATE alert_notification_states
			SET suspected_cause_sent_at = $3::timestamptz, updated_at = NOW()
			WHERE alert_id = $1 AND channel_id = $2
			  AND suspected_cause_sent_at IS NULL
			  AND last_event_type <> 'resolved'
			RETURNING alert_id`
	default:
		return false, time.Time{}, fmt.Errorf("unknown notification event type %q", eventType)
	}

	var claimedAlertID uuid.UUID
	var round time.Time
	dest := []any{&claimedAlertID}
	if eventType == "reminder" {
		dest = append(dest, &round)
	}
	err := tx.QueryRowContext(ctx, query, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, fmt.Errorf("claim %s notification: %w", eventType, err)
	}
	return true, round, nil
}

type stopOnAckCtx struct{}

// withStopOnAck hands sendChannelNotification the route's stop-on-ack flag
// for the async envelope, without widening sendFunc.
func withStopOnAck(ctx context.Context, stopOnAck bool) context.Context {
	return context.WithValue(ctx, stopOnAckCtx{}, stopOnAck)
}

// stopOnAckFrom returns the flag deliverNotification attached (false when the
// send bypassed the claim).
func stopOnAckFrom(ctx context.Context) bool {
	v, _ := ctx.Value(stopOnAckCtx{}).(bool)
	return v
}

type recipientKeyCtx struct{}

// withRecipientKey hands sendChannelNotification the ledger key of the event
// deliverNotification claimed, without widening sendFunc.
func withRecipientKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, recipientKeyCtx{}, key)
}

// recipientKey returns the key deliverNotification attached, or derives one
// from now for a send that bypassed the claim.
func recipientKey(ctx context.Context, eventType string, now time.Time) string {
	if key, ok := ctx.Value(recipientKeyCtx{}).(string); ok && key != "" {
		return key
	}
	return notifications.RecipientEventKey(eventType, now)
}
