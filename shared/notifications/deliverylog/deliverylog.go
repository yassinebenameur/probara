// Package deliverylog is the notification delivery history: one append-only
// row in alert_notification_deliveries (migration 000110) per attempt to
// deliver an alert event to a channel, and per event the delivery worker
// drops. The alerter records its synchronous sends (or `queued` when it hands
// the event to the delivery workers), the worker records every attempt it
// makes, and the API reads them back per alert and per channel.
//
// Writes go through the connection pool, never the caller's notification
// claim or alert-lock transaction — the same rule as package
// recipientledger: a failed send rolls that transaction back, and the row
// that says it failed must survive it. Recording is best effort: a failed
// write is returned for logging and never fails or retries the delivery.
package deliverylog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// RetentionDays is how long delivery rows are kept; the scheduler's retention
// cleanup prunes older ones for every workspace.
const RetentionDays = 90

// recordTimeout bounds a Record. It runs on a context detached from the
// delivery deadline, like the recipient ledger: an attempt that timed out
// is exactly the one worth recording.
const recordTimeout = 5 * time.Second

// Outcome is what one attempt came to.
type Outcome string

const (
	// OutcomeQueued: the alerter handed the event to the delivery workers
	// (asynchronous dispatch); their rows say what happened next.
	OutcomeQueued Outcome = "queued"
	// OutcomeDelivered: the channel's provider accepted the event.
	OutcomeDelivered Outcome = "delivered"
	// OutcomeFailed: a transient failure; the event is retried (the next
	// alerter cycle, or the worker's backoff) until the retries run out.
	OutcomeFailed Outcome = "failed"
	// OutcomePermanentFailure: the provider rejected the channel itself
	// (plugin.Permanent); it is not retried.
	OutcomePermanentFailure Outcome = "permanent_failure"
	// OutcomeDroppedSuperseded: the worker dropped a queued event because
	// the alert moved on before it could be delivered: it resolved, was
	// unacknowledged (for an acknowledgement), or reopened (for an event of
	// an earlier paging episode, a recovery included). Not always a resolve,
	// so the UI labels it "superseded".
	OutcomeDroppedSuperseded Outcome = "dropped_superseded"
	// OutcomeDroppedAck: the worker held back a queued event because the
	// alert was acknowledged (on a route that stops paging on ack) or
	// snoozed since it was queued; it fires again once the mute lifts.
	OutcomeDroppedAck Outcome = "dropped_ack"
)

// Outcomes lists every Outcome, in the migration's CHECK order.
var Outcomes = []Outcome{
	OutcomeQueued, OutcomeDelivered, OutcomeFailed, OutcomePermanentFailure,
	OutcomeDroppedSuperseded, OutcomeDroppedAck,
}

// Path says which delivery path made the attempt.
type Path string

const (
	// PathSync is the alerter sending in its own process.
	PathSync Path = "sync"
	// PathAsync is the JetStream hand-off and the delivery worker.
	PathAsync Path = "async"
)

// SendOutcome classifies a send's result: delivered, permanent failure
// (plugin.IsPermanent), or a transient failure.
func SendOutcome(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeDelivered
	case plugin.IsPermanent(err):
		return OutcomePermanentFailure
	default:
		return OutcomeFailed
	}
}

// Entry is one row to record.
type Entry struct {
	AlertID uuid.UUID
	// ChannelID is the channel the send went through. uuid.Nil only for a
	// person send that needs no channel (an escalation page by email,
	// through the platform mailer); such an entry must carry UserID and
	// Recipient.
	ChannelID uuid.UUID
	// ChannelName is the channel's name at send time; empty takes the
	// channel's current name.
	ChannelName string
	// UserID, ContactID and Recipient (the masked address,
	// contacts.Mask) identify a person send: an escalation policy paging a
	// user's verified contact (migration 000113). Zero/empty for channel
	// sends.
	UserID    uuid.UUID
	ContactID uuid.UUID
	Recipient string
	EventType string
	// DeliveryKey is the event's recipient-ledger key
	// (notifications.RecipientEventKey): it tells reminder rounds apart.
	DeliveryKey string
	// Attempt is 1-based. Zero numbers it after the earlier rows of the same
	// event on the same path (the alerter retries by re-claiming, so it has
	// no counter of its own).
	Attempt int
	Path    Path
	Outcome Outcome
	// Err is the failure, if any; it is stored sanitized (SanitizeError).
	Err error
	// Recipients carries the fan-out counts of the send, when the plugin
	// used a recipient ledger.
	Recipients *recipientledger.Tally
}

// Execer is what Record needs: the pool, never a claim transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// recordSQL derives tenant_id from the alert and joins the channel on the same
// tenant, so a row can never pair an alert with another workspace's channel,
// and writes nothing for an alert or channel deleted meanwhile or for a
// workspace being deleted (tenancy.LiveTenantSQL).
//
// A person send ($12 user, $13 contact, $14 masked recipient) may have no
// channel ($2 NULL, an email page): the channel join is then a LEFT JOIN
// that must stay empty, and the attempt counter keys on the contact.
var recordSQL = `
	INSERT INTO alert_notification_deliveries (
		tenant_id, alert_id, channel_id, channel_name, event_type, delivery_key,
		attempt, path, outcome, error, recipients_total, recipients_delivered,
		user_id, contact_id, recipient)
	SELECT a.tenant_id, a.id, c.id, COALESCE(NULLIF($3::text, ''), c.name, 'Email'), $4::text, $5::text,
		CASE WHEN $6::int > 0 THEN $6::int ELSE 1 + (
			SELECT COUNT(*) FROM alert_notification_deliveries p
			WHERE p.alert_id = a.id AND p.channel_id IS NOT DISTINCT FROM c.id
			  AND p.contact_id IS NOT DISTINCT FROM $13::uuid
			  AND p.delivery_key = $5::text AND p.path = $7::text)::int END,
		$7::text, $8::text, $9::text, $10::int, $11::int,
		u.id, $13::uuid, NULLIF($14::text, '')
	FROM alerts a
	LEFT JOIN alert_channels c ON c.id = $2::uuid AND c.tenant_id = a.tenant_id
	LEFT JOIN admin_users u ON u.id = $12::uuid
	WHERE a.id = $1 AND ` + tenancy.LiveTenantSQL("a.tenant_id") + `
	  AND ($2::uuid IS NULL OR c.id IS NOT NULL)
	  AND ($2::uuid IS NOT NULL OR (u.id IS NOT NULL AND $14::text <> ''))`

// Record appends e to the history. It is safe to call with a nil db (no-op).
// Before migration 000110 has run it records nothing and returns nil.
func Record(ctx context.Context, db Execer, e Entry) error {
	if db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	var errText sql.NullString
	if e.Err != nil {
		errText = sql.NullString{String: SanitizeError(e.Err.Error()), Valid: true}
	}
	var total, delivered sql.NullInt64
	if t, d, ok := e.Recipients.Counts(); ok && t > 0 {
		total = sql.NullInt64{Int64: int64(t), Valid: true}
		delivered = sql.NullInt64{Int64: int64(d), Valid: true}
	}
	_, err := db.ExecContext(ctx, recordSQL,
		e.AlertID, nullUUID(e.ChannelID), e.ChannelName, e.EventType, e.DeliveryKey,
		e.Attempt, string(e.Path), string(e.Outcome), errText, total, delivered,
		nullUUID(e.UserID), nullUUID(e.ContactID), e.Recipient)
	if isUndefinedTable(err) {
		// Rollout window: this binary runs ahead of the migrations job.
		return nil
	}
	return err
}

// nullUUID maps uuid.Nil to SQL NULL.
func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func isUndefinedTable(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42P01"
}
