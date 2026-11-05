package statuspage

import (
	"fmt"
	"html"
	"math"
	"strings"
	"unicode/utf8"
)

// Badge colors, shields.io's flat palette so badges sit well next to others
// in a README.
const (
	badgeGreen  = "#4c1"
	badgeYellow = "#dfb317"
	badgeRed    = "#e05d44"
	badgeBlue   = "#007ec6"
	badgeGrey   = "#9f9f9f"
)

// maxBadgeLabelRunes keeps a long component name from producing a banner.
const maxBadgeLabelRunes = 40

// badgeRanges are the ranges an uptime badge accepts: the visitor ranges.
var badgeRanges = map[string]bool{"1h": true, "24h": true, "7d": true, "30d": true, "90d": true}

// defaultBadgeRange is the uptime badge's range when ?range= is absent.
const defaultBadgeRange = "30d"

// pageStatusBadge is the page-level status value: the worst component state
// wins, so an unattended outage never reads as "operational".
func pageStatusBadge(counts summaryCounts) (string, string) {
	switch {
	case counts.Down > 0:
		return "outage", badgeRed
	case counts.Degraded > 0:
		return "degraded", badgeYellow
	case counts.Unknown > 0:
		return "unknown", badgeGrey
	case counts.Maintenance > 0:
		return "maintenance", badgeBlue
	case counts.Total == 0:
		return "no data", badgeGrey
	default:
		return "operational", badgeGreen
	}
}

func componentStatusBadge(status string) (string, string) {
	switch status {
	case "operational":
		return "operational", badgeGreen
	case "degraded":
		return "degraded", badgeYellow
	case "down":
		return "down", badgeRed
	case "maintenance":
		return "maintenance", badgeBlue
	default:
		return "unknown", badgeGrey
	}
}

// uptimeBadge formats an uptime value with the page's own uptime thresholds
// (toneForUptime in default.gohtml: 99 and 95).
func uptimeBadge(value *float64) (string, string) {
	if value == nil {
		return "no data", badgeGrey
	}
	text := fmt.Sprintf("%.2f%%", *value)
	switch {
	case *value >= 99:
		return text, badgeGreen
	case *value >= 95:
		return text, badgeYellow
	default:
		return text, badgeRed
	}
}

// badgeTextWidth approximates the rendered width of s in 11px Verdana, which
// is all a flat badge needs: no font metrics ship with the service.
func badgeTextWidth(s string) int {
	width := 0.0
	for _, r := range s {
		switch {
		case strings.ContainsRune("iljI.,:;|!'()[] ", r):
			width += 3.9
		case r == 'm' || r == 'w' || r == 'M' || r == 'W' || r == '%':
			width += 10.0
		case r >= 'A' && r <= 'Z':
			width += 7.6
		default:
			width += 6.8
		}
	}
	return int(math.Ceil(width))
}

// renderBadge renders a flat two-part SVG badge. label and value are
// escaped: a component name is tenant-authored text.
func renderBadge(label, value, color string) []byte {
	if utf8.RuneCountInString(label) > maxBadgeLabelRunes {
		label = truncateRunes(label, maxBadgeLabelRunes)
	}
	labelW := badgeTextWidth(label) + 10
	valueW := badgeTextWidth(value) + 10
	total := labelW + valueW
	l := html.EscapeString(label)
	v := html.EscapeString(value)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, total, l, v)
	fmt.Fprintf(&b, `<title>%s: %s</title>`, l, v)
	b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`)
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>`, total)
	fmt.Fprintf(&b, `<g clip-path="url(#r)"><rect width="%d" height="20" fill="#555"/><rect x="%d" width="%d" height="20" fill="%s"/><rect width="%d" height="20" fill="url(#s)"/></g>`, labelW, labelW, valueW, color, total)
	b.WriteString(`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">`)
	lx := labelW / 2
	vx := labelW + valueW/2
	fmt.Fprintf(&b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`, lx, l, lx, l)
	fmt.Fprintf(&b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`, vx, v, vx, v)
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}
