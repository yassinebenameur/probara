package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/messagestore"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// Chat cards. A plugin advertising plugin.CapabilityMessageUpdate (the Slack
// app) saves a handle on the card it posted for an alert in
// alert_notification_messages, with the state the card shows
// (rendered_state, messagestore.RenderedState). Each lifecycle tick,
// dispatchCardUpdates finds the cards whose state differs from their alert's
// (messagestore.CardStateSQL) and sends them the "updated" event through
// deliverNotification, whose claim (cardUpdateClaimSQL) moves rendered_state
// to the alert's state under the alert row lock. So an acknowledgement,
// unacknowledgement, snooze, snooze end or resolve made from any surface
// reaches the card within a tick, without a one-shot event per transition:
// the reconcile converges on whatever the alert is now.

// cardState is a card the reconcile loaded: the state it shows and the state
// its alert is in.
type cardState struct {
	Rendered string
	Want     string
}

// stale reports whether the card shows another state than its alert's, the
// updated claim's IS DISTINCT FROM.
func (c *cardState) stale() bool { return c.Rendered != c.Want }

// cardUpdateClaimSQL is claimNotificationTx's "updated" claim ($1 alert, $2
// channel, $3 now): it moves the card to the alert's state while the card
// belongs to the alert's current paging episode and shows another state, and
// returns the card's previous write (the round of its ledger and delivery
// key). The alert row is read, not locked, here: deliverNotification already
// holds it FOR SHARE, so the state cannot move until the send is out.
var cardUpdateClaimSQL = `
	UPDATE alert_notification_messages msg
	SET rendered_state = ` + messagestore.CardStateSQL("al") + `, updated_at = $3::timestamptz
	FROM alerts al, alert_notification_messages prev
	WHERE msg.alert_id = $1 AND msg.channel_id = $2
	  AND al.id = msg.alert_id
	  AND prev.alert_id = msg.alert_id AND prev.channel_id = msg.channel_id
	  AND msg.episode = al.relapse_count
	  AND msg.rendered_state IS DISTINCT FROM ` + messagestore.CardStateSQL("al") + `
	RETURNING msg.alert_id, prev.updated_at`

// setCardState copies the alert row's state, read under the claim's lock,
// onto the record an "updated" event is built from, so the card is drawn in
// exactly the state the claim records.
func setCardState(alert *alertRecord, status string, resolvedAt *time.Time, snoozed bool, snoozedUntil sql.NullTime) {
	alert.Status = status
	alert.ResolvedAt = nil
	if status == "resolved" {
		alert.ResolvedAt = resolvedAt
	}
	alert.SnoozedUntil = nil
	if snoozed && snoozedUntil.Valid {
		until := snoozedUntil.Time
		alert.SnoozedUntil = &until
	}
}

// cardUpdateEvent is the "updated" event for alert: the event its card was
// first rendered from (alert.CardEvent), moved to the alert's current state.
// The page's details (locations, metrics, last error) stay as the card first
// showed them; only the state changes.
func cardUpdateEvent(binding policyBinding, alert *alertRecord, now time.Time) notifications.AlertEvent {
	var ev notifications.AlertEvent
	if alert.CardEvent != nil {
		ev = *alert.CardEvent
	} else {
		ev = buildAlertEvent(present.EventCreated, binding, alert, nil, now)
	}
	ev.Type = present.EventUpdated
	ev.TenantID = binding.TenantID.String()
	ev.Timestamp = now
	ev.Alert.ID = alert.ID.String()
	ev.Alert.Status = alert.Status
	if ev.Alert.Status == "" {
		ev.Alert.Status = "active"
	}
	ev.Alert.ResolvedAt = alert.ResolvedAt
	ev.Alert.SnoozedUntil = alert.SnoozedUntil
	ev.Alert.RelapseCount = alert.RelapseCount
	ev.Alert.ReopenedAt = alert.ReopenedAt
	ev.Alert.SuspectedCause = nil
	return ev
}

// cardUpdateBatch bounds one tick's rewrites; the rest follow next tick.
const cardUpdateBatch = 500

// dispatchCardUpdates rewrites every chat card that shows another state than
// its alert's (see the comment at the top of this file). Maintenance,
// suppression, acknowledgement and snooze do not gate it: a card reports the
// alert's state, it pages nobody.
func (a *Alerter) dispatchCardUpdates(ctx context.Context) error {
	var types []string
	for _, m := range plugin.DefaultRegistry.All() {
		if m.HasCapability(plugin.CapabilityMessageUpdate) {
			types = append(types, m.Type)
		}
	}
	if len(types) == 0 {
		return nil
	}

	// rendered_state <> 'resolved' rides the partial index: a card showing
	// the resolve is final for its episode (a reopen posts a new card).
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, COALESCE(m.name, ''), al.kind, al.triggered_at,
			al.failure_count, al.last_error, msg.event, msg.rendered_state, `+messagestore.CardStateSQL("al")+`,
			ac.id, ac.name, ac.type, ac.config, ac.is_active
		FROM alert_notification_messages msg
		JOIN alerts al ON al.id = msg.alert_id
		JOIN alert_channels ac ON ac.id = msg.channel_id
		LEFT JOIN monitors m ON m.id = al.monitor_id
		WHERE msg.rendered_state <> 'resolved'
		  AND msg.episode = al.relapse_count
		  AND msg.rendered_state IS DISTINCT FROM `+messagestore.CardStateSQL("al")+`
		  AND ac.is_active
		  AND ac.type = ANY($1)
		ORDER BY msg.updated_at
		LIMIT $2
	`, pq.Array(types), cardUpdateBatch)
	if err != nil {
		if isUndefinedTableErr(err) {
			// Rollout window before migration 000114.
			return nil
		}
		return fmt.Errorf("query stale chat cards: %w", err)
	}
	defer rows.Close()

	type pending struct {
		binding policyBinding
		record  alertRecord
		channel alertChannel
		state   notificationState
	}
	var todo []pending
	for rows.Next() {
		var p pending
		var monitorID uuid.NullUUID
		var lastError sql.NullString
		var eventJSON, configBytes []byte
		card := &cardState{}
		if err := rows.Scan(&p.record.ID, &p.record.TenantID, &monitorID, &p.binding.MonitorName,
			&p.record.Kind, &p.record.TriggeredAt, &p.record.FailureCount, &lastError,
			&eventJSON, &card.Rendered, &card.Want,
			&p.channel.ID, &p.channel.Name, &p.channel.Type, &configBytes, &p.channel.IsActive); err != nil {
			return fmt.Errorf("scan stale chat card: %w", err)
		}
		if lastError.Valid {
			p.record.LastError = &lastError.String
		}
		if monitorID.Valid {
			p.record.MonitorID = monitorID.UUID
		}
		if len(eventJSON) > 0 {
			var ev notifications.AlertEvent
			if json.Unmarshal(eventJSON, &ev) == nil {
				p.record.CardEvent = &ev
			}
		}
		p.binding.MonitorID = p.record.MonitorID
		p.binding.TenantID = p.record.TenantID
		p.channel.Config = json.RawMessage(configBytes)
		p.state.Card = card
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for i := range todo {
		p := &todo[i]
		if !shouldSendNotification(present.EventUpdated, &p.state, now, 0, DispatchGate{}, false) {
			continue
		}
		a.fireChannel(ctx, present.EventUpdated, p.binding, &p.record, p.channel, now, 0, false)
	}
	return nil
}

func isUndefinedTableErr(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42P01"
}
