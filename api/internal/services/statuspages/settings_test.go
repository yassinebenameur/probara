package statuspages

import (
	"encoding/json"
	"errors"
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

// The SEO settings survive the write path, and the stored JSON is exactly
// what the status-page read mirror parses (its
// TestParseStatusPageSettings_SEOFromAPIShape uses the same document).
func TestStatusPageSettings_SEORoundTrip(t *testing.T) {
	if !defaultStatusPageSettings().AllowSearchIndexing || !parseStatusPageSettings([]byte(`{"show_footer":true}`)).AllowSearchIndexing {
		t.Fatal("pages, including ones stored before the setting, must default to indexable")
	}

	title, description, off := "  Acme Cloud status ", "Uptime of Acme Cloud.", false
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{
		SEOTitle: &title, SEODescription: &description, AllowSearchIndexing: &off,
	})
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if keys["seo_title"] != "Acme Cloud status" || keys["seo_description"] != "Uptime of Acme Cloud." || keys["allow_search_indexing"] != false {
		t.Fatalf("stored JSON = %s", raw)
	}

	api := parseStatusPageSettings(raw).toAPI()
	if api.SEOTitle == nil || *api.SEOTitle != "Acme Cloud status" || api.SEODescription == nil || *api.SEODescription != description {
		t.Fatalf("toAPI SEO = %v %v", api.SEOTitle, api.SEODescription)
	}
	if api.AllowSearchIndexing == nil || *api.AllowSearchIndexing {
		t.Fatalf("toAPI AllowSearchIndexing = %v, want false", api.AllowSearchIndexing)
	}

	reread := parseStatusPageSettings(raw)
	if kept := reread.applyPatch(&models.StatusPageSettings{}); kept.SEOTitle == nil || kept.AllowSearchIndexing {
		t.Fatal("an absent field changed a stored SEO setting")
	}
	empty := ""
	if cleared := reread.applyPatch(&models.StatusPageSettings{SEOTitle: &empty, SEODescription: &empty}); cleared.SEOTitle != nil || cleared.SEODescription != nil {
		t.Fatal(`"" must clear an SEO override`)
	}
}

// enable_email_subscriptions and auto_publish_incidents survive the write
// path: off by default, stored by applyPatch, kept by the JSON, returned by
// toAPI, and left alone by a patch that omits them. The status-page read
// mirror has the matching test for enable_email_subscriptions (the only one
// it renders).
func TestStatusPageSettings_SubscriberSettingsRoundTrip(t *testing.T) {
	defaults := defaultStatusPageSettings().toAPI()
	if *defaults.EnableEmailSubscriptions || *defaults.AutoPublishIncidents {
		t.Fatal("subscriber settings are on by default")
	}
	on := true
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{EnableEmailSubscriptions: &on, AutoPublishIncidents: &on})
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	reread := parseStatusPageSettings(raw)
	if got := reread.toAPI(); !*got.EnableEmailSubscriptions || !*got.AutoPublishIncidents {
		t.Fatalf("round trip lost a setting: %s", raw)
	}
	footer := false
	kept := reread.applyPatch(&models.StatusPageSettings{ShowFooter: &footer})
	if !kept.EnableEmailSubscriptions || !kept.AutoPublishIncidents {
		t.Fatal("a patch without the settings cleared them")
	}
}

// Turning subscriptions on is refused while mail is not configured (or the
// check was never installed); a page already on is not.
func TestCheckSubscriptionsEnable(t *testing.T) {
	on, off := true, false
	s := &Service{}
	if err := s.checkSubscriptionsEnable(false, &models.StatusPageSettings{EnableEmailSubscriptions: &on}); err == nil {
		t.Fatal("enabled with no mail check installed")
	}
	s.ConfigureSubscriberMail(func() []string { return []string{"SMTP is not configured."} })
	err := s.checkSubscriptionsEnable(false, &models.StatusPageSettings{EnableEmailSubscriptions: &on})
	if err == nil || !errors.Is(err, ErrSubscriptionsUnavailable) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range []struct {
		was   bool
		patch *models.StatusPageSettings
	}{
		{true, &models.StatusPageSettings{EnableEmailSubscriptions: &on}},
		{false, &models.StatusPageSettings{EnableEmailSubscriptions: &off}},
		{false, &models.StatusPageSettings{}},
		{false, nil},
	} {
		if err := s.checkSubscriptionsEnable(c.was, c.patch); err != nil {
			t.Fatalf("was %v: %v", c.was, err)
		}
	}
	s.ConfigureSubscriberMail(func() []string { return nil })
	if err := s.checkSubscriptionsEnable(false, &models.StatusPageSettings{EnableEmailSubscriptions: &on}); err != nil {
		t.Fatalf("refused with mail ready: %v", err)
	}
}

// template survives the write path: "default" for a page that never set
// it, stored by applyPatch, kept by the JSON, returned by toAPI, left alone
// by a patch that omits it, and the stored JSON is what the status-page read
// mirror parses (its TestParseStatusPageSettings_Template).
func TestStatusPageSettings_TemplateRoundTrip(t *testing.T) {
	if got := defaultStatusPageSettings().toAPI().Template; got == nil || *got != "default" {
		t.Fatalf("default toAPI Template = %v, want default", got)
	}
	if got := parseStatusPageSettings([]byte(`{"show_footer":true}`)).toAPI().Template; got == nil || *got != "default" {
		t.Fatalf("a page stored before the setting must read as default, got %v", got)
	}

	classic := "Classic"
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{Template: &classic})
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if keys["template"] != "classic" {
		t.Fatalf("stored JSON = %s", raw)
	}
	reread := parseStatusPageSettings(raw)
	if got := reread.toAPI().Template; got == nil || *got != "classic" {
		t.Fatalf("toAPI Template = %v, want classic", got)
	}
	if kept := reread.applyPatch(&models.StatusPageSettings{}); kept.Template != "classic" {
		t.Fatalf("an absent template changed the stored one: %q", kept.Template)
	}
	back := "default"
	if reset := reread.applyPatch(&models.StatusPageSettings{Template: &back}); reset.Template != "default" {
		t.Fatalf("template default = %q", reset.Template)
	}
}
