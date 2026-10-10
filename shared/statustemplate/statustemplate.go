// Package statustemplate holds the default public status page template and
// the helpers shared by the status-page renderer and the admin API. The API
// exports DefaultSource so customers can download it as a starting point, and
// validates uploaded template sources with Validate; the renderer parses both
// the default and customer templates through New so every template runs with
// the same function map (and therefore the same contract).
package statustemplate

import (
	_ "embed"
	"fmt"
	"html/template"
	"strings"
)

// DefaultSource is the built-in public status page template. It is the
// template served when a status page has no published custom template, and
// the source handed to customers as the starting point for customization.
//
//go:embed default.gohtml
var DefaultSource string

// ServiceWorkerSource is the push service worker the status-page service
// serves at /public/status/sw.js. It lives here beside the template because
// the two are one unit: the template registers this worker, and the payload
// fields the worker reads are produced by the push sender.
//
// It is the only status page asset that cannot be inlined -- a service worker
// must be fetched from a real URL for its scope to mean anything.
//
//go:embed sw.js
var ServiceWorkerSource string

// MaxSourceSize bounds customer-supplied template sources. The default
// template is ~130KB; 512KB leaves generous room without letting a template
// balloon render cost or DB rows.
const MaxSourceSize = 512 * 1024

// Uptime strip cell counts. These are both defaults baked into the render
// view and the values returned by the template functions below, so custom
// templates draw strips with the same resolution as the built-in one.
const (
	GlobalUptimeStripCells      = 48
	GlobalUptime7dStripCells    = 70
	MonitorUptimeStripCells     = 36
	LongRangeMonitorStripCells  = 60
	LongRangeExpandedStripCells = 90
)

// TypeIcon maps a monitor type to the feather-style icon name used by the
// default template.
func TypeIcon(kind string) string {
	switch kind {
	case "http":
		return "globe"
	case "ping":
		return "wifi"
	case "dns":
		return "search"
	case "grpc":
		return "cpu"
	case "group":
		return "layers"
	case "agent":
		return "monitor"
	case "push":
		return "upload-cloud"
	case "sip":
		return "phone"
	case "synthetic_api":
		return "code"
	case "synthetic_browser":
		return "monitor-check"
	case "redis", "postgres", "mongodb", "rabbitmq":
		return "database"
	default:
		return "activity"
	}
}

// GlobalStripCells returns the number of cells in the page-level uptime strip
// for a range name ("24h", "7d", "30d", "90d").
func GlobalStripCells(rangeName string) int {
	switch rangeName {
	case "7d":
		return GlobalUptime7dStripCells
	case "30d", "90d":
		return LongRangeExpandedStripCells
	default:
		return GlobalUptimeStripCells
	}
}

// MonitorStripCells returns the number of cells in a monitor row's uptime
// strip for a range name.
func MonitorStripCells(rangeName string) int {
	switch rangeName {
	case "30d", "90d":
		return LongRangeMonitorStripCells
	default:
		return MonitorUptimeStripCells
	}
}

// ExpandedStripCells returns the number of cells in a monitor's expanded
// detail uptime strip for a range name.
func ExpandedStripCells(rangeName string) int {
	switch rangeName {
	case "30d", "90d":
		return LongRangeExpandedStripCells
	default:
		return MonitorUptimeStripCells
	}
}

// FuncMap is the complete set of functions available to status page
// templates, custom or default. Adding a function here extends the customer
// template contract; removing or renaming one breaks published templates, so
// treat this map as append-only.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"expandedStripCells": ExpandedStripCells,
		"globalStripCells":   GlobalStripCells,
		"join":               strings.Join,
		"monitorStripCells":  MonitorStripCells,
		"typeIcon":           TypeIcon,
	}
}

// New parses source as a public status page template with the standard
// function map. html/template's contextual autoescaping applies, so template
// authors cannot accidentally break out of HTML/JS/CSS contexts with data
// values; only literal template text is emitted verbatim.
func New(name, source string) (*template.Template, error) {
	return template.New(name).Funcs(FuncMap()).Parse(source)
}

// Validate checks a customer-supplied template source without executing it:
// non-empty, within MaxSourceSize, and parseable with the standard function
// map. Parse errors are returned verbatim (they include the line number) so
// the editor can surface them to the author.
func Validate(source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("template source is empty")
	}
	if len(source) > MaxSourceSize {
		return fmt.Errorf("template source is %d bytes; the maximum is %d", len(source), MaxSourceSize)
	}
	if _, err := New("candidate", source); err != nil {
		return err
	}
	return nil
}
