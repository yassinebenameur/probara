package notifications

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
}

// IdempotencyKey returns the deterministic key the alerter sets as the
// x-idempotency-key NATS header and webhook plugins forward as
// X-Probara-Idempotency-Key, so receivers can dedupe retried deliveries.
func (e DispatchEnvelope) IdempotencyKey() string {
	return e.AlertID + ":" + e.ChannelID + ":" + e.EventType
}
