// Package pushtext lays out the plain-text body of a mobile push
// notification (ntfy, Gotify, Pushover) from the shared presentation in
// package present. The push plugins carry the title and the deep link in
// their own fields, so the body is the summary, the facts and the last error.
//
// It owns layout only: every word comes from present.Message.
package pushtext

import (
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// Body renders m's summary, facts and last error as plain text of at most
// maxBytes bytes of UTF-8. Push providers reject or truncate an over-long
// body, so optional parts are shrunk to fit: the last error is clipped into
// whatever room is left, then facts are dropped from the end.
func Body(m present.Message, maxBytes int) string {
	var facts []string
	for _, f := range m.Facts() {
		facts = append(facts, f.Label+": "+f.Value)
	}
	for {
		var b strings.Builder
		b.WriteString(m.Summary)
		if len(facts) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(strings.Join(facts, "\n"))
		}
		base := b.String()
		if m.LastError != "" {
			const label = "\n\nLast error: "
			if room := maxBytes - len(base) - len(label); room > 16 {
				return base + label + Clip(m.LastError, room)
			}
		}
		if len(base) <= maxBytes || len(facts) == 0 {
			return Clip(base, maxBytes)
		}
		facts = facts[:len(facts)-1]
	}
}

// Clip bounds s to n bytes of UTF-8, cutting on a rune boundary and marking
// the cut with an ellipsis.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const ellipsis = "…"
	if n < len(ellipsis) {
		return ""
	}
	i := n - len(ellipsis)
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + ellipsis
}
