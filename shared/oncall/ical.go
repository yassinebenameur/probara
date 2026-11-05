package oncall

import (
	"strings"
	"time"
	"unicode/utf8"
)

// CalendarEvent is one VEVENT of an on-call feed.
type CalendarEvent struct {
	UID         string
	Start, End  time.Time
	Summary     string
	Description string
}

// Calendar is an iCalendar (RFC 5545) feed.
type Calendar struct {
	Name   string
	Stamp  time.Time
	Events []CalendarEvent
}

const icalTime = "20060102T150405Z"

// Render returns the feed as text/calendar: CRLF line endings, lines folded
// at 75 octets, text values escaped, times in UTC.
func (c Calendar) Render() string {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(fold(s))
		b.WriteString("\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Probara//On-call schedule//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + escapeText(c.Name))
	line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	line("X-PUBLISHED-TTL:PT1H")
	stamp := c.Stamp.UTC().Format(icalTime)
	for _, e := range c.Events {
		line("BEGIN:VEVENT")
		line("UID:" + escapeText(e.UID))
		line("DTSTAMP:" + stamp)
		line("DTSTART:" + e.Start.UTC().Format(icalTime))
		line("DTEND:" + e.End.UTC().Format(icalTime))
		line("SUMMARY:" + escapeText(e.Summary))
		if e.Description != "" {
			line("DESCRIPTION:" + escapeText(e.Description))
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

// escapeText escapes an RFC 5545 TEXT value and drops other control
// characters, so a schedule or user name cannot inject properties.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fold splits a content line into 75-octet pieces joined by CRLF and a
// space, never inside a UTF-8 sequence.
func fold(s string) string {
	const limit = 75
	if len(s) <= limit {
		return s
	}
	var b strings.Builder
	width := limit
	for len(s) > width {
		cut := width
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		width = limit - 1 // the leading space counts
	}
	b.WriteString(s)
	return b.String()
}
