package statuspages

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// GET …/template/default hands out a built-in source by ?name=: absent is
// the default template, classic the classic one, anything else a 400.
func TestGetDefaultTemplate_Name(t *testing.T) {
	h := NewHandlers(&fakeService{}, logger.New("test", "error"))
	page := "/" + uuid.NewString()
	for _, tc := range []struct {
		query    string
		status   int
		body     string
		filename string
	}{
		{"", http.StatusOK, statustemplate.DefaultSource, "status-page-template.gohtml"},
		{"?name=default", http.StatusOK, statustemplate.DefaultSource, "status-page-template.gohtml"},
		{"?name=classic", http.StatusOK, statustemplate.ClassicSource, "status-page-template-classic.gohtml"},
		{"?name=Classic", http.StatusOK, statustemplate.ClassicSource, "status-page-template-classic.gohtml"},
		{"?name=modern", http.StatusBadRequest, "", ""},
	} {
		w := serve(h.GetDefaultTemplate, http.MethodGet, page+tc.query, "")
		if w.Code != tc.status {
			t.Fatalf("%q: status %d, want %d (%s)", tc.query, w.Code, tc.status, w.Body.String())
		}
		if tc.status != http.StatusOK {
			continue
		}
		if w.Body.String() != tc.body {
			t.Fatalf("%q: wrong template source", tc.query)
		}
		if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="`+tc.filename+`"` {
			t.Fatalf("%q: Content-Disposition %q", tc.query, got)
		}
	}
}
