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

	// AlertID + EventType combine into the idempotency key used by the worker
	// to deduplicate redeliveries.
	AlertID   string `json:"alert_id"`
	EventType string `json:"event_type"`

	// Event is the full alert event payload (already serialised by the alerter
	// from the same source of truth used by the synchronous path).
	Event AlertEvent `json:"event"`
}

// IdempotencyKey returns the deterministic key the worker writes into the
// NATS message header (x-idempotency-key) and uses to dedupe in
// alert_notification_states.
func (e DispatchEnvelope) IdempotencyKey() string {
	return e.AlertID + ":" + e.ChannelID + ":" + e.EventType
}
