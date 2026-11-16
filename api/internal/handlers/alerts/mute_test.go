package alerts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// fakeMuteService answers the mute calls with err, or the alert in its new
// state; it records the snooze end it was given.
type fakeMuteService struct {
	alertservice.AlertService
	err       error
	gotUntil  time.Time
	bulkCalls int
}

func (f *fakeMuteService) alert(id uuid.UUID) (*models.AlertWithDetails, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &models.AlertWithDetails{Alert: models.Alert{ID: id, Status: models.AlertStatusActive}}, nil
}

func (f *fakeMuteService) UnacknowledgeAlert(_ context.Context, _, id uuid.UUID) (*models.AlertWithDetails, error) {
	return f.alert(id)
}

func (f *fakeMuteService) SnoozeAlert(_ context.Context, _, id uuid.UUID, until time.Time) (*models.AlertWithDetails, error) {
	f.gotUntil = until
	return f.alert(id)
}

func (f *fakeMuteService) UnsnoozeAlert(_ context.Context, _, id uuid.UUID) (*models.AlertWithDetails, error) {
	return f.alert(id)
}

func (f *fakeMuteService) BulkSnooze(_ context.Context, _ uuid.UUID, ids []uuid.UUID, until time.Time) (*alertservice.BulkResult, error) {
	f.bulkCalls++
	f.gotUntil = until
	return &alertservice.BulkResult{Items: []models.AlertWithDetails{}, Failed: []alertservice.BulkFailure{}}, nil
}

func serveMute(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Post("/{id}", h)
	r := httptest.NewRequest(http.MethodPost, "/"+uuid.NewString(), strings.NewReader(body))
	r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// A snooze takes exactly one of until or duration_minutes, ending 1 minute to
// 7 days from now; anything else is a 400 that never reaches the service.
func TestSnoozeAlert_Body(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	tooFar := time.Now().Add(8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"one hour preset", `{"duration_minutes": 60}`, http.StatusOK},
		{"custom end", `{"until": "` + future + `"}`, http.StatusOK},
		{"neither", `{}`, http.StatusBadRequest},
		{"both", `{"duration_minutes": 60, "until": "` + future + `"}`, http.StatusBadRequest},
		{"zero minutes", `{"duration_minutes": 0}`, http.StatusBadRequest},
		{"in the past", `{"until": "` + past + `"}`, http.StatusBadRequest},
		{"beyond seven days", `{"until": "` + tooFar + `"}`, http.StatusBadRequest},
		{"malformed", `{"until": "tomorrow"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeMuteService{}
			h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error"))
			w := serveMute(h.SnoozeAlert, tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK && svc.gotUntil.IsZero() {
				t.Fatal("the service never got the snooze end")
			}
			if tc.want != http.StatusOK && !svc.gotUntil.IsZero() {
				t.Fatal("an invalid snooze reached the service")
			}
		})
	}
}

// Each action answers 404 when the alert is not in a state it applies to,
// like acknowledge and resolve, and 500 on anything else.
func TestMuteActions_ErrorsMapToStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*Handlers) http.HandlerFunc
		body    string
		err     error
		want    int
	}{
		{"unacknowledge active", func(h *Handlers) http.HandlerFunc { return h.UnacknowledgeAlert }, "", alertservice.ErrAlertNotAcknowledged, http.StatusNotFound},
		{"snooze resolved", func(h *Handlers) http.HandlerFunc { return h.SnoozeAlert }, `{"duration_minutes": 60}`, alertservice.ErrAlertNotOpen, http.StatusNotFound},
		{"unsnooze not snoozed", func(h *Handlers) http.HandlerFunc { return h.UnsnoozeAlert }, "", alertservice.ErrAlertNotSnoozed, http.StatusNotFound},
		{"unacknowledge db down", func(h *Handlers) http.HandlerFunc { return h.UnacknowledgeAlert }, "", context.DeadlineExceeded, http.StatusInternalServerError},
		{"unacknowledge ok", func(h *Handlers) http.HandlerFunc { return h.UnacknowledgeAlert }, "", nil, http.StatusOK},
		{"unsnooze ok", func(h *Handlers) http.HandlerFunc { return h.UnsnoozeAlert }, "", nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(&fakeMuteService{err: tc.err}, alertservice.NewHub(), logger.New("test", "error"))
			if w := serveMute(tc.handler(h), tc.body); w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// The bulk snooze validates the end before touching any alert.
func TestBulkSnooze_ValidatesEnd(t *testing.T) {
	tenantID := uuid.New()
	for body, want := range map[string]int{
		`{"ids": ["` + uuid.NewString() + `"], "duration_minutes": 240}`:   http.StatusOK,
		`{"ids": ["` + uuid.NewString() + `"]}`:                            http.StatusBadRequest,
		`{"ids": ["` + uuid.NewString() + `"], "duration_minutes": 20000}`: http.StatusBadRequest,
	} {
		svc := &fakeMuteService{}
		h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error"))
		w := httptest.NewRecorder()
		h.BulkSnoozeAlerts(w, bulkRequest(t, tenantID, "/api/v1/alerts/bulk/snooze", body))
		if w.Code != want {
			t.Fatalf("%s: status = %d, want %d: %s", body, w.Code, want, w.Body.String())
		}
		if (svc.bulkCalls == 1) != (want == http.StatusOK) {
			t.Fatalf("%s: service calls = %d", body, svc.bulkCalls)
		}
	}
}
