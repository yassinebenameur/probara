package statuspage

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yassinebenameur/probara/shared/logger"
)

func TestWriteStatusPageError(t *testing.T) {
	h := NewHandlers(nil, nil, logger.New("status-page-test", "error"), nil, nil, nil)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"sentinel", ErrStatusPageNotFound, http.StatusNotFound},
		{"wrapped sentinel", fmt.Errorf("load page: %w", ErrStatusPageNotFound), http.StatusNotFound},
		{"other", errors.New("connection refused"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.writeStatusPageError(rec, "demo", tc.err, "Failed in test")
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

func TestSlugFromPath(t *testing.T) {
	cases := []struct {
		path, suffix, want string
	}{
		{"/public/status/demo/data", "/data", "demo"},
		{"/public/status/demo/stream", "/stream", "demo"},
		{"/public/status/demo", "/data", "demo"},
		{"/public/status/", "/data", ""},
		{"/public/status//data", "/data", ""},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := slugFromPath(r, tc.suffix); got != tc.want {
			t.Errorf("slugFromPath(%q, %q) = %q, want %q", tc.path, tc.suffix, got, tc.want)
		}
	}
}
