package plugin

import (
	"context"
	"encoding/json"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/present"
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

// DispatchRequest is the payload handed to Plugin.Send.
//
// Attempt is 1-indexed and increments across retries so plugins can adjust
// behavior on re-delivery (e.g. add a "(retry)" prefix or skip non-idempotent
// side effects).
type DispatchRequest struct {
	Channel   ChannelRef
	Event     notifications.AlertEvent
	EventType string
	Attempt   int
	// Test marks the synthetic event sent by the "test channel" API. Paging
	// plugins resolve what they opened straight away so a test does not leave
	// a real incident behind.
	Test bool
}

// Type returns the event type being delivered: "created", "reminder",
// "resolved" or "acknowledged".
func (r DispatchRequest) Type() string {
	if r.EventType != "" {
		return r.EventType
	}
	if r.Event.Type != "" {
		return r.Event.Type
	}
	return present.EventCreated
}

// View returns the shared presentation of this event — the wording every
// plugin renders, with deep links built from the configured AppBaseURL.
// Plugins own layout only; alert phrasing lives in package present.
func (r DispatchRequest) View() present.Message {
	return present.Build(r.Event, r.Type(), CurrentRuntime().AppBaseURL)
}
