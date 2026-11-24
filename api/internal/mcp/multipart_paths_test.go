package mcp

import (
	"slices"
	"testing"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
)

// TestMultipartUploadPathsMatchCatalog: a route the catalog uploads as
// multipart must be on the auth middleware's list of routes whose
// cookie-authenticated writes may carry a multipart body, and the list names
// nothing else, or the browser's upload of that file is answered 415.
func TestMultipartUploadPathsMatchCatalog(t *testing.T) {
	var want []string
	for _, s := range catalog() {
		if s.Multipart != nil {
			want = append(want, apiPrefix+s.Route)
		}
	}
	got := slices.Clone(apimiddleware.MultipartUploadPaths)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("multipart routes in the catalog %v, middleware.MultipartUploadPaths %v", want, got)
	}
}
