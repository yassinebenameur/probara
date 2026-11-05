package statuspage

import (
	"encoding/json"
	"testing"
)

func TestDefaultStatusPageSettings_ThemeDefaults(t *testing.T) {
	settings := defaultStatusPageSettings()

	if settings.DefaultTheme != "dark" {
		t.Fatalf("DefaultTheme = %q, want dark", settings.DefaultTheme)
	}
	if !settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = false, want true")
	}
}

func TestParseStatusPageSettings_ThemeFields(t *testing.T) {
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

func TestParseStatusPageSettings_BackwardCompatibleThemeDefaults(t *testing.T) {
	settings := parseStatusPageSettings(nil)

	if settings.DefaultTheme != "dark" {
		t.Fatalf("DefaultTheme = %q, want dark", settings.DefaultTheme)
	}
	if !settings.AllowThemeToggle {
		t.Fatalf("AllowThemeToggle = false, want true")
	}
}

func TestParseStatusPageSettings_PushNotificationsDefaultOff(t *testing.T) {
	// Enabling push makes the page ask visitors for a browser permission, so
	// it must never turn itself on for pages that predate the setting.
	if defaultStatusPageSettings().EnablePushNotifications {
		t.Fatalf("EnablePushNotifications = true by default, want false")
	}
	if parseStatusPageSettings(nil).EnablePushNotifications {
		t.Fatalf("EnablePushNotifications = true for absent settings, want false")
	}
	if parseStatusPageSettings([]byte(`{"show_footer":false}`)).EnablePushNotifications {
		t.Fatalf("EnablePushNotifications = true for unrelated settings, want false")
	}
}

func TestParseStatusPageSettings_PushNotificationsRoundTrip(t *testing.T) {
	for _, want := range []bool{true, false} {
		raw, err := json.Marshal(map[string]any{"enable_push_notifications": want})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if got := parseStatusPageSettings(raw).EnablePushNotifications; got != want {
			t.Fatalf("EnablePushNotifications = %v, want %v", got, want)
		}
	}
}

// The API writes csp_mode and allowed_embed_origins under exactly these keys
// (api/internal/services/statuspages, TestStatusPageSettingsCSPRoundTrip);
// this is the read half of that three-mirror round trip.
func TestParseStatusPageSettings_CSPFields(t *testing.T) {
	settings := parseStatusPageSettings([]byte(`{"csp_mode":"compatible","allowed_embed_origins":["https://intranet.example.com","https://*.corp.example"]}`))
	if settings.CSPMode != "compatible" {
		t.Fatalf("CSPMode = %q, want compatible", settings.CSPMode)
	}
	if len(settings.AllowedEmbedOrigins) != 2 || settings.AllowedEmbedOrigins[1] != "https://*.corp.example" {
		t.Fatalf("AllowedEmbedOrigins = %v", settings.AllowedEmbedOrigins)
	}

	defaults := parseStatusPageSettings([]byte(`{"show_footer":false}`))
	if defaults.CSPMode != "" || defaults.AllowedEmbedOrigins != nil {
		t.Fatalf("absent CSP settings must read as automatic with no embed origins: %+v", defaults)
	}
	if got := parseStatusPageSettings([]byte(`{"csp_mode":"nonsense"}`)).CSPMode; got != "" {
		t.Fatalf("unknown csp_mode = %q, want automatic", got)
	}
}

// Read half of the seo_description three-mirror round trip (write half:
// api/internal/services/statuspages TestStatusPageSettingsSEODescriptionRoundTrip).
func TestParseStatusPageSettings_SEODescription(t *testing.T) {
	if got := parseStatusPageSettings([]byte(`{"seo_description":"  Acme status  "}`)).SEODescription; got != "Acme status" {
		t.Fatalf("SEODescription = %q, want %q", got, "Acme status")
	}
	if got := parseStatusPageSettings([]byte(`{"show_footer":false}`)).SEODescription; got != "" {
		t.Fatalf("absent seo_description = %q, want empty", got)
	}
}
