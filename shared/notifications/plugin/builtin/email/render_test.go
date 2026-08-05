package email

import (
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func TestRenderHTML_DefaultIsBrandedAndSelfContained(t *testing.T) {
	event := sampleEvent()
	event.Alert.MonitorID = "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234"
	html, err := RenderHTML(event, Templates{}, "https://probara.example.com")
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	for _, want := range []string{
		"<!DOCTYPE html>",
		"API health",                            // headline
		"Stopped responding",                    // summary sentence
		"timeout",                               // last error
		"#ff5a24",                               // brand accent
		"prefers-color-scheme: dark",            // both themes styled
		"https://probara.example.com/monitors/", // deep link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}
	// No remote assets: every mail client can block them, and several strip
	// data: URIs outright.
	if strings.Contains(html, "<img") || strings.Contains(html, "http://") {
		t.Error("html must not reference external assets")
	}
}

func TestRenderHTML_NoActionButtonWithoutAppBaseURL(t *testing.T) {
	html, err := RenderHTML(sampleEvent(), Templates{}, "")
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if strings.Contains(html, "Open the monitor") {
		t.Error("expected no call-to-action button when no app base URL is configured")
	}
}

func TestRenderHTML_TextOverrideSuppressesHTMLPart(t *testing.T) {
	html, err := RenderHTML(sampleEvent(), Templates{Body: "plain only"}, "https://x.example")
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if html != "" {
		t.Errorf("expected empty HTML part for a text-only override, got %d bytes", len(html))
	}
}

func TestRenderHTML_OverrideTemplateEscapesAlertValues(t *testing.T) {
	event := sampleEvent()
	injected := `<script>alert(1)</script>`
	event.Alert.LastError = &injected

	html, err := RenderHTML(event, Templates{HTML: `<p>{{.last_error}}</p>`}, "")
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Errorf("alert values must be escaped, got %q", html)
	}
	if !strings.Contains(html, "<p>") {
		t.Errorf("author markup must survive, got %q", html)
	}
}

func TestRenderHTML_OverrideTemplateHasPresentationVars(t *testing.T) {
	html, err := RenderHTML(sampleEvent(), Templates{
		HTML: `{{.label}}|{{.status_label}}|{{.summary}}|{{.triggered_at_human}}`,
	}, "")
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	for _, want := range []string{"Alert Triggered", "DOWN", "Stopped responding", "UTC"} {
		if !strings.Contains(html, want) {
			t.Errorf("override render missing %q: %s", want, html)
		}
	}
}

func TestNewAlertView_TonesByKind(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*notifications.AlertEvent)
		wantLabel string
		wantColor string
	}{
		{"availability down", func(e *notifications.AlertEvent) {}, "DOWN", toneDown.Accent},
		{"reminder", func(e *notifications.AlertEvent) { e.Type = "reminder" }, "STILL DOWN", toneDown.Accent},
		{"resolved", func(e *notifications.AlertEvent) {
			e.Type = "resolved"
			e.Alert.Status = "resolved"
		}, "RECOVERED", toneUp.Accent},
		{"latency", func(e *notifications.AlertEvent) {
			e.Alert.Kind = notifications.KindLatencyAnomaly
		}, "DEGRADED", toneWarn.Accent},
		{"host metric", func(e *notifications.AlertEvent) {
			e.Alert.Kind = notifications.KindHostMetric
		}, "THRESHOLD BREACHED", toneWarn.Accent},
		{"tls expiry", func(e *notifications.AlertEvent) {
			e.Alert.Kind = notifications.KindTLSExpiry
		}, "EXPIRING SOON", toneWarn.Accent},
		{"mesh edge", func(e *notifications.AlertEvent) {
			e.Alert.Kind = notifications.KindMeshEdge
		}, "PATH DOWN", toneDown.Accent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := sampleEvent()
			tc.mutate(&event)
			v := newAlertView(event, "")
			if v.Tone.Label != tc.wantLabel {
				t.Errorf("tone label = %q, want %q", v.Tone.Label, tc.wantLabel)
			}
			if v.Tone.Accent != tc.wantColor {
				t.Errorf("tone accent = %q, want %q", v.Tone.Accent, tc.wantColor)
			}
		})
	}
}

func TestNewAlertView_DurationUsesEventClockNotWallClock(t *testing.T) {
	event := sampleEvent()
	event.Type = "reminder"
	event.Alert.TriggeredAt = event.Timestamp.Add(-95 * time.Minute)

	v := newAlertView(event, "")
	if !strings.Contains(v.Summary, "1h 35m") {
		t.Errorf("summary should report elapsed time from the event timestamp: %q", v.Summary)
	}
	if got := durationOf(v); got != "1h 35m" {
		t.Errorf("duration row = %q, want 1h 35m", got)
	}
}

func TestNewAlertView_MeshEdgeLinksToMeshMatrix(t *testing.T) {
	event := sampleEvent()
	event.Alert.Kind = notifications.KindMeshEdge
	event.Alert.MonitorID = emptyUUID
	src, dst := "eu-west", "us-east"
	event.Alert.SourceLocationName = &src
	event.Alert.TargetLocationName = &dst

	v := newAlertView(event, "https://probara.example.com")
	if v.ActionURL != "https://probara.example.com/mesh" {
		t.Errorf("action URL = %q", v.ActionURL)
	}
	if !strings.Contains(v.Summary, "from eu-west to us-east") {
		t.Errorf("summary = %q", v.Summary)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:          "45s",
		90 * time.Second:          "1m 30s",
		4 * time.Minute:           "4m",
		time.Hour + 4*time.Minute: "1h 4m",
		3 * time.Hour:             "3h",
		50 * time.Hour:            "2d 2h",
		48 * time.Hour:            "2d",
	}
	for in, want := range cases {
		if got := humanDuration(in); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderAlertText_MirrorsHTMLContent(t *testing.T) {
	event := sampleEvent()
	event.Alert.MonitorID = "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234"
	event.Alert.Kind = notifications.KindHostMetric
	metric := "memory"
	value, threshold := 97.5, 85.0
	event.Alert.MetricName = &metric
	event.Alert.MetricValue = &value
	event.Alert.ThresholdValue = &threshold

	v := newAlertView(event, "https://probara.example.com")
	text := renderAlertText(v)
	for _, want := range []string{"THRESHOLD BREACHED", "API health", "97.5%", "threshold 85%", "https://probara.example.com/monitors/"} {
		if !strings.Contains(text, want) {
			t.Errorf("text body missing %q:\n%s", want, text)
		}
	}
	// The text part must not leak markup into text-only clients.
	if strings.Contains(text, "<") {
		t.Errorf("text body contains markup:\n%s", text)
	}
}

func TestWrapText_PreservesExistingNewlines(t *testing.T) {
	got := wrapText("one two three\nfour", 8)
	if got != "one two\nthree\nfour" {
		t.Errorf("wrapText = %q", got)
	}
}
