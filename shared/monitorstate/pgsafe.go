package monitorstate

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// Check output is free text from the target (response bodies, WHOIS records,
// protocol reason phrases), and PostgreSQL refuses some of it outright: a
// TEXT column takes no NUL byte or invalid UTF-8, and jsonb also rejects the
// \u0000 escape and unpaired UTF-16 surrogate escapes. One such character
// would fail the insert and the whole result (and the state change it
// carries) with it, redelivered until the queue gives up. insertResult
// therefore scrubs both columns here, for every checker, replacing what
// Postgres refuses with U+FFFD.

// pgSafeText is s with NUL and invalid UTF-8 replaced by U+FFFD (nil stays
// nil).
func pgSafeText(s *string) *string {
	if s == nil || (utf8.ValidString(*s) && !strings.ContainsRune(*s, 0)) {
		return s
	}
	safe := strings.ReplaceAll(strings.ToValidUTF8(*s, "\uFFFD"), "\x00", "\uFFFD")
	return &safe
}

// pgSafeJSON is the JSON document raw with what jsonb input rejects replaced
// by U+FFFD: invalid UTF-8, \u0000, and surrogate escapes that are not a
// high-low pair. Each replaced escape keeps its six bytes (\ufffd), so the
// document's structure is untouched. raw is returned as is when clean.
func pgSafeJSON(raw []byte) []byte {
	if !utf8.Valid(raw) {
		raw = bytes.ToValidUTF8(raw, []byte("\uFFFD"))
	}
	if !bytes.Contains(raw, []byte(`\u`)) {
		return raw
	}
	out := raw
	copied := false
	replace := func(at int) {
		if !copied {
			out = append([]byte(nil), raw...)
			copied = true
		}
		copy(out[at:at+6], `\ufffd`)
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			inString = c == '"'
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			cp, ok := jsonEscapeAt(raw, i)
			if !ok {
				i++ // a two-byte escape such as \" or \\
				continue
			}
			switch {
			case cp == 0, cp >= 0xDC00 && cp <= 0xDFFF:
				replace(i)
			case cp >= 0xD800 && cp <= 0xDBFF:
				if lo, ok := jsonEscapeAt(raw, i+6); ok && lo >= 0xDC00 && lo <= 0xDFFF {
					i += 6 // a valid pair: skip its low half too
				} else {
					replace(i)
				}
			}
			i += 5
		}
	}
	return out
}

// jsonEscapeAt decodes a \uXXXX escape starting at raw[i].
func jsonEscapeAt(raw []byte, i int) (rune, bool) {
	if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
		return 0, false
	}
	var cp rune
	for _, h := range raw[i+2 : i+6] {
		switch {
		case h >= '0' && h <= '9':
			cp = cp<<4 | rune(h-'0')
		case h >= 'a' && h <= 'f':
			cp = cp<<4 | rune(h-'a'+10)
		case h >= 'A' && h <= 'F':
			cp = cp<<4 | rune(h-'A'+10)
		default:
			return 0, false
		}
	}
	return cp, true
}
