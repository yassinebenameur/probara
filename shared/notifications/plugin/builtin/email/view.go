package email

import (
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// Brand tokens, shared with the operator UI (web/components/ui/BrandMark.tsx)
// and status pages (shared/statustemplate/default.gohtml). Email clients cannot
// use CSS variables reliably, so the values are duplicated here as constants and
// interpolated inline.
const (
	brandOrange = "#ff5a24"
	brandInk    = "#140a05"
)

// tone is the status colour triple used by one alert email: a saturated accent
// for bars and numbers, a pale tint for callout backgrounds, and a darker text
// shade that stays legible on that tint. Values are the light-theme status
// colours from the status-page palette, darkened where needed for contrast on
// white.
type tone struct {
	Accent string
	Tint   string
	OnTint string
	Label  string // short status word, e.g. "DOWN"
}

var (
	toneDown = tone{Accent: "#da1b69", Tint: "#fdeff4", OnTint: "#a11350", Label: "DOWN"}
	toneUp   = tone{Accent: "#00a35f", Tint: "#ecfaf3", OnTint: "#007845", Label: "RECOVERED"}
	toneWarn = tone{Accent: "#d38d00", Tint: "#fdf6e8", OnTint: "#946200", Label: "WARNING"}
	toneInfo = tone{Accent: "#2563eb", Tint: "#eef3fd", OnTint: "#1d4ed8", Label: "INFO"}
)

// alertView is the presentation model for one alert email: the shared,
// channel-agnostic wording from package present plus the email-only palette
// and inbox preview. Both the HTML template and the plain-text body render
// from it, so the two parts of a multipart message can never drift apart.
// Wording belongs in package present, never here or in the template.
type alertView struct {
	present.Message
	Tone      tone
	Preheader string // inbox preview text
}

// newAlertView resolves an event into the presentation model. appBaseURL is the
// public operator-UI origin; empty disables the call-to-action button.
func newAlertView(event notifications.AlertEvent, appBaseURL string) alertView {
	msg := present.Build(event, event.Type, appBaseURL)
	v := alertView{Message: msg, Preheader: msg.Summary}
	switch msg.Tone {
	case present.ToneUp:
		v.Tone = toneUp
	case present.ToneWarn:
		v.Tone = toneWarn
	case present.ToneInfo:
		v.Tone = toneInfo
	default:
		v.Tone = toneDown
	}
	v.Tone.Label = msg.StatusWord
	return v
}

// DefaultLabel is the kind- and event-aware headline label, e.g.
// "Alert Triggered" or "CPU Usage High". Shared by the subject line and the
// email header so they always agree.
func DefaultLabel(event notifications.AlertEvent) string {
	return present.Label(event)
}

func humanTime(t time.Time) string { return present.HumanTime(t) }
