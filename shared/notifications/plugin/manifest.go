// Package plugin defines the alert channel plugin contract and registry.
//
// Each alert channel integration (Teams, Slack, email, …) ships as a Plugin
// implementation that self-registers in its init() function. The API, alerter,
// and worker import this package to discover and dispatch through plugins
// without knowing about specific channel types.
package plugin

// FieldType is the manifest-declared input type for a config field. The
// frontend renders the matching widget; the backend uses it as a parsing hint.
type FieldType string

const (
	FieldTypeString    FieldType = "string"
	FieldTypeURL       FieldType = "url"
	FieldTypeEmailList FieldType = "email_list"
	FieldTypeTextarea  FieldType = "textarea"
	FieldTypeSecret    FieldType = "secret"
	FieldTypeBool      FieldType = "bool"
)

// Capability is an opt-in feature flag a plugin advertises in its manifest.
// Capabilities are additive — unknown values from a future binary are ignored
// rather than rejected.
type Capability string

const (
	// CapabilityRenderedAlert means the plugin accepts the shared RenderedAlert
	// payload. Most plugins should set this so they get free formatting.
	CapabilityRenderedAlert Capability = "rendered_alert"

	// CapabilityRawEvent means the plugin wants the raw AlertEvent in addition
	// to (or instead of) the rendered form — e.g. Slack blocks or Teams cards
	// that need full event detail.
	CapabilityRawEvent Capability = "raw_event"

	// CapabilityTestable means the plugin's Send is safe to invoke with a
	// synthetic event for the "test channel" API.
	CapabilityTestable Capability = "testable"
)

// Field describes a single config input for a plugin. The frontend renders
// from this; the backend uses Secret to drive encryption at rest.
type Field struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Placeholder string    `json:"placeholder,omitempty"`
	Help        string    `json:"help,omitempty"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required,omitempty"`
	Secret      bool      `json:"secret,omitempty"`
	Default     any       `json:"default,omitempty"`
}

// Manifest is the self-description a plugin returns from Manifest(). It drives
// both the catalog UI and the dynamic config form, and tells the encryption
// layer which fields are secrets.
type Manifest struct {
	Type         string       `json:"type"`
	DisplayName  string       `json:"display_name"`
	Description  string       `json:"description"`
	IconKey      string       `json:"icon_key"`
	DocsURL      string       `json:"docs_url,omitempty"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
	Fields       []Field      `json:"fields"`
}

// HasCapability reports whether the manifest advertises the given capability.
func (m Manifest) HasCapability(c Capability) bool {
	for _, have := range m.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}
