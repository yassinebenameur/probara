package aisettings

import (
	svc "github.com/yassinebenameur/probara/api/internal/services/aisettings"
	"net/http/httptest"
	"strings"
	"testing"
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
}
