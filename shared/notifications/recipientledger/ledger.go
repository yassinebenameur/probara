// Package recipientledger is the Postgres-backed plugin.RecipientLedger: the
// per-recipient progress of one notification event, kept in
// alert_notification_recipient_deliveries (migration 000097).
//
// Every retry of a notification send can land in a different process — the
// alerter re-claims a failed synchronous send on its next cycle, on whichever
// replica gets there first, and JetStream redelivers a failed async dispatch
// to any worker, after restarts included — so the progress lives in the
// database both of them share, never in memory.
package recipientledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// recordTimeout bounds a Record. It runs on a context detached from the
// dispatch deadline: a provider that accepted the message just before that
// deadline must still be recorded, or the retry would text it again.
const recordTimeout = 5 * time.Second

// DB is what the ledger needs from a connection pool. It must be the pool,
// never the caller's claim or lock transaction: those roll back on the very
// failure the ledger exists to remember.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// For returns the ledger of one event (eventKey, see
// notifications.RecipientEventKey) for one (alert, channel) pair. A nil db
// yields nil, which plugins treat as untracked.
func For(db DB, alertID, channelID uuid.UUID, eventKey string) plugin.RecipientLedger {
	if db == nil {
		return nil
	}
	return &ledger{db: db, alertID: alertID, channelID: channelID, eventKey: eventKey}
}

type ledger struct {
	db        DB
	alertID   uuid.UUID
	channelID uuid.UUID
	eventKey  string
}

func (l *ledger) Delivered(ctx context.Context, recipients []string) (map[string]bool, error) {
	byHash := make(map[string]string, len(recipients))
	hashes := make([]string, 0, len(recipients))
	for _, r := range recipients {
		h := hash(r)
		byHash[h] = r
		hashes = append(hashes, h)
	}
	done := map[string]bool{}
	if len(hashes) == 0 {
		return done, nil
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT recipient_hash
		FROM alert_notification_recipient_deliveries
		WHERE alert_id = $1 AND channel_id = $2 AND event_key = $3
		  AND recipient_hash = ANY($4)`,
		l.alertID, l.channelID, l.eventKey, pq.Array(hashes))
	if err != nil {
		if pgerr.IsUndefinedTable(err) {
			// Rollout window before migration 000097: with no table nothing
			// can have been recorded, so "none delivered" is exact.
			return done, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		if r, ok := byHash[h]; ok {
			done[r] = true
		}
	}
	return done, rows.Err()
}

func (l *ledger) Record(ctx context.Context, recipient string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	// The join keeps a row from pairing an alert with another tenant's
	// channel, whatever the caller passes.
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash)
		SELECT a.id, c.id, $3, $4
		FROM alerts a
		JOIN alert_channels c ON c.id = $2 AND c.tenant_id = a.tenant_id
		WHERE a.id = $1
		ON CONFLICT DO NOTHING`,
		l.alertID, l.channelID, l.eventKey, hash(recipient))
	return err
}

// attemptMarker is the recipient_hash of the channel-level "possibly reached"
// marker (MarkAttempt). It is not a SHA-256 hex digest, so it can never
// collide with a recipient's row, and Delivered never matches it.
const attemptMarker = "attempt"

// MarkAttempt records, through the pool, that a send of the event is about to
// leave for the (alert, channel) pair, before the provider is called, so the
// marker survives a crash or a rolled-back transaction: from then on the event
// counts as possibly reached until ClearAttempt proves otherwise. It reports
// whether this call created the marker (false when an earlier attempt's is
// still there).
func MarkAttempt(ctx context.Context, db DB, alertID, channelID uuid.UUID, eventKey string) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash)
		SELECT a.id, c.id, $3, $4
		FROM alerts a
		JOIN alert_channels c ON c.id = $2 AND c.tenant_id = a.tenant_id
		WHERE a.id = $1
		ON CONFLICT DO NOTHING`,
		alertID, channelID, eventKey, attemptMarker)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ClearAttempt removes the event's marker: the attempt that created it
// failed in a way that proves nothing was accepted (plugin.IsNotDelivered),
// or the alert resolved and the marker has served its purpose.
func ClearAttempt(ctx context.Context, db DB, alertID, channelID uuid.UUID, eventKey string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM alert_notification_recipient_deliveries
		WHERE alert_id = $1 AND channel_id = $2 AND event_key = $3 AND recipient_hash = $4`,
		alertID, channelID, eventKey, attemptMarker)
	return err
}

// Reached reports whether the event (eventKey) possibly reached anyone on
// the (alert, channel) pair: an attempt marker (MarkAttempt) or a recipient
// recorded delivered. q may be a transaction: it only reads rows that
// earlier attempts committed through the pool. Before migration 000097
// nothing can have been recorded, so a missing table reads as false.
func Reached(ctx context.Context, q DB, alertID, channelID uuid.UUID, eventKey string) (bool, error) {
	return ReachedAny(ctx, q, alertID, channelID, []string{eventKey})
}

// ReachedAny is Reached for whichever of eventKeys: true when any of them
// possibly reached anyone on the pair.
func ReachedAny(ctx context.Context, q DB, alertID, channelID uuid.UUID, eventKeys []string) (bool, error) {
	if len(eventKeys) == 0 {
		return false, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT 1 FROM alert_notification_recipient_deliveries
		WHERE alert_id = $1 AND channel_id = $2 AND event_key = ANY($3)
		LIMIT 1`, alertID, channelID, pq.Array(eventKeys))
	if err != nil {
		if pgerr.IsUndefinedTable(err) {
			return false, nil
		}
		return false, err
	}
	defer rows.Close()
	reached := rows.Next()
	return reached, rows.Err()
}

// Hash is the key the ledger stores for a recipient, so the delivery history
// (shared/notifications/deliverylog) can name a recipient the same way
// without keeping the address.
func Hash(recipient string) string { return hash(recipient) }

func hash(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return hex.EncodeToString(sum[:])
}
