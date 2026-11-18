package notifications

import (
	"strconv"
	"time"
)

// DispatchEnvelope is the wire format for async notification dispatch.
//
// Published by the alerter on subject "alerts.dispatch.<plugin_type>" and
// consumed by the worker. The envelope carries everything the worker needs to
// reconstruct a plugin.DispatchRequest WITHOUT trusting the encrypted channel
// config that was loaded by the alerter — the worker re-fetches the channel
// row from Postgres and decrypts it locally. That avoids round-tripping
// plaintext secrets through NATS, which is desirable even though JetStream
// storage is local.
type DispatchEnvelope struct {
	// Schema version. Bump when the envelope shape changes incompatibly.
	V int `json:"v"`

	// ChannelID identifies the alert_channels row the worker must load and
	// decrypt before dispatching.
	ChannelID string `json:"channel_id"`

	// ChannelType is the plugin key (teams, email, slack, ...). Redundant with
	// the NATS subject but kept here so consumers don't have to parse it.
	ChannelType string `json:"channel_type"`

	// AlertID + EventType (with ChannelID) form the idempotency key that rides
	// along to receivers. Deduplication itself happens before publish: the
	// alerter claims the (alert, channel, event) slot in
	// alert_notification_states atomically. The Nats-Msg-Id is the key plus
	// the claimed occurrence (the alerter's dispatchRound), the same when a
	// claim that never committed is retried, so the broker collapses that
	// duplicate, and different for a legitimate second send of the event.
	// External delivery is at least once: receivers should honor the key.
	AlertID   string `json:"alert_id"`
	EventType string `json:"event_type"`

	// Event is the full alert event payload (already serialised by the alerter
	// from the same source of truth used by the synchronous path).
	Event AlertEvent `json:"event"`

	// DeliveryKey names this event in the per-recipient delivery ledger (see
	// RecipientEventKey). The alerter sets it from its claim; an envelope
	// published before the field existed falls back to RecipientKey's
	// derivation, which is stable across redeliveries of one message.
	DeliveryKey string `json:"delivery_key,omitempty"`

	// Release is set on a relapse notice: the claimed row as the alerter's
	// claim found it, which a worker that holds the notice back (the alert
	// was acknowledged or snoozed since) restores, so the route is exactly
	// as if the notice had never been claimed (save a 'resolved' whose
	// recovery notice never reached the channel: see the worker's
	// releaseMutedClaim).
	Release *ClaimRelease `json:"release,omitempty"`
}

// ClaimRelease is a relapse notice's hand-back state
// (DispatchEnvelope.Release).
type ClaimRelease struct {
	// EventType and SentAt are the row's last_event_type and last_sent_at
	// before the relapse claim replaced them.
	EventType string     `json:"event_type"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	// PagedRelapse is the row's paged_relapse before the claim.
	PagedRelapse int `json:"paged_relapse"`
}

// RecipientKey returns the ledger key for this envelope's event.
func (e DispatchEnvelope) RecipientKey() string {
	if e.DeliveryKey != "" {
		return e.DeliveryKey
	}
	return RecipientEventKeyFor(e.EventType, e.Event.Timestamp, e.Event.Alert.RelapseCount)
}

// RecipientEventKey names one event of one (alert, channel) pair in the
// per-recipient delivery ledger (alert_notification_recipient_deliveries).
// Every event type but reminder happens at most once per pair, so its type is
// the key. Reminders repeat, so each round is keyed by round: the
// last_sent_at the reminder claim found, which a retry of the same round
// finds again (a failed sync send rolls the claim back) and the next round
// does not.
//
// The key names one send, not one event type: a deliberate second send of an
// event needs a key of its own, or a plugin skips every recipient the first
// send already reached and the re-send goes nowhere.
func RecipientEventKey(eventType string, round time.Time) string {
	if eventType == "reminder" && !round.IsZero() {
		return "reminder:" + round.UTC().Format(time.RFC3339Nano)
	}
	return eventType
}

// RecipientEventKeyFor is RecipientEventKey within one paging episode of
// the alert: relapse is the alert's relapse_count when the event was claimed.
// A reopened alert sends its page, acknowledgement and resolve again, once
// per episode, so every key but a reminder's (already unique per round)
// carries the episode from the first relapse on. Episode 0 keeps the plain
// key, so ledger entries written before relapses existed still match.
func RecipientEventKeyFor(eventType string, round time.Time, relapse int) string {
	key := RecipientEventKey(eventType, round)
	if relapse > 0 && key == eventType {
		key += ":relapse-" + strconv.Itoa(relapse)
	}
	return key
}

// IdempotencyKey returns the deterministic key the alerter sets as the
// x-idempotency-key NATS header and webhook plugins forward as
// X-Probara-Idempotency-Key, so receivers can dedupe retried deliveries.
func (e DispatchEnvelope) IdempotencyKey() string {
	return IdempotencyKey(e.AlertID, e.ChannelID, e.EventType, e.Event.Alert.RelapseCount)
}

// IdempotencyKey is the receiver-facing key of one event of one alert on one
// channel: "alert:channel:event", plus ":relapse-N" for an event of the
// alert's Nth relapse, whose page and resolve are new events a receiver must
// not discard as retries of the first episode's.
func IdempotencyKey(alertID, channelID, eventType string, relapse int) string {
	key := alertID + ":" + channelID + ":" + eventType
	if relapse > 0 {
		key += ":relapse-" + strconv.Itoa(relapse)
	}
	return key
}
