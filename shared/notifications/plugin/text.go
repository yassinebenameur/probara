package plugin

// Truncate shortens s to at most n characters, replacing the tail with an
// ellipsis when it had to cut. It counts runes, not bytes, so a multi-byte
// character is never split; providers reject or mangle invalid UTF-8.
// Channel plugins use it for every provider field with a length limit.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
