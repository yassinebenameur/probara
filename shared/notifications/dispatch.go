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
	// alert_notification_states atomically. Publishing also sets Nats-Msg-Id
	// for broker deduplication if a process dies before committing its claim.
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

	// StopOnAck tells the delivery worker that an acknowledgement silences
	// this created/reminder on its route (the tenant stops paging on ack and
	// the route is not notify_after_ack): if the alert is acknowledged by
	// the time the message is delivered — it may wait on a retry backoff —
	// the worker drops it under the alert row lock. Envelopes without it keep
	// the older behaviour (deliver, then re-acknowledge an overtaken trigger).
	StopOnAck bool `json:"stop_on_ack,omitempty"`

	// ContactID, when set, sends this event to one user's verified contact
	// (user_contacts) through the channel instead of the channel's
	// configured recipients; the plugin must advertise
	// plugin.CapabilityDirectRecipient. The envelope never carries the
	// address: the worker resolves it (contacts.LoadVerified) and drops the
	// message when the contact is gone or no longer verified.
	ContactID string `json:"contact_id,omitempty"`
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
// event needs a key of its own (see OvertakenAckKey), or a plugin skips every
// recipient the first send already reached and the re-send goes nowhere.
func RecipientEventKey(eventType string, round time.Time) string {
	if eventType == "reminder" && !round.IsZero() {
		return "reminder:" + round.UTC().Format(time.RFC3339Nano)
	}
	return eventType
}

// OvertakenAckKey is the ledger key of the acknowledgement the delivery
// worker re-sends after a trigger that the original acknowledgement overtook.
// The original went out under "acknowledged" and was a no-op at the provider,
// so the re-send must not inherit its progress; the key is still stable across
// redeliveries of the one trigger message that fires it (created happens once
// per alert and channel).
const OvertakenAckKey = "acknowledged:after-trigger"

// IdempotencyKey returns the deterministic key the alerter sets as the
// x-idempotency-key NATS header and webhook plugins forward as
// X-Probara-Idempotency-Key, so receivers can dedupe retried deliveries.
func (e DispatchEnvelope) IdempotencyKey() string {
	return IdempotencyKeyFor(e.AlertID, e.ChannelID, e.EventType, e.Event.Alert.RelapseCount)
}

// IdempotencyKeyFor is the receiver-facing idempotency key of one event of
// one (alert, channel) pair: "<alert>:<channel>:<event>", plus ":<relapse>"
// once the alert has relapsed. A reopened alert sends created, acknowledged
// and resolved again under the same alert ID, and a receiver deduping on the
// bare key would drop every page of the new episode; episode 0 keeps the
// original format. The webhook plugin and the async envelope both use it.
func IdempotencyKeyFor(alertID, channelID, eventType string, relapseCount int) string {
	key := alertID + ":" + channelID + ":" + eventType
	if relapseCount > 0 {
		key += ":" + strconv.Itoa(relapseCount)
	}
	return key
}

// RecipientEventKeyFor is RecipientEventKey for an alert in paging episode
// relapseCount (AlertDetails.RelapseCount). A reopened alert sends its
// one-per-episode events (created, acknowledged, resolved, and the reopen
// itself, which the alerter claims as "reopened") again, so from the first
// relapse on they carry the episode, e.g. "reopened:1" or "resolved:2":
// under the bare type a fan-out plugin would skip every recipient the
// previous episode's send reached, and the new page would reach nobody.
// Reminder rounds are already unique by their round timestamp.
func RecipientEventKeyFor(eventType string, round time.Time, relapseCount int) string {
	key := RecipientEventKey(eventType, round)
	if relapseCount > 0 && key == eventType {
		key += ":" + strconv.Itoa(relapseCount)
	}
	return key
}

// OvertakenAckKeyFor is OvertakenAckKey in paging episode relapseCount: a
// relapse's trigger can be overtaken by its acknowledgement too, and that
// re-send must not inherit the previous episode's progress.
func OvertakenAckKeyFor(relapseCount int) string {
	if relapseCount > 0 {
		return OvertakenAckKey + ":" + strconv.Itoa(relapseCount)
	}
	return OvertakenAckKey
}
