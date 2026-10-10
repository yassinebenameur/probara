package metricstore

import (
	"fmt"
	"strings"
)

// Unit is the display unit of a metric value. It drives FormatValue and lets
// notification wording and alert rows render "94.2%" or "1.2 GB" instead of
// raw floats. web/lib/metrics.ts mirrors this table for the operator UI.
type Unit int

const (
	UnitUnknown Unit = iota
	UnitRatio        // 0–1, displayed as percent
	UnitBytes
	UnitBytesPerSecond
	UnitSeconds
	UnitCount
)

// Meta is curated display metadata for a metric name.
type Meta struct {
	// Label is the full human label ("Filesystem usage").
	Label string
	// Short is the compact label used in alert subjects ("Filesystem").
	Short string
	Unit  Unit
}

// curated covers the semconv host metrics the shipped collector config
// produces, plus the legacy fixed threshold kinds (cpu|memory|disk|swap) so
// historical host_metric alerts keep rendering after the OTel migration.
var curated = map[string]Meta{
	"system.cpu.utilization":        {Label: "CPU usage", Short: "CPU", Unit: UnitRatio},
	"system.memory.utilization":     {Label: "Memory usage", Short: "Memory", Unit: UnitRatio},
	"system.memory.usage":           {Label: "Memory used", Short: "Memory", Unit: UnitBytes},
	"system.paging.utilization":     {Label: "Swap usage", Short: "Swap", Unit: UnitRatio},
	"system.paging.usage":           {Label: "Swap used", Short: "Swap", Unit: UnitBytes},
	"system.filesystem.utilization": {Label: "Filesystem usage", Short: "Filesystem", Unit: UnitRatio},
	"system.filesystem.usage":       {Label: "Filesystem used", Short: "Filesystem", Unit: UnitBytes},
	"system.cpu.load_average.1m":    {Label: "Load average (1m)", Short: "Load 1m", Unit: UnitCount},
	"system.cpu.load_average.5m":    {Label: "Load average (5m)", Short: "Load 5m", Unit: UnitCount},
	"system.cpu.load_average.15m":   {Label: "Load average (15m)", Short: "Load 15m", Unit: UnitCount},
	"system.network.io":             {Label: "Network I/O", Short: "Network", Unit: UnitBytes},
	"system.disk.io":                {Label: "Disk I/O", Short: "Disk I/O", Unit: UnitBytes},
	"system.uptime":                 {Label: "Uptime", Short: "Uptime", Unit: UnitSeconds},
	"system.processes.count":        {Label: "Processes", Short: "Processes", Unit: UnitCount},
	"system.cpu.logical.count":      {Label: "CPU cores", Short: "Cores", Unit: UnitCount},

	// Legacy fixed-kind alert keys (pre-OTel host_metric alerts).
	"cpu":    {Label: "CPU usage", Short: "CPU", Unit: UnitRatio},
	"memory": {Label: "Memory usage", Short: "Memory", Unit: UnitRatio},
	"disk":   {Label: "Disk usage", Short: "Disk", Unit: UnitRatio},
	"swap":   {Label: "Swap usage", Short: "Swap", Unit: UnitRatio},
}

// Lookup returns curated metadata for a metric name (not a series key —
// parse first). ok=false means an uncurated metric: callers show the raw
// name and format values unitless.
func Lookup(metricName string) (Meta, bool) {
	m, ok := curated[metricName]
	return m, ok
}

// UnitFor resolves the display unit for a metric: curated table first, then
// the OTel unit string recorded on the series ("1" = ratio, "By" = bytes,
// "s" = seconds), else unknown.
func UnitFor(metricName, otelUnit string) Unit {
	if m, ok := curated[metricName]; ok {
		return m.Unit
	}
	switch otelUnit {
	case "1":
		return UnitRatio
	case "By":
		return UnitBytes
	case "By/s":
		return UnitBytesPerSecond
	case "s":
		return UnitSeconds
	default:
		return UnitUnknown
	}
}

// FormatValue renders a value in its display unit: ratios as percent, bytes
// with binary-ish decimal prefixes, seconds as a duration, counts/unknown as
// trimmed decimals.
func FormatValue(v float64, u Unit) string {
	switch u {
	case UnitRatio:
		return trimFloat(v*100, 1) + "%"
	case UnitBytes:
		return formatBytes(v)
	case UnitBytesPerSecond:
		return formatBytes(v) + "/s"
	case UnitSeconds:
		return formatSeconds(v)
	default:
		return trimFloat(v, 2)
	}
}

// DisplayAttr picks the one attribute worth showing next to a series label:
// the mountpoint for filesystem metrics, the direction for I/O metrics, and
// otherwise the first attribute that isn't presentation noise (a bare
// state=used carries no information once the label says "usage").
func DisplayAttr(metricName string, attrs map[string]string) string {
	if v, ok := attrs["mountpoint"]; ok {
		return v
	}
	if v, ok := attrs["direction"]; ok {
		return v
	}
	if v, ok := attrs["device"]; ok {
		return v
	}
	for k, v := range attrs {
		if k != "state" {
			return v
		}
	}
	return ""
}

// legacyPercentKinds are the pre-OTel fixed threshold kinds whose alert
// values were stored as percent (0-100), not ratios. Historical alerts keep
// these names forever; formatting must not re-scale them.
var legacyPercentKinds = map[string]bool{"cpu": true, "memory": true, "disk": true, "swap": true}

// FormatAlertValue renders an alerts.metric_value for display given the
// alert's metric_name (a canonical series key, or a legacy fixed kind).
// Legacy kinds carry percent values as-is; canonical keys carry native units
// (ratio 0-1, bytes, ...) and format through the curated unit table.
func FormatAlertValue(metricNameOrKey string, v float64) string {
	name, _ := ParseSeriesKeyString(metricNameOrKey)
	if legacyPercentKinds[name] {
		return trimFloat(v, 1) + "%"
	}
	return FormatValue(v, UnitFor(name, ""))
}

// AlertLabel renders a human label for an alerts.metric_name (canonical
// series key or legacy kind): the curated short label (or raw metric name)
// plus the one attribute worth showing, e.g. "Filesystem (/data)".
func AlertLabel(metricNameOrKey string) string {
	name, attrs := ParseSeriesKeyString(metricNameOrKey)
	label := name
	if m, ok := Lookup(name); ok {
		label = m.Short
	}
	if attr := DisplayAttr(name, attrs); attr != "" {
		return label + " (" + attr + ")"
	}
	return label
}

func formatBytes(v float64) string {
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return neg + trimFloat(v, 1) + " " + units[i]
}

func formatSeconds(v float64) string {
	s := int64(v)
	switch {
	case s >= 86400:
		return fmt.Sprintf("%dd %dh", s/86400, (s%86400)/3600)
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func trimFloat(v float64, decimals int) string {
	s := fmt.Sprintf("%.*f", decimals, v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
