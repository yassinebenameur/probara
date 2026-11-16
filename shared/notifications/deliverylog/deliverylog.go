// Package deliverylog is the notification delivery history: one append-only
// row in alert_notification_deliveries (migration 000107) per attempt to
// deliver an alert event to a channel — one per recipient on a channel that
// fans out — and per queued event the delivery worker drops. It is the only
// writer of that table, and its three callers are the alerter's synchronous
// send (deliverNotification), the delivery worker's consumer, and the API's
// channel test send. The API reads it back per alert and per channel.
//
// Recording is best effort: a failed write is returned for logging and never
// fails, retries or holds up the delivery. Where the caller already has the
// transaction that records the delivery state open and that transaction
// commits (a successful or permanently failed claim), RecordTx writes inside
// it, under a savepoint, so the history and the claim agree. Where it rolls
// back (a transient failure releases the claim so the next cycle retries),
// Record writes through the pool after the rollback, so the failed attempt
// outlives it.
//
// Nothing secret is stored: errors go through SanitizeError (URLs reduced to
// their host, credential-shaped text masked, the channel's own secret values
// removed verbatim), and recipients are stored masked plus the hash the
// recipient ledger already keys on.
package deliverylog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
)

// RetentionDays is how long rows are kept; the scheduler's retention cleanup
// deletes older ones for every workspace, whatever its data_retention_days.
const RetentionDays = 90

const (
	// recordTimeout bounds a pool write. It runs detached from the delivery
	// deadline: an attempt that timed out is exactly the one worth recording.
	recordTimeout = 5 * time.Second
	// txStatementTimeout and txLockTimeout bound a write inside the caller's
	// claim transaction, which holds the alert row and the claim while it
	// runs. They are server-side limits (SET LOCAL) on purpose: lib/pq closes
	// the connection when a client context fires mid-statement, which would
	// take the caller's transaction, and with it the claim, down with the
	// history write. A server-side timeout is an ordinary statement error the
	// savepoint rolls back.
	txStatementTimeout = "2s"
	txLockTimeout      = "1s"
)

// Outcome is what one attempt came to.
type Outcome string

const (
	// OutcomeDelivered: the provider accepted the event (for this recipient).
	OutcomeDelivered Outcome = "delivered"
	// OutcomeFailed: a transient failure. The event is retried: by the
	// alerter's next cycle on the synchronous path, by the worker's backoff
	// on the asynchronous one (until ReasonRetriesExhausted).
	OutcomeFailed Outcome = "failed"
	// OutcomePermanentFailure: the provider or the platform rejected the send
	// in a way retrying cannot fix (plugin.Permanent); it is not retried.
	OutcomePermanentFailure Outcome = "permanent_failure"
	// OutcomeDropped: the worker did not send a queued event, on purpose;
	// Reason says why.
	OutcomeDropped Outcome = "dropped"
)

// Outcomes lists every Outcome, in the migration's CHECK order.
var Outcomes = []Outcome{OutcomeDelivered, OutcomeFailed, OutcomePermanentFailure, OutcomeDropped}

// Reason qualifies a dropped or failed row. Free text in the table, so a new
// cause needs no migration; the API, MCP tools, UI and docs explain each.
type Reason string

const (
	// ReasonResolved (dropped): the alert resolved after the event was
	// queued. A late trigger or reminder would reopen it on the provider.
	ReasonResolved Reason = "resolved"
	// ReasonUnacknowledged (dropped): an acknowledgement notice whose alert
	// was unacknowledged before it went out.
	ReasonUnacknowledged Reason = "unacknowledged"
	// ReasonMuted (dropped): a page or reminder held because the alert was
	// acknowledged or snoozed after it was queued, and the route still counts
	// as notified: a reminder (the next round follows the mute), or a page
	// an earlier attempt may have delivered, so it gets the acknowledgement
	// and the recovery notice instead of a second page.
	ReasonMuted Reason = "muted"
	// ReasonMutedRequeued (dropped): a held page that provably reached nobody;
	// its claim was handed back, so it is paged once the mute lifts.
	ReasonMutedRequeued Reason = "muted_requeued"
	// ReasonChannelInactive (dropped): the channel was deactivated after the
	// event was queued.
	ReasonChannelInactive Reason = "channel_inactive"
	// ReasonChannelChanged (dropped): the channel's type changed after the
	// event was queued.
	ReasonChannelChanged Reason = "channel_changed"
	// ReasonEgressBlocked (permanent failure): the notification egress
	// policy refused the destination (NOTIFICATION_BLOCK_PRIVATE_IPS).
	ReasonEgressBlocked Reason = "egress_blocked"
	// ReasonInvalidConfig (permanent failure): the stored channel config
	// could not be read.
	ReasonInvalidConfig Reason = "invalid_config"
	// ReasonPluginMissing (failed): this worker build has no plugin for the
	// channel type; retried in case another worker has it.
	ReasonPluginMissing Reason = "plugin_missing"
	// ReasonRetriesExhausted (failed): the worker's last delivery attempt
	// failed; nothing retries it.
	ReasonRetriesExhausted Reason = "retries_exhausted"
)

// Path says which delivery path made the attempt.
type Path string

const (
	// PathSync is a send in the alerter's (or, for tests, the API's) own
	// process.
	PathSync Path = "sync"
	// PathAsync is a send by a delivery worker after the alerter queued it.
	PathAsync Path = "async"
)

// SendOutcome classifies a send's result with the plugin package's error
// classification: nil is delivered, plugin.IsPermanent a permanent failure
// (egress-blocked when netguard refused the destination), anything else a
// transient failure.
func SendOutcome(err error) (Outcome, Reason) {
	switch {
	case err == nil:
		return OutcomeDelivered, ""
	case errors.Is(err, netguard.ErrBlocked):
		return OutcomePermanentFailure, ReasonEgressBlocked
	case plugin.IsPermanent(err):
		return OutcomePermanentFailure, ""
	default:
		return OutcomeFailed, ""
	}
}

// Attempt is one attempt to record.
type Attempt struct {
	// AlertID is uuid.Nil only for a test send.
	AlertID   uuid.UUID
	ChannelID uuid.UUID
	EventType string
	// DeliveryKey is the event's recipient-ledger key
	// (notifications.RecipientEventKey); it tells reminder rounds apart.
	DeliveryKey string
	Path        Path
	// Number is the 1-based attempt number. Zero numbers it after the
	// earlier rows of the same event, path and recipient (the alerter
	// retries by re-claiming, so it has no counter of its own).
	Number  int
	Test    bool
	Outcome Outcome
	Reason  Reason
	// Err is the failure, if any; it is stored sanitized (SanitizeError,
	// with Secrets).
	Err error
	// Secrets are the channel's secret config values (SecretValues), masked
	// out of the stored error wherever they appear.
	Secrets []string
	// Addresses are the channel's configured recipients (RecipientValues),
	// masked in the stored error like stored recipients are. The recipients
	// a tracked fan-out tried are added automatically.
	Addresses []string
	Duration  time.Duration
	// Recipients is the send's per-recipient progress, when it went through
	// a recipient ledger (Track); nil records one row for the channel.
	Recipients *Tracker
}

// Execer is what Record needs: the pool, never a transaction that may roll
// back.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// recordSQL takes the tenant from the channel and requires the alert to be
// the channel's tenant's, so a row can never pair an alert with another
// workspace's channel, and writes nothing for an alert or channel deleted
// meanwhile. One statement writes every recipient row of the attempt, so they
// share one timestamp (statement_timestamp: the write, not the start of the
// caller's transaction). Every parameter is cast: lib/pq sends them untyped
// and Postgres cannot infer a type for one used only in a CASE or an
// IS NOT DISTINCT FROM.
const recordSQL = `
	INSERT INTO alert_notification_deliveries (
		tenant_id, alert_id, channel_id, event_type, delivery_key, recipient, recipient_hash,
		attempt, path, is_test, outcome, reason, error, provider_status, duration_ms, created_at)
	SELECT c.tenant_id, a.id, c.id, $3::text, $4::text, NULLIF(r.recipient, ''), NULLIF(r.recipient_hash, ''),
		CASE WHEN $5::int > 0 THEN $5::int ELSE 1 + (
			SELECT COUNT(*) FROM alert_notification_deliveries p
			WHERE p.alert_id = a.id AND p.channel_id = c.id
			  AND p.delivery_key = $4::text AND p.path = $6::text
			  AND p.recipient_hash IS NOT DISTINCT FROM NULLIF(r.recipient_hash, ''))::int END,
		$6::text, $7::boolean, r.outcome,
		CASE WHEN r.outcome = 'delivered' THEN NULL ELSE NULLIF($8::text, '') END,
		CASE WHEN r.outcome = 'delivered' THEN NULL ELSE NULLIF($9::text, '') END,
		CASE WHEN r.outcome = 'delivered' THEN NULL ELSE $10::int END,
		$11::int, statement_timestamp()
	FROM alert_channels c
	LEFT JOIN alerts a ON a.id = $1::uuid AND a.tenant_id = c.tenant_id
	CROSS JOIN unnest($12::text[], $13::text[], $14::text[]) AS r(recipient, recipient_hash, outcome)
	WHERE c.id = $2::uuid
	  AND ($1::uuid IS NULL OR a.id IS NOT NULL)`

// args builds recordSQL's arguments; ok is false when the attempt has no row
// to write (a retry that found every recipient already delivered).
func (a Attempt) args() ([]any, bool) {
	var recipients, hashes, outcomes []string
	if a.Recipients != nil && a.Recipients.fannedOut() {
		for _, r := range a.Recipients.results(a.Outcome) {
			recipients = append(recipients, MaskRecipient(r.recipient))
			// The ledger's own key, so a row matches its ledger entry
			// without the table keeping the address.
			hashes = append(hashes, recipientledger.Hash(r.recipient))
			outcomes = append(outcomes, string(r.outcome))
		}
		if len(outcomes) == 0 {
			return nil, false
		}
	} else {
		recipients, hashes, outcomes = []string{""}, []string{""}, []string{string(a.Outcome)}
	}
	var errText string
	if a.Err != nil {
		addresses := a.Addresses
		if a.Recipients != nil {
			addresses = append(append([]string(nil), addresses...), a.Recipients.tried()...)
		}
		errText = sanitize(a.Err.Error(), a.Secrets, addresses)
	}
	var status sql.NullInt64
	if s, ok := plugin.ResponseStatus(a.Err); ok {
		status = sql.NullInt64{Int64: int64(s), Valid: true}
	}
	var duration sql.NullInt64
	if a.Duration > 0 {
		duration = sql.NullInt64{Int64: a.Duration.Milliseconds(), Valid: true}
	}
	return []any{
		nullUUID(a.AlertID), a.ChannelID, a.EventType, a.DeliveryKey,
		a.Number, string(a.Path), a.Test, string(a.Reason), errText, status, duration,
		pq.StringArray(recipients), pq.StringArray(hashes), pq.StringArray(outcomes),
	}, true
}

// Record appends the attempt through db, which must be the pool. A nil db is
// a no-op, and so is a missing table (this binary running ahead of the
// migrations job).
func Record(ctx context.Context, db Execer, a Attempt) error {
	if db == nil {
		return nil
	}
	args, ok := a.args()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, recordSQL, args...); err != nil && !pgerr.IsUndefinedTable(err) {
		return fmt.Errorf("record notification delivery: %w", err)
	}
	return nil
}

// RecordTx appends the attempt inside tx, the caller's claim or lock
// transaction, so the row commits (or not) with the delivery state it
// describes. It runs under a savepoint with server-side statement and lock
// timeouts (never a client deadline: see txStatementTimeout): a failed or
// slow write is rolled back to the savepoint and returned, and tx stays
// usable, so the caller's commit never fails because of the history. The
// caller's own timeouts are back in force afterwards. Use it only on a path
// where tx commits.
func RecordTx(ctx context.Context, tx *sql.Tx, a Attempt) error {
	if tx == nil {
		return nil
	}
	args, ok := a.args()
	if !ok {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT delivery_log`); err != nil {
		return fmt.Errorf("record notification delivery: savepoint: %w", err)
	}
	// Bound the write on the server. Settings changed inside a savepoint
	// are undone by ROLLBACK TO SAVEPOINT; on success they are put back by
	// hand before the release.
	var prevStatement, prevLock string
	err := tx.QueryRowContext(ctx, `SELECT current_setting('statement_timeout'), current_setting('lock_timeout')`).
		Scan(&prevStatement, &prevLock)
	if err == nil {
		_, err = tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $2, true)`,
			txStatementTimeout, txLockTimeout)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, recordSQL, args...)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $2, true)`,
			prevStatement, prevLock)
	}
	if err != nil {
		if _, rerr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT delivery_log`); rerr != nil {
			return fmt.Errorf("record notification delivery: %v; roll back to savepoint: %w", err, rerr)
		}
		if pgerr.IsUndefinedTable(err) {
			return nil
		}
		return fmt.Errorf("record notification delivery: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT delivery_log`); err != nil {
		return fmt.Errorf("record notification delivery: release savepoint: %w", err)
	}
	return nil
}

func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}
