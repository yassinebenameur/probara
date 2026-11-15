package notifications

import "time"

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
	// alert_notification_states atomically. The Nats-Msg-Id names that claim
	// (the key plus the claim time), so JetStream never swallows a later,
	// legitimate send of the same event. External delivery is at least once:
	// receivers should honor the key.
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
}

// RecipientKey returns the ledger key for this envelope's event.
func (e DispatchEnvelope) RecipientKey() string {
	if e.DeliveryKey != "" {
		return e.DeliveryKey
	}
	return RecipientEventKey(e.EventType, e.Event.Timestamp)
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

// IdempotencyKey returns the deterministic key the alerter sets as the
// x-idempotency-key NATS header and webhook plugins forward as
// X-Probara-Idempotency-Key, so receivers can dedupe retried deliveries.
func (e DispatchEnvelope) IdempotencyKey() string {
	return e.AlertID + ":" + e.ChannelID + ":" + e.EventType
}
