package worker

// truncateRunes keeps the first limit characters of s and marks a cut with an
// ellipsis. It cuts on rune boundaries: these strings come from remote peers
// (SIP headers, database error text) and end up in check results as JSON, and
// a byte-offset cut through a multi-byte character would store invalid UTF-8.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit { // fewer bytes than the limit means fewer runes too
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
