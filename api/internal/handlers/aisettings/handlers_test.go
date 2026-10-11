package aisettings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	svc "github.com/yassinebenameur/probara/api/internal/services/aisettings"
	"github.com/yassinebenameur/probara/shared/ai"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

func TestSettingsRejectLegacyDestinationAndCredentials(t *testing.T) {
	for _, body := range []string{`{"enabled":true,"base_url":"http://internal"}`, `{"enabled":true,"api_key":"secret"}`, `{"enabled":true} {"enabled":false}`, `{"provider":"openai"}`} {
		r := httptest.NewRequest("PUT", "/ai-settings", strings.NewReader(body))
		w := httptest.NewRecorder()
		var out svc.UpdateRequest
		if err := decodeSettings(w, r, &out); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	var out svc.UpdateRequest
	r := httptest.NewRequest("PUT", "/ai-settings", strings.NewReader(`{"enabled":false}`))
	if err := decodeSettings(httptest.NewRecorder(), r, &out); err != nil || out.Enabled == nil || *out.Enabled {
		t.Fatal("valid switch rejected")
	}
	out = svc.UpdateRequest{}
	r = httptest.NewRequest("PUT", "/ai-settings", strings.NewReader(`{"auto_triage_enabled":true,"triage_delay_seconds":0,"triage_cooldown_seconds":0,"triage_max_per_hour":1}`))
	if err := decodeSettings(httptest.NewRecorder(), r, &out); err != nil || out.AutoTriageEnabled == nil || !*out.AutoTriageEnabled ||
		out.TriageDelaySeconds == nil || *out.TriageDelaySeconds != 0 || out.TriageMaxPerHour == nil || *out.TriageMaxPerHour != 1 {
		t.Fatalf("triage fields rejected: %+v", out)
	}
}

// putSettings runs UpdateSettings against a service whose database is mock.
func putSettings(t *testing.T, mock func(sqlmock.Sqlmock), body string) *httptest.ResponseRecorder {
	t.Helper()
	sqlDB, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if mock != nil {
		mock(m)
	}
	// Installation-disabled: Get never probes the local service.
	h := NewHandlers(svc.NewService(&shareddb.Client{DB: sqlDB}, ai.Config{Disabled: true}), logger.New("test", "error"))
	r := httptest.NewRequest(http.MethodPut, "/api/v1/ai-settings", strings.NewReader(body))
	r = r.WithContext(ctxpkg.WithTenantID(context.Background(), uuid.NewString()))
	w := httptest.NewRecorder()
	h.UpdateSettings(w, r)
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	return w
}

func TestUpdateSettingsValidation(t *testing.T) {
	for body, want := range map[string]string{
		`{}`:                                 "at least one setting is required",
		`{"triage_delay_seconds":-1}`:        "triage_delay_seconds must be between 0 and 3600",
		`{"triage_delay_seconds":3601}`:      "triage_delay_seconds must be between 0 and 3600",
		`{"triage_cooldown_seconds":-1}`:     "triage_cooldown_seconds must be between 0 and 86400",
		`{"triage_cooldown_seconds":86401}`:  "triage_cooldown_seconds must be between 0 and 86400",
		`{"triage_max_per_hour":0}`:          "triage_max_per_hour must be between 1 and 600",
		`{"triage_max_per_hour":601}`:        "triage_max_per_hour must be between 1 and 600",
		`{"enabled":true,"model":"x"}`:       "Updates accept enabled, auto_triage_enabled",
		`{"enabled":true} {"enabled":false}`: "external provider fields are retired",
		`{"triage_delay_seconds":"60"}`:      "Updates accept enabled",
	} {
		w := putSettings(t, nil, body) // no query expected: rejected before the database
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), want) || !strings.Contains(w.Body.String(), "validation_error") {
			t.Fatalf("%s: body %s, want %q", body, w.Body, want)
		}
	}
}

func TestUpdateSettingsDatabaseErrorIs500(t *testing.T) {
	w := putSettings(t, func(m sqlmock.Sqlmock) {
		m.ExpectExec(regexp.QuoteMeta(`INSERT INTO ai_settings`)).WillReturnError(errors.New("connection reset"))
	}, `{"enabled":false}`)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "connection reset") {
		t.Fatalf("db error: %d %s", w.Code, w.Body)
	}
}

func TestUpdateSettingsEdgesAccepted(t *testing.T) {
	cols := []string{"enabled", "auto_triage_enabled", "triage_delay_seconds", "triage_cooldown_seconds", "triage_max_per_hour"}
	w := putSettings(t, func(m sqlmock.Sqlmock) {
		m.ExpectExec(regexp.QuoteMeta(`INSERT INTO ai_settings`)).
			WithArgs(sqlmock.AnyArg(), nil, true, 0, 86400, 600).
			WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectQuery(regexp.QuoteMeta(`FROM ai_settings WHERE tenant_id = $1`)).
			WillReturnRows(sqlmock.NewRows(cols).AddRow(true, true, 0, 86400, 600))
	}, `{"auto_triage_enabled":true,"triage_delay_seconds":0,"triage_cooldown_seconds":86400,"triage_max_per_hour":600}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got svc.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Installation disabled forces enabled=false; the triage values round-trip.
	if got.Enabled || got.InstallationEnabled || !got.AutoTriageEnabled || got.TriageDelaySeconds != 0 ||
		got.TriageCooldownSeconds != 86400 || got.TriageMaxPerHour != 600 {
		t.Fatalf("settings %+v", got)
	}
}
