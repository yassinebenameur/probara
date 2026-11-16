package notifications

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// deliveryRecord is one dispatch message's row in the delivery history
// (shared/notifications/deliverylog): the consumer creates it per message,
// fills in the channel's secrets and recipient ledger as it learns them, and
// calls exactly one of dropped, droppedTx or finished at the outcome. Every
// write is best effort: a failure is logged and the message is handled as
// if nothing was recorded.
type deliveryRecord struct {
	c         *Consumer
	entry     *logrus.Entry
	alertID   uuid.UUID
	channelID uuid.UUID
	valid     bool
	eventType string
	key       string
	attempt   int
	started   time.Time

	secrets    []string
	addresses  []string
	recipients *deliverylog.Tracker
}

func (c *Consumer) deliveryRecord(entry *logrus.Entry, envelope notifications.DispatchEnvelope, attempt int) *deliveryRecord {
	alertID, aerr := uuid.Parse(envelope.AlertID)
	channelID, cerr := uuid.Parse(envelope.ChannelID)
	return &deliveryRecord{
		c:         c,
		entry:     entry,
		alertID:   alertID,
		channelID: channelID,
		valid:     aerr == nil && cerr == nil,
		eventType: envelope.EventType,
		key:       envelope.RecipientKey(),
		attempt:   attempt,
		started:   time.Now(),
	}
}

// channelLoaded notes the decrypted channel config's secret values, masked
// out of any stored error, and its configured recipients, masked in it.
func (r *deliveryRecord) channelLoaded(manifest plugin.Manifest, config map[string]any) {
	r.secrets = deliverylog.SecretValues(manifest, config)
	r.addresses = deliverylog.RecipientValues(manifest, config)
}

// track wraps the send's recipient ledger so the history gets one row per
// recipient this attempt tried. A nil ledger stays nil (untracked).
func (r *deliveryRecord) track(ledger plugin.RecipientLedger) plugin.RecipientLedger {
	if ledger == nil {
		return nil
	}
	r.recipients = deliverylog.Track(ledger)
	return r.recipients
}

func (r *deliveryRecord) row(outcome deliverylog.Outcome, reason deliverylog.Reason, err error) deliverylog.Attempt {
	return deliverylog.Attempt{
		AlertID:     r.alertID,
		ChannelID:   r.channelID,
		EventType:   r.eventType,
		DeliveryKey: r.key,
		Path:        deliverylog.PathAsync,
		Number:      r.attempt,
		Outcome:     outcome,
		Reason:      reason,
		Err:         err,
		Secrets:     r.secrets,
		Addresses:   r.addresses,
		Duration:    time.Since(r.started),
		Recipients:  r.recipients,
	}
}

// releaseLock rolls back lock, the alert-lock transaction of a path that
// returns right after recording without committing it, so the pool write that
// follows never runs while the alert row is held: with more handlers than pool
// connections it could wait, and an acknowledge or resolve with it. Nil (no
// lock taken, or a resolve) is a no-op; the caller's deferred rollback then
// finds the transaction done.
func releaseLock(lock *sql.Tx) {
	if lock != nil {
		_ = lock.Rollback()
	}
}

// dropped records an event the consumer decided not to send, through the
// pool, after releasing lock.
func (r *deliveryRecord) dropped(ctx context.Context, reason deliverylog.Reason, lock *sql.Tx) {
	releaseLock(lock)
	r.write(ctx, nil, r.row(deliverylog.OutcomeDropped, reason, nil))
}

// droppedTx records a drop inside tx, the alert-lock transaction that also
// hands the claim back and then commits, so the row and the claim agree.
func (r *deliveryRecord) droppedTx(ctx context.Context, tx *sql.Tx, reason deliverylog.Reason) {
	r.write(ctx, tx, r.row(deliverylog.OutcomeDropped, reason, nil))
}

// failed records an attempt that failed before reaching the plugin's Send,
// through the pool, after releasing lock.
func (r *deliveryRecord) failed(ctx context.Context, outcome deliverylog.Outcome, reason deliverylog.Reason, err error, lock *sql.Tx) {
	releaseLock(lock)
	r.write(ctx, nil, r.row(outcome, r.finalReason(outcome, reason), err))
}

// finished records the plugin's Send result through the pool, after releasing
// lock: the lock transaction around Send always rolls back.
func (r *deliveryRecord) finished(ctx context.Context, sendErr error, lock *sql.Tx) {
	releaseLock(lock)
	outcome, reason := deliverylog.SendOutcome(sendErr)
	r.write(ctx, nil, r.row(outcome, r.finalReason(outcome, reason), sendErr))
}

// finalReason marks a transient failure on the last delivery JetStream will
// make: nothing retries it, so "failed" alone would read as pending. It
// replaces any other reason (plugin_missing included); the stored error
// still names the cause.
func (r *deliveryRecord) finalReason(outcome deliverylog.Outcome, reason deliverylog.Reason) deliverylog.Reason {
	if outcome == deliverylog.OutcomeFailed && r.attempt >= MaxDeliver {
		return deliverylog.ReasonRetriesExhausted
	}
	return reason
}

// mutedReason tells, after releaseMutedClaim, whether the held page's route
// is due again once the mute lifts (no claim row left for the pair: handed
// back) or still counts as notified (a reminder, a page that may have
// reached the provider, or a claim that moved on). It reads inside tx, which
// still holds the alert row, so no claim moved meanwhile, under a savepoint
// so a failed read can never abort the transaction that commits the hand-back;
// on any error it says "muted", the reading that promises nothing.
func mutedReason(ctx context.Context, tx *sql.Tx, envelope notifications.DispatchEnvelope) deliverylog.Reason {
	if envelope.EventType != "created" {
		return deliverylog.ReasonMuted
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return deliverylog.ReasonMuted
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT delivery_log_muted`); err != nil {
		return deliverylog.ReasonMuted
	}
	var claimed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2)`,
		alertID, channelID).Scan(&claimed)
	if err != nil {
		_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT delivery_log_muted`)
		return deliverylog.ReasonMuted
	}
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT delivery_log_muted`)
	if claimed {
		return deliverylog.ReasonMuted
	}
	return deliverylog.ReasonMutedRequeued
}

// supersededReason names why eventSuperseded dropped an event, from the
// alert status it read: resolved, or (for an acknowledgement) unacknowledged.
// A deleted alert reads as "" and leaves no row to write.
func supersededReason(alertStatus string) deliverylog.Reason {
	if alertStatus == "resolved" {
		return deliverylog.ReasonResolved
	}
	return deliverylog.ReasonUnacknowledged
}

func (r *deliveryRecord) write(ctx context.Context, tx *sql.Tx, a deliverylog.Attempt) {
	if r == nil || !r.valid || r.c.db == nil {
		return
	}
	var err error
	if tx != nil {
		err = deliverylog.RecordTx(ctx, tx, a)
	} else {
		err = deliverylog.Record(ctx, r.c.db, a)
	}
	if err != nil {
		r.entry.WithError(err).Warn("Failed to record notification delivery")
	}
}
