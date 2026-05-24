package plugin

import (
	"context"
	"encoding/json"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// Plugin is the contract every alert channel integration implements. Plugins
// live in shared/notifications/plugin/builtin/<type>/ and self-register via
// init().
type Plugin interface {
	// Manifest returns the plugin's self-description. The value must be stable
	// for the lifetime of the process; the registry caches the Type.
	Manifest() Manifest

	// Validate checks a channel config blob against the plugin's expectations.
	// Returns nil if the config is acceptable. The plugin owns its validation
	// rules; the API layer is a thin caller.
	Validate(config json.RawMessage) error

	// Send delivers a single notification. Implementations must be safe to
	// call concurrently and must not retain references to req beyond the call.
	Send(ctx context.Context, req DispatchRequest) error
}

// ChannelRef carries the per-channel context that a plugin needs to send: the
// channel's identity plus its decrypted config. Secrets are decrypted exactly
// once, immediately before Send.
type ChannelRef struct {
	ID     string
	Name   string
	Config map[string]any
}

// RenderedAlert is the shared, channel-agnostic rendering of an alert. Plugins
// that advertise CapabilityRenderedAlert receive a non-nil pointer; plugins
// that only advertise CapabilityRawEvent may receive nil.
type RenderedAlert struct {
	Title    string
	Body     string
	Severity string
	Link     string
	Fields   []KeyValue
}

// KeyValue is a label/value pair used in RenderedAlert.Fields.
type KeyValue struct {
	Key   string
	Value string
}

// DispatchRequest is the payload handed to Plugin.Send.
//
// Attempt is 1-indexed and increments across retries so plugins can adjust
// behavior on re-delivery (e.g. add a "(retry)" prefix or skip non-idempotent
// side effects).
type DispatchRequest struct {
	Channel   ChannelRef
	Event     notifications.AlertEvent
	Rendered  *RenderedAlert
	EventType string
	Attempt   int
}
