// Package messagestore is the Postgres-backed plugin.MessageStore: the card a
// two-way chat channel (plugin.CapabilityMessageUpdate) posted for an alert,
// kept in alert_notification_messages (migration 000114), and the card state
// rule the alerter's "updated" claim reconciles it against.
//
// Like package recipientledger it is written through the connection pool,
// never the caller's claim or alert-lock transaction: the card was posted
// whether or not that transaction later commits, and the handle on it must
// survive a rollback. Every send of the alert can land in a different
// process (alerter replicas, delivery workers), so the handle lives in the
// database they share.
package messagestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// StateResolved is the card state of a resolved alert. A card in it is final
// for its paging episode.
const StateResolved = "resolved"

// saveTimeout bounds a Save. It runs on a context detached from the dispatch
// deadline: a card the provider accepted just before that deadline must still
// be recorded, or the alert's later state changes could never reach it.
const saveTimeout = 5 * time.Second

// CardState is the state an alert card shows: "resolved", or the alert's
// status ("active", "acknowledged") with ":snoozed:<unix seconds>" appended
// while it is snoozed until snoozedUntil (nil when not snoozed). The alerter
// rewrites a card whenever its stored state differs from CardStateSQL, so
// the two must format identically (TestCardStateSQLMatchesGo).
func CardState(status string, snoozedUntil *time.Time) string {
	if status == StateResolved {
		return StateResolved
	}
	if status == "" {
		status = "active"
	}
	if snoozedUntil != nil {
		return status + ":snoozed:" + strconv.FormatInt(snoozedUntil.Unix(), 10)
	}
	return status
}

// CardStateSQL is CardState over the alerts row aliased by alertAlias, as of
// the statement's NOW(): the state a card of that alert should show.
func CardStateSQL(alertAlias string) string {
	a := alertAlias
	return `(CASE WHEN ` + a + `.status = 'resolved' THEN 'resolved'
		WHEN ` + alertrouting.SnoozedPredicate(a) + `
			THEN ` + a + `.status::text || ':snoozed:' || floor(extract(epoch FROM ` + a + `.snoozed_until))::bigint::text
		ELSE ` + a + `.status::text END)`
}

// RenderedState is the CardState a card rendered from event shows. The
// event's own status is what the plugin drew, which may already be behind
// the alert (an async send waiting on a retry): the reconcile then sees the
// difference and rewrites the card.
func RenderedState(event notifications.AlertEvent) string {
	status := event.Alert.Status
	if event.Type == StateResolved {
		status = StateResolved
	}
	return CardState(status, event.Alert.SnoozedUntil)
}

// DB is what the store needs from a connection pool. It must be the pool,
// never the caller's claim or lock transaction.
type DB interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// For returns the card store of one alert on one channel, for a send of
// eventType rendering event. The episode is event.Alert.RelapseCount. A nil db
// yields nil, which plugins treat as untracked.
func For(db DB, alertID, channelID uuid.UUID, eventType string, event notifications.AlertEvent) plugin.MessageStore {
	if db == nil {
		return nil
	}
	return &store{db: db, alertID: alertID, channelID: channelID, eventType: eventType, event: event}
}

type store struct {
	db        DB
	alertID   uuid.UUID
	channelID uuid.UUID
	eventType string
	event     notifications.AlertEvent
}

func (s *store) Load(ctx context.Context) (json.RawMessage, bool, error) {
	var ref []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT provider_ref
		FROM alert_notification_messages
		WHERE alert_id = $1 AND channel_id = $2 AND episode = $3`,
		s.alertID, s.channelID, s.event.Alert.RelapseCount).Scan(&ref)
	switch {
	case errors.Is(err, sql.ErrNoRows), isUndefinedTable(err):
		// Rollout window before migration 000114: nothing can be saved.
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return json.RawMessage(ref), true, nil
}

func (s *store) Save(ctx context.Context, ref json.RawMessage) error {
	if s.eventType == present.EventUpdated {
		// The updated claim holds this row locked while the card is
		// rewritten; the card's reference does not change.
		return nil
	}
	if len(ref) == 0 {
		return errors.New("empty message reference")
	}
	eventJSON, err := json.Marshal(s.event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	// The join keeps a row from pairing an alert with another tenant's
	// channel, whatever the caller passes. A card of an earlier episode
	// never replaces the current one.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO alert_notification_messages (
			alert_id, channel_id, episode, provider_ref, event, rendered_state, created_at, updated_at)
		SELECT a.id, c.id, $3, $4, $5, $6, NOW(), NOW()
		FROM alerts a
		JOIN alert_channels c ON c.id = $2 AND c.tenant_id = a.tenant_id
		WHERE a.id = $1
		ON CONFLICT (alert_id, channel_id) DO UPDATE
		SET episode = EXCLUDED.episode, provider_ref = EXCLUDED.provider_ref,
			event = EXCLUDED.event, rendered_state = EXCLUDED.rendered_state,
			updated_at = NOW()
		WHERE alert_notification_messages.episode <= EXCLUDED.episode`,
		s.alertID, s.channelID, s.event.Alert.RelapseCount, []byte(ref), eventJSON, RenderedState(s.event))
	if isUndefinedTable(err) {
		return nil
	}
	return err
}

func isUndefinedTable(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42P01"
}
