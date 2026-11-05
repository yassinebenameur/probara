package statuspages

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestDefaultStatusPageSettings_IncludeThemeDefaults(t *testing.T) {
	settings := defaultStatusPageSettings()

	if settings.DefaultTheme != "dark" {
		t.Fatalf("DefaultTheme = %q, want dark", settings.DefaultTheme)
	}
	if !settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = false, want true")
	}
}

func TestStatusPageSettingsStored_ApplyPatchUpdatesThemeFields(t *testing.T) {
	light := "light"
	allowToggle := false

	settings := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{
		DefaultTheme:     &light,
		AllowThemeToggle: &allowToggle,
	})

	if settings.DefaultTheme != "light" {
		t.Fatalf("DefaultTheme = %q, want light", settings.DefaultTheme)
	}
	if settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = true, want false")
	}
}

func TestParseStatusPageSettings_ThemeDefaultsRemainBackwardCompatible(t *testing.T) {
	settings := parseStatusPageSettings(nil)
	if settings.DefaultTheme != "dark" {
		t.Fatalf("DefaultTheme = %q, want dark", settings.DefaultTheme)
	}
	if !settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = false, want true")
	}
}

func TestParseStatusPageSettings_ReadsThemeFields(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"default_theme":      "light",
		"allow_theme_toggle": false,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	settings := parseStatusPageSettings(raw)
	if settings.DefaultTheme != "light" {
		t.Fatalf("DefaultTheme = %q, want light", settings.DefaultTheme)
	}
	if settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = true, want false")
	}
}

// TestStatusPageSettingsCSPRoundTrip covers the API's two mirrors of the
// status-page CSP settings (write path: applyPatch -> stored JSON -> parse ->
// toAPI) and pins the stored key names the status-page read path parses
// (status-page/internal/statuspage TestParseStatusPageSettings_CSPFields).
func TestStatusPageSettingsCSPRoundTrip(t *testing.T) {
	mode := " Compatible "
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{
		CSPMode:             &mode,
		AllowedEmbedOrigins: []string{"https://Intranet.Example.com/", "https://intranet.example.com", "https://*.corp.example"},
	})
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if keys["csp_mode"] != "compatible" {
		t.Fatalf("stored csp_mode = %v, want compatible (raw %s)", keys["csp_mode"], raw)
	}
	if origins, _ := keys["allowed_embed_origins"].([]any); len(origins) != 2 {
		t.Fatalf("stored allowed_embed_origins = %v, want 2 normalized origins", keys["allowed_embed_origins"])
	}

	api := parseStatusPageSettings(raw).toAPI()
	if api.CSPMode == nil || *api.CSPMode != "compatible" {
		t.Fatalf("toAPI csp_mode = %v", api.CSPMode)
	}
	if len(api.AllowedEmbedOrigins) != 2 || api.AllowedEmbedOrigins[0] != "https://intranet.example.com" || api.AllowedEmbedOrigins[1] != "https://*.corp.example" {
		t.Fatalf("toAPI allowed_embed_origins = %v", api.AllowedEmbedOrigins)
	}

	// An unrelated patch keeps both; explicit values clear them.
	footer := "hi"
	kept := parseStatusPageSettings(raw).applyPatch(&models.StatusPageSettings{FooterText: &footer})
	if kept.CSPMode != "compatible" || len(kept.AllowedEmbedOrigins) != 2 {
		t.Fatalf("unrelated patch dropped CSP settings: %+v", kept)
	}
	automatic := ""
	cleared := kept.applyPatch(&models.StatusPageSettings{CSPMode: &automatic, AllowedEmbedOrigins: []string{}})
	if cleared.CSPMode != "" || len(cleared.AllowedEmbedOrigins) != 0 {
		t.Fatalf("explicit clear kept values: %+v", cleared)
	}
	clearedRaw, _ := json.Marshal(cleared)
	if back := parseStatusPageSettings(clearedRaw).toAPI(); *back.CSPMode != "" || len(back.AllowedEmbedOrigins) != 0 {
		t.Fatalf("cleared settings came back as %v %v", *back.CSPMode, back.AllowedEmbedOrigins)
	}
}
