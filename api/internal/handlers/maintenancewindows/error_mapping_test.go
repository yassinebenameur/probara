package maintenancewindows

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/apperr"
	maintenancewindowservice "github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	"github.com/yassinebenameur/probara/shared/logger"
)

func TestServiceErrorsMapToStatus(t *testing.T) {
	h := NewHandlers(nil, logger.New("test", "error"))
	for _, tc := range []struct {
		err     error
		want    int
		message string
	}{
		{maintenancewindowservice.ErrNotFound, http.StatusNotFound, "maintenance window not found"},
		// A window naming a monitor the tenant does not have is a bad
		// request here, not a 404: the window itself is the resource.
		{apperr.ErrMonitorNotFound, http.StatusBadRequest, "monitor not found"},
		{errors.New("title is required"), http.StatusBadRequest, "title is required"},
		{fmt.Errorf("database is down"), http.StatusInternalServerError, "failed to save window"},
	} {
		w := httptest.NewRecorder()
		h.writeServiceError(w, httptest.NewRequest(http.MethodPost, "/", nil), tc.err, "save window")
		if w.Code != tc.want || !strings.Contains(w.Body.String(), `"message":"`+tc.message+`"`) {
			t.Errorf("%v: status = %d body = %s; want %d %q", tc.err, w.Code, w.Body.String(), tc.want, tc.message)
		}
	}
}
