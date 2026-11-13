// Package pagination holds the one clamp rule for page/page_size, so list
// endpoints cannot disagree on what an out-of-range value means.
package pagination

const (
	// DefaultSize and MaxSize are the limits most list endpoints use.
	DefaultSize = 20
	MaxSize     = 100
	// LargeDefaultSize and LargeMaxSize are for lists that are cheap per row
	// and usually read whole (locations, audit log).
	LargeDefaultSize = 50
	LargeMaxSize     = 200
)

// Normalize returns a usable page and page size: a page below 1 becomes 1, a
// size below 1 becomes defaultSize, and a size above maxSize becomes maxSize.
func Normalize(page, size, defaultSize, maxSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}

// Offset is the SQL OFFSET for a normalized page.
func Offset(page, size int) int {
	return (page - 1) * size
}
