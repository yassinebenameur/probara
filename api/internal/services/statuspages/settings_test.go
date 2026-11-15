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

// csp_mode survives the write path: applyPatch stores it, the JSON keeps
// it, toAPI returns it, and an absent patch field leaves it alone. The
// status-page read mirror has the matching test (status-page settings_test).
func TestStatusPageSettings_CSPModeRoundTrip(t *testing.T) {
	strict := "Strict"
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{CSPMode: &strict})
	if stored.CSPMode != "strict" {
		t.Fatalf("CSPMode = %q, want strict", stored.CSPMode)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	reread := parseStatusPageSettings(raw)
	if got := reread.toAPI().CSPMode; got == nil || *got != "strict" {
		t.Fatalf("toAPI CSPMode = %v, want strict", got)
	}
	if kept := reread.applyPatch(&models.StatusPageSettings{}); kept.CSPMode != "strict" {
		t.Fatalf("an absent csp_mode cleared the stored one: %q", kept.CSPMode)
	}
	automatic := ""
	if cleared := reread.applyPatch(&models.StatusPageSettings{CSPMode: &automatic}); cleared.CSPMode != "" {
		t.Fatalf("csp_mode \"\" must reset to automatic, got %q", cleared.CSPMode)
	}
}
