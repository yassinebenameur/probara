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

// The read mirror understands csp_mode as the API writes it; an unknown
// value reads as automatic.
func TestParseStatusPageSettings_CSPMode(t *testing.T) {
	for raw, want := range map[string]string{
		`{"csp_mode":"compatible"}`: "compatible",
		`{"csp_mode":"off"}`:        "off",
		`{"csp_mode":"bogus"}`:      "",
		`{}`:                        "",
	} {
		if got := parseStatusPageSettings([]byte(raw)).CSPMode; got != want {
			t.Errorf("%s: CSPMode = %q, want %q", raw, got, want)
		}
	}
}

// The document the API's write mirror stores for the SEO settings (its
// TestStatusPageSettings_SEORoundTrip asserts the same keys and types).
func TestParseStatusPageSettings_SEOFromAPIShape(t *testing.T) {
	settings := parseStatusPageSettings([]byte(`{"show_footer":true,"seo_title":"Acme Cloud status","seo_description":"Uptime of Acme Cloud.","allow_search_indexing":false}`))
	if settings.SEOTitle != "Acme Cloud status" || settings.SEODescription != "Uptime of Acme Cloud." || settings.AllowSearchIndexing {
		t.Fatalf("parsed %+v", settings)
	}
	if !parseStatusPageSettings(nil).AllowSearchIndexing || !parseStatusPageSettings([]byte(`{"show_footer":false}`)).AllowSearchIndexing {
		t.Fatal("pages stored before the setting must stay indexable")
	}
}

// The read mirror of enable_email_subscriptions (the API's write path has the
// matching round-trip test): off by default and for older settings.
func TestParseStatusPageSettings_EmailSubscriptions(t *testing.T) {
	if defaultStatusPageSettings().EnableEmailSubscriptions || parseStatusPageSettings([]byte(`{"show_footer":false}`)).EnableEmailSubscriptions {
		t.Fatal("email subscriptions on by default")
	}
	if !parseStatusPageSettings([]byte(`{"enable_email_subscriptions":true,"auto_publish_incidents":true}`)).EnableEmailSubscriptions {
		t.Fatal("enable_email_subscriptions not read")
	}
}
