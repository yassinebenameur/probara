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
// It returns true only when this call actually sent.
func (a *Alerter) deliverNotification(
	ctx context.Context,
	eventType string,
	binding alertSubject,
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
	// Acknowledge, unacknowledge, snooze and unsnooze are UPDATEs of this row
	// too, so the mute read here is the committed truth for the whole send:
	// an acknowledgement that commits first stops this page or reminder, and
	// one that comes later waits for it.
	var status string
	var resolvedAt *time.Time
	var snoozed bool
	var resumedAt, acknowledgedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT al.status, al.resolved_at, `+alertrouting.SnoozedPredicate("al")+`,
			`+alertrouting.DispatchResumedAtExpr("al")+`, al.acknowledged_at
		FROM alerts al WHERE al.id = $1 FOR SHARE`, alert.ID).Scan(&status, &resolvedAt, &snoozed, &resumedAt, &acknowledgedAt); err != nil {
		return false, fmt.Errorf("lock notification alert: %w", err)
	}
	if (eventType == "resolved") != (status == "resolved") {
		return false, nil
	}
	if eventType == "acknowledged" && status != "acknowledged" {
		return false, nil
	}
	if alertrouting.MuteHolds(eventType, status == "acknowledged", snoozed) {
		// Muted since the caller's pre-filter read the alert. Nothing is
		// claimed, so the event is due again once the mute lifts.
		return false, nil
	}
	if eventType == "resolved" {
		alert.ResolvedAt = resolvedAt
	}

	claimed, round, err := claimNotificationTx(ctx, tx, alert.ID, channel.ID, eventType, now, reminderInterval)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	// A rolled-back claim is retried by whichever replica claims next; the
	// key lets that retry skip the recipients this attempt already reached.
	ctx = withRecipientKey(ctx, notifications.RecipientEventKey(eventType, round))
	ctx = withDispatchRound(ctx, dispatchRound(eventType, round, resumedAt.Time, acknowledgedAt.Time))
	ctx, trace := withSendTrace(ctx)
	if err := a.sendFunc(ctx, channel, eventType, binding, alert, groupInfo, now); err != nil {
		if plugin.IsPermanent(err) {
			// The provider rejected the channel itself (revoked key, deleted
			// webhook). Keep the claim so the next cycle does not re-send and
			// fail identically every evaluation interval; the error is still
			// returned so it is logged.
			a.recordDelivery(ctx, tx, trace, eventType, alert.ID, channel.ID, err)
			if cerr := tx.Commit(); cerr != nil {
				return false, fmt.Errorf("commit notification claim after permanent failure: %w", cerr)
			}
			return false, err
		}
		// Delivery history: the claim is released for the next cycle's retry,
		// so the failed attempt is written through the pool after that
		// rollback, which would otherwise take it along.
		_ = tx.Rollback()
		a.recordDelivery(ctx, nil, trace, eventType, alert.ID, channel.ID, err)
		return false, err
	}
	a.recordDelivery(ctx, tx, trace, eventType, alert.ID, channel.ID, nil)
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit notification claim: %w", err)
	}
	return true, nil
}

// dispatchGate is an open alert's acknowledge/snooze mute as the dispatch
// query read it (shared/alertrouting mute.go): the pre-filter's copy of what
// deliverNotification re-reads under the row lock.
type dispatchGate struct {
	// Muted is alertrouting.MutedPredicate: acknowledged, or snoozed.
	Muted bool
	// ResumedAt is alertrouting.DispatchResumedAtExpr: the last unacknowledge
	// or snooze end; zero when the alert was never muted that way.
	ResumedAt time.Time
}

// openAlertEventDue is the in-memory pre-filter for an open alert's channel:
// which event, if any, is due now. It mirrors deliverNotification's mute
// check and the created and reminder predicates of claimNotificationTx,
// which stay the authority (a second replica can pass this filter and still
// lose the claim); change them together. alertAge counts from when the alert
// became eligible to dispatch (alertrouting.DispatchEligibleSinceExpr, which
// already restarts at gate.ResumedAt), delay is the channel's escalation
// delay. The delay is this filter's rule alone: the created claim does not
// re-check it.
//
//   - acknowledged or snoozed (gate.Muted)                        -> ""
//   - no state yet and the delay has passed                       -> "created"
//   - already sent, not resolved, interval elapsed since the last
//     send and since the alert came out of a mute                 -> "reminder"
//   - otherwise                                                   -> ""
func openAlertEventDue(state *notificationState, alertAge, delay time.Duration, now time.Time, reminderInterval time.Duration, gate dispatchGate) string {
	if gate.Muted {
		return ""
	}
	switch {
	case state == nil:
		if alertAge >= delay {
			return "created"
		}
	case state.LastEventType != "resolved" && reminderInterval > 0 && now.Sub(state.LastSentAt) >= reminderInterval &&
		(gate.ResumedAt.IsZero() || now.Sub(gate.ResumedAt) >= reminderInterval):
		return "reminder"
	}
	return ""
}

// claimNotificationTx reserves the right to send eventType for one
// (alert, channel) pair, inside tx. It encodes the due rules that used to live
// in Go (`state == nil`, `last_event_type != resolved`, reminder interval
// elapsed) as the claim predicate, so a concurrent claimant that re-evaluates
// after our commit sees the slot taken:
//
//   - created:  insert wins; any existing row means someone already fired.
//   - reminder: update wins only when the row is not resolved and
//     last_sent_at is at least reminderInterval old, and so is the alert's
//     way out of its last mute (alertrouting.DispatchResumedAtExpr), so an
//     unacknowledge or a snooze end restarts the reminder clock.
//   - resolved: insert or update wins unless the row already says resolved.
//   - acknowledged: update wins once (acknowledged_sent_at IS NULL) on an
//     existing, unresolved row; it leaves last_event_type alone so reminders
//     and the resolve still flow.
//   - suspected_cause: the same one-shot shape on suspected_cause_sent_at.
//
// The one-shot columns never touch last_event_type or last_sent_at, so the
// created/reminder/resolved timing is identical with or without them.
//
// The acknowledge/snooze mute is not repeated here: deliverNotification
// applies it (alertrouting.MuteHolds) to the row it holds FOR SHARE in the
// same tx, before claiming.
//
// Returns true when a row came back, i.e. the caller owns the send. For a
// reminder it also returns the last_sent_at the claim replaced: the round's
// identity in the per-recipient ledger (notifications.RecipientEventKey). A
// transient failure rolls the claim back, so the retry finds the same value;
// the next round finds the time this one committed.
func claimNotificationTx(ctx context.Context, tx *sql.Tx, alertID, channelID uuid.UUID, eventType string, now time.Time, reminderInterval time.Duration) (bool, time.Time, error) {
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
			  AND NOT EXISTS (
				SELECT 1 FROM alerts al
				WHERE al.id = s.alert_id
				  AND ` + alertrouting.DispatchResumedAtExpr("al") + ` > $3::timestamptz - ($4::double precision * INTERVAL '1 second'))
			RETURNING s.alert_id, prev.last_sent_at`
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

// dispatchRound names which occurrence of eventType a claim sends, for the
// async dispatch message id (sendChannelNotification). It is built from data
// a rolled-back claim finds again on its retry, so JetStream's duplicate
// window collapses a publish whose claim never committed (lost PubAck, failed
// commit, crash) instead of queueing a second message for the slot, and that
// changes exactly when a second send is legitimate:
//
//   - created: the alert's way out of its last mute (resumedAt), which an
//     unacknowledge or snooze end moves: a page the worker handed back is
//     re-paged under a new id, even inside the duplicate window.
//   - reminder: the round, the last_sent_at the claim replaced.
//   - acknowledged: the acknowledgement time; a re-acknowledge after an
//     unacknowledge is a new one.
//   - resolved, suspected_cause: once per alert and channel, no round.
func dispatchRound(eventType string, round, resumedAt, acknowledgedAt time.Time) string {
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	switch eventType {
	case "created":
		return stamp(resumedAt)
	case "reminder":
		return stamp(round)
	case "acknowledged":
		return stamp(acknowledgedAt)
	}
	return ""
}

type dispatchRoundCtx struct{}

// withDispatchRound hands sendChannelNotification the claim's dispatchRound,
// without widening sendFunc.
func withDispatchRound(ctx context.Context, round string) context.Context {
	return context.WithValue(ctx, dispatchRoundCtx{}, round)
}

// dispatchMessageID is the async dispatch Nats-Msg-Id: the idempotency key,
// plus the claim's round when it has one. A send that bypassed the claim
// (none attached) keys a reminder by now, as before, so rounds never merge.
func dispatchMessageID(ctx context.Context, key, eventType string, now time.Time) string {
	round, ok := ctx.Value(dispatchRoundCtx{}).(string)
	if !ok && eventType == "reminder" {
		round = now.UTC().Format(time.RFC3339Nano)
	}
	if round == "" {
		return key
	}
	return key + ":" + round
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
