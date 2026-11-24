package statuspage

import (
	"encoding/xml"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Badge colours: mid-tones that keep white text readable (contrast >= 4.5)
// and stay visible on both light and dark READMEs and dashboards.
const (
	badgeLabelColor = "#454b52"
	badgeGreen      = "#1a7f37"
	badgeAmber      = "#9a6700"
	badgeRed        = "#cf222e"
	badgeBlue       = "#0969da"
	badgeGrey       = "#6e7781"
)

// Badge styles: flat has rounded corners and a soft gradient, square has
// neither. The only style parameter; labels are never caller-supplied, so a
// badge served from the page's host only ever says what the page says.
const (
	badgeStyleFlat   = "flat"
	badgeStyleSquare = "square"
)

// maxBadgeLabelRunes keeps a long component name from producing a banner.
const maxBadgeLabelRunes = 40

// badgeRanges are the ranges an uptime badge accepts: the visitor ranges.
var badgeRanges = map[string]bool{"1h": true, "24h": true, "7d": true, "30d": true, "90d": true}

// defaultBadgeRange is the uptime badge's range when ?range= is absent.
const defaultBadgeRange = "30d"

// uptimeBadgeLabel is a component name and the range, the name shortened
// first so a long name never loses the range (a 7d and a 90d badge must not
// look alike).
func uptimeBadgeLabel(name, rangeName string) string {
	suffix := " " + rangeName
	return truncateRunes(strings.TrimSpace(name), maxBadgeLabelRunes-utf8.RuneCountInString(suffix)) + suffix
}

// pageStatusBadge is the overall badge. Its word follows the page headline
// (summary.status.indicator); an "issues" page names its worst component
// state so a README shows an outage as one.
func pageStatusBadge(status summaryStatus) (string, string) {
	switch status.Indicator {
	case "issues":
		switch {
		case status.Counts.Down > 0:
			return "outage", badgeRed
		case status.Counts.Degraded > 0:
			return "degraded", badgeAmber
		default:
			return "unknown", badgeGrey
		}
	case "maintenance":
		return "maintenance", badgeBlue
	default:
		return "operational", badgeGreen
	}
}

func componentStatusBadge(status string) (string, string) {
	switch status {
	case "operational":
		return "operational", badgeGreen
	case "degraded":
		return "degraded", badgeAmber
	case "down":
		return "down", badgeRed
	case "maintenance":
		return "maintenance", badgeBlue
	default:
		return "unknown", badgeGrey
	}
}

// uptimeBadge formats an uptime figure with the page's own thresholds
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
		return text, badgeAmber
	default:
		return text, badgeRed
	}
}

// verdanaWidths are Verdana's advance widths for printable ASCII in font
// units (2048 per em). The badge is laid out from them server-side and each
// text run carries textLength, so the badge keeps its geometry whichever
// font the viewer substitutes; nothing is fetched.
var verdanaWidths = [95]uint16{
	720, 806, 940, 1716, 1302, 2223, 1486, 550, 909, 909, 1302, 1716, 745, 909, 745, 1302, // ' '..'/'
	1302, 1302, 1302, 1302, 1302, 1302, 1302, 1302, 1302, 1302, 909, 909, 1716, 1716, 1716, 1112, // '0'..'?'
	2048, 1401, 1405, 1430, 1577, 1294, 1178, 1587, 1540, 862, 931, 1416, 1132, 1730, 1525, 1612, // '@'..'O'
	1243, 1612, 1424, 1400, 1241, 1500, 1401, 2025, 1403, 1240, 1402, 909, 1302, 909, 1716, 1302, // 'P'..'_'
	1302, 1229, 1270, 1062, 1270, 1216, 721, 1270, 1295, 562, 692, 1186, 562, 1981, 1295, 1222, // '`'..'o'
	1270, 1270, 873, 1064, 807, 1295, 1186, 1673, 1186, 1186, 1053, 1302, 909, 1302, 1716, // 'p'..'~'
}

const (
	badgeFontSize = 11
	badgeHeight   = 20
	badgePadding  = 6
)

// badgeTextWidth is the width of s in pixels at badgeFontSize. Characters
// outside printable ASCII count as one em, which errs wide (CJK) rather than
// clipping.
func badgeTextWidth(s string) int {
	units := 0
	for _, r := range s {
		if r >= ' ' && r <= '~' {
			units += int(verdanaWidths[r-' '])
		} else {
			units += 2048
		}
	}
	return int(math.Ceil(float64(units) * badgeFontSize / 2048))
}

// escapeBadgeText escapes s for SVG character data and attribute values.
// xml.EscapeText also replaces characters XML cannot carry (NUL, most C0
// controls) with U+FFFD, so tenant-authored names never break the document.
func escapeBadgeText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// renderBadge renders a two-part SVG badge. label is tenant-authored text
// (a component name) and is escaped like value; the document has no script,
// no external reference and no style element.
func renderBadge(label, value, color, style string) []byte {
	label = truncateRunes(strings.TrimSpace(label), maxBadgeLabelRunes)
	if label == "" {
		label = "status"
	}
	labelText := badgeTextWidth(label)
	valueText := badgeTextWidth(value)
	labelW := labelText + 2*badgePadding
	valueW := valueText + 2*badgePadding
	total := labelW + valueW
	l := escapeBadgeText(label)
	v := escapeBadgeText(value)

	radius := 3
	if style == badgeStyleSquare {
		radius = 0
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" role="img" aria-label="%s: %s">`, total, badgeHeight, l, v)
	fmt.Fprintf(&b, `<title>%s: %s</title>`, l, v)
	if style != badgeStyleSquare {
		b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`)
	}
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="%d" rx="%d" fill="#fff"/></clipPath>`, total, badgeHeight, radius)
	fmt.Fprintf(&b, `<g clip-path="url(#r)"><rect width="%d" height="%d" fill="%s"/><rect x="%d" width="%d" height="%d" fill="%s"/>`, labelW, badgeHeight, badgeLabelColor, labelW, valueW, badgeHeight, color)
	if style != badgeStyleSquare {
		fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="url(#s)"/>`, total, badgeHeight)
	}
	b.WriteString(`</g>`)
	fmt.Fprintf(&b, `<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="%d">`, badgeFontSize)
	writeBadgeText(&b, labelW/2, labelText, l)
	writeBadgeText(&b, labelW+valueW/2, valueText, v)
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}

// writeBadgeText writes one centred text run with its drop shadow. text is
// already escaped.
func writeBadgeText(b *strings.Builder, x, width int, text string) {
	fmt.Fprintf(b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3" textLength="%d" lengthAdjust="spacingAndGlyphs">%s</text>`, x, width, text)
	fmt.Fprintf(b, `<text x="%d" y="14" textLength="%d" lengthAdjust="spacingAndGlyphs">%s</text>`, x, width, text)
}
