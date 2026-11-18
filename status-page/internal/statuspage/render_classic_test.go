package statuspage

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/statustemplate"
)

func renderClassic(t *testing.T, data *StatusPageData) string {
	t.Helper()
	out, err := renderPublicStatusPage(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `id="servicesList"`) || !strings.Contains(out, "Past incidents") {
		t.Fatal("expected the classic template")
	}
	return out
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page lacks %q", want)
		}
	}
}

func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("rendered page contains %q", u)
		}
	}
}

// The template setting picks the built-in; unknown and empty names render
// the default, and a published custom template still wins.
func TestRender_TemplateSettingSelectsBuiltin(t *testing.T) {
	now := time.Now()
	data := classicSampleData(scenarioOperational, now)

	data.Template = ""
	if out := renderOrFail(t, data); strings.Contains(out, `id="historySection"`) || !strings.Contains(out, `id="statusHero"`) {
		t.Fatal("empty template setting must render the default template")
	}
	data.Template = "nonsense"
	if out := renderOrFail(t, data); !strings.Contains(out, `id="statusHero"`) {
		t.Fatal("unknown template setting must render the default template")
	}
	data.Template = statustemplate.BuiltinClassic
	renderClassic(t, data)

	data.CustomTemplateSource = `<p>custom {{.Slug}}</p>`
	data.CustomTemplateVersion = 1
	if out := renderOrFail(t, data); out != "<p>custom example</p>" {
		t.Fatalf("a published custom template must win, got %q", out)
	}
	// A broken custom template falls back to the page's chosen built-in.
	data.CustomTemplateSource = `{{if .Title}}unclosed`
	data.CustomTemplateVersion = 2
	out, customErr, err := renderStatusPageHTML(data)
	if err != nil || customErr == nil {
		t.Fatalf("want a custom error and a fallback render, got customErr=%v err=%v", customErr, err)
	}
	if !strings.Contains(out, `id="historySection"`) {
		t.Fatal("fallback must be the classic built-in the page selected")
	}
}

func renderOrFail(t *testing.T, data *StatusPageData) string {
	t.Helper()
	out, err := renderPublicStatusPage(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

// Every built-in renders the sample data and passes the upload validation.
func TestRender_EveryBuiltinRendersSampleData(t *testing.T) {
	for _, b := range statustemplate.Builtins() {
		if err := statustemplate.Validate(b.Source); err != nil {
			t.Fatalf("%s: Validate: %v", b.Name, err)
		}
		for _, s := range []classicScenario{scenarioOperational, scenarioOutage, scenarioMaintenance, scenarioNoData, scenarioEmpty} {
			data := classicSampleData(s, time.Now())
			data.Template = b.Name
			if _, err := renderPublicStatusPage(data); err != nil {
				t.Fatalf("%s/%s: %v", b.Name, s, err)
			}
		}
	}
}

func TestClassic_Operational(t *testing.T) {
	out := renderClassic(t, classicSampleData(scenarioOperational, time.Now()))
	mustContain(t, out,
		`data-t="ok" role="status"`, "All systems operational",
		`<h3 class="group-name">Core platform</h3>`, "Public API", "Operational",
		`data-range-pill="90d"`, "24 hours ago",
		"Scheduled maintenance", "Weekly database patching", "Affects Primary Postgres",
		"No incidents reported.", "Delayed webhook deliveries",
		`nonce="`+statustemplate.NoncePlaceholder+`"`,
	)
	// Operational: no summary line under the banner title.
	mustNotContain(t, out, `class="banner-sub"`)
	// Seven days of history, newest first.
	if got := strings.Count(out, `class="day-date"`); got != incidentHistoryDays {
		t.Fatalf("history days = %d, want %d", got, incidentHistoryDays)
	}
}

func TestClassic_IncidentWithSeveralUpdates(t *testing.T) {
	now := time.Now()
	out := renderClassic(t, classicSampleData(scenarioOutage, now))
	mustContain(t, out,
		`data-t="down" role="status"`, "Partial outage", "2 of 9 services are affected.",
		"Load balancer outage in eu-west", "Affects Load balancer, GraphQL gateway",
		"The rollback is 60% complete.", "A faulty configuration push is the cause.",
		"Requests through the eu-west load balancer are failing.",
		`<span class="comp-state" data-t="down">Outage</span>`,
		`<span class="comp-state" data-t="warn">Degraded</span>`,
	)
	// All updates, newest first, the newest carrying the current state.
	newest := strings.Index(out, "The rollback is 60% complete.")
	older := strings.Index(out, "A faulty configuration push")
	summary := strings.Index(out, "Requests through the eu-west load balancer are failing.")
	if !(newest < older && older < summary) {
		t.Fatal("updates must be listed newest first, the summary last")
	}
	if !strings.Contains(out, `<b>Identified</b> <span>— The rollback`) {
		t.Fatal("the newest update must carry the incident's state")
	}
}

func TestClassic_Maintenance(t *testing.T) {
	out := renderClassic(t, classicSampleData(scenarioMaintenance, time.Now()))
	mustContain(t, out,
		`data-t="maint" role="status"`, "Maintenance in progress",
		"1 of 9 services is under planned maintenance.",
		"Primary Postgres upgrade", "In progress · Affects Primary Postgres",
		`class="js-maint-range" data-start="`,
		`<span class="comp-state" data-t="maint">Maintenance</span>`,
	)
}

func TestClassic_SingleServiceHidesRedundantGroupRow(t *testing.T) {
	out := renderClassic(t, classicSampleData(scenarioSingle, time.Now()))
	mustContain(t, out, "Public API")
	mustNotContain(t, out, `class="group-name"`, "Scheduled maintenance")
}

func TestClassic_NoDataDrawsNeutralBars(t *testing.T) {
	out := renderClassic(t, classicSampleData(scenarioNoData, time.Now()))
	mustContain(t, out, "Some services are not reporting", `>No data</span>`, `<i data-t="none" data-tip="No data">`)
	mustNotContain(t, out, `<i data-t="ok"`)
}

func TestClassic_EmptyPage(t *testing.T) {
	out := renderClassic(t, classicSampleData(scenarioEmpty, time.Now()))
	mustContain(t, out, "No services are listed on this page yet.")
	mustNotContain(t, out, `data-range-pill=`, `class="axis"`)
}

func TestClassic_SubscriptionsOnOff(t *testing.T) {
	data := classicSampleData(scenarioOperational, time.Now())
	out := renderClassic(t, data)
	mustContain(t, out, `href="#subscribe"`, `id="subscribe"`, `name="email"`,
		`action="https://status.example.com`+subscribeSuffix+`"`)

	data.SubscribeFormEnabled = false
	out = renderClassic(t, data)
	mustNotContain(t, out, `href="#subscribe"`, `id="subscribe"`, `name="email"`)
}

func TestClassic_PrimaryColourReplacesAccent(t *testing.T) {
	data := classicSampleData(scenarioOperational, time.Now())
	data.PrimaryColor = nil
	out := renderClassic(t, data)
	mustContain(t, out, "--accent: #ff5a24;", "--accent-ink: #1a0b04;")

	violet := "#6D28D9"
	data.PrimaryColor = &violet
	out = renderClassic(t, data)
	mustContain(t, out, "--accent: #6d28d9;", "--accent-ink: #ffffff;")

	pale := "#fde68a"
	data.PrimaryColor = &pale
	out = renderClassic(t, data)
	mustContain(t, out, "--accent: #fde68a;", "--accent-ink: #1a0b04;")
}

// Hiding monitor uptime removes the strips, figures and range switch, and
// the target only shows when show_monitor_url is on.
func TestClassic_VisibilitySettings(t *testing.T) {
	data := classicSampleData(scenarioOperational, time.Now())
	out := renderClassic(t, data)
	mustContain(t, out, "https://api.example.com/health", `class="bars js-strip"`)

	data = classicSampleData(scenarioOperational, time.Now())
	data.ShowMonitorUptime = false
	data.ShowMonitorURL = false
	out = renderClassic(t, data)
	mustNotContain(t, out, "https://api.example.com/health", `class="bars js-strip"`, `data-range-pill=`, "% uptime", `data-range-24h=`)
}

// The server-drawn strip and its JSON agree, with one tooltip per slot.
func TestClassic_StripData(t *testing.T) {
	data := classicSampleData(scenarioOperational, time.Now())
	view := buildStatusPageRenderView(data)
	m := view.Monitors[0]
	if len(m.Bars) != (stripCells/bars24hCells)*bars24hCells {
		t.Fatalf("24h strip has %d cells", len(m.Bars))
	}
	for name, raw := range map[string]string{"24h": m.Bars24hJSON, "7d": m.Bars7dJSON, "30d": m.Bars30dJSON, "90d": m.Bars90dJSON} {
		var points []barPoint
		if err := json.Unmarshal([]byte(raw), &points); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := map[string]int{"24h": 24, "7d": 7, "30d": 30, "90d": 90}[name]
		if len(points) != want {
			t.Fatalf("%s: %d points, want %d", name, len(points), want)
		}
	}
	if !regexp.MustCompile(`^\d\d:00–\d\d:00 UTC · \d+\.\d\d%$`).MatchString(m.Bars[0].Tip) {
		t.Fatalf("hourly tooltip %q", m.Bars[0].Tip)
	}
	var days []barPoint
	_ = json.Unmarshal([]byte(m.Bars90dJSON), &days)
	if !regexp.MustCompile(`^[A-Z][a-z]{2} \d{1,2}, \d{4}$`).MatchString(days[len(days)-1].Label) {
		t.Fatalf("daily label %q", days[len(days)-1].Label)
	}
	out := renderClassic(t, data)
	if !strings.Contains(out, `data-tip="`+html.EscapeString(m.Bars[0].Tip)+`"`) {
		t.Fatal("bars must carry their tooltip")
	}
}

func TestClassicBanner(t *testing.T) {
	for _, tt := range []struct {
		name                                  string
		down, degraded, unknown, maint, total int
		title, tone, summary                  string
	}{
		{"operational", 0, 0, 0, 0, 9, "All systems operational", "ok", ""},
		{"degraded", 0, 1, 0, 0, 9, "Degraded performance", "warn", "1 of 9 services is affected."},
		{"partial", 2, 1, 0, 0, 9, "Partial outage", "down", "3 of 9 services are affected."},
		{"major", 5, 0, 0, 0, 9, "Major outage", "down", "5 of 9 services are affected."},
		{"maintenance", 0, 0, 0, 1, 9, "Maintenance in progress", "maint", "1 of 9 services is under planned maintenance."},
		{"unknown", 0, 0, 2, 0, 9, "Some services are not reporting", "unknown", "2 of 9 services are without recent data."},
		{"one service down", 1, 0, 0, 0, 1, "Major outage", "down", "1 of 1 service is affected."},
	} {
		view := statusPageRenderView{DownCount: tt.down, DegradedCount: tt.degraded, UnknownCount: tt.unknown, MaintenanceCount: tt.maint, MonitorCount: tt.total}
		title, tone, summary := classicBanner(view)
		if title != tt.title || tone != tt.tone || summary != tt.summary {
			t.Errorf("%s: got (%q, %q, %q)", tt.name, title, tone, summary)
		}
	}
}

// Resolved incidents are listed under their publication day, or their
// resolution day when published before the window; older ones drop out.
func TestIncidentHistoryDays(t *testing.T) {
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	resolved := func(published, resolvedAt time.Time) StatusPageIncident {
		return StatusPageIncident{Title: published.String(), State: "resolved", PublishedAt: published, ResolvedAt: &resolvedAt}
	}
	data := &StatusPageData{Title: "x", Incidents: []StatusPageIncident{
		resolved(now.AddDate(0, 0, -2), now.AddDate(0, 0, -2).Add(time.Hour)),
		resolved(now.AddDate(0, 0, -20), now.AddDate(0, 0, -1)),
		resolved(now.AddDate(0, 0, -30), now.AddDate(0, 0, -29)),
		{Title: "active", State: "investigating", PublishedAt: now.Add(-time.Hour)},
	}}
	view := statusPageRenderView{}
	applyClassicView(&view, data, now)
	if len(view.IncidentHistory) != 7 || view.IncidentHistory[0].DateISO != "2026-11-18" || view.IncidentHistory[6].DateISO != "2026-11-12" {
		t.Fatalf("history days %+v", view.IncidentHistory)
	}
	counts := map[string]int{}
	for _, d := range view.IncidentHistory {
		counts[d.DateISO] = len(d.Incidents)
	}
	if counts["2026-11-16"] != 1 || counts["2026-11-17"] != 1 || counts["2026-11-18"] != 0 {
		t.Fatalf("incidents per day %v", counts)
	}
	if len(view.ActiveIncidents) != 1 || len(view.PastIncidents) != 3 {
		t.Fatalf("active %d, past %d", len(view.ActiveIncidents), len(view.PastIncidents))
	}
	// An update on another day than its heading names its date.
	listed := view.IncidentHistory[1].Incidents[0]
	if got := listed.Updates[0].TimeText; !strings.HasPrefix(got, "Oct 29,") {
		t.Fatalf("summary posted on another day should carry its date, got %q", got)
	}
}

// The default template's output does not depend on the classic fields.
func TestClassicFieldsLeaveDefaultTemplateAlone(t *testing.T) {
	data := classicSampleData(scenarioOutage, time.Now())
	data.Template = statustemplate.BuiltinDefault
	out := renderOrFail(t, data)
	mustContain(t, out, "Issues detected", `id="statusHero"`)
	mustNotContain(t, out, "Partial outage", `id="historySection"`)
}
