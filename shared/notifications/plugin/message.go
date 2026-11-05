package plugin

import (
	"context"
	"encoding/json"
)

// CapabilityInteractive means the plugin's messages carry controls that act
// on the alert from inside the chat tool (a Slack "Acknowledge" button). The
// provider calls back into the API (for Slack, the public
// /api/v1/integrations/slack/interactions route), which verifies the call and
// acknowledges through the alerts service like any other surface.
const CapabilityInteractive Capability = "interactive"

// CapabilityMessageUpdate means the plugin can edit the message it posted for
// an alert (Slack chat.update). Such a plugin saves its handle on the posted
// card through DispatchRequest.Messages, and the alerter then sends it the
// "updated" event whenever the alert's state (status, snooze) differs from
// the state the card shows: an acknowledgement, unacknowledgement, snooze or
// resolve made anywhere rewrites the card. Follow-ups (reminders, the
// resolve) may go as replies to the card.
const CapabilityMessageUpdate Capability = "message_update"

// MessageStore is the durable handle on the card a plugin posted for one
// alert on one channel (package messagestore, alert_notification_messages).
// It is shared by every send of the alert's current paging episode, across
// retries, replicas and the alerter/worker split, and writes through the
// connection pool.
type MessageStore interface {
	// Load returns the provider reference saved for this alert's card in
	// the current paging episode, or ok=false when there is none.
	Load(ctx context.Context) (ref json.RawMessage, ok bool, err error)
	// Save records ref as this alert's card, rendered from the event being
	// sent. Call it right after the provider accepted the post. It is a
	// no-op on the "updated" event, which edits a card that is already
	// saved (and whose row the alerter holds locked while it sends).
	Save(ctx context.Context, ref json.RawMessage) error
}
