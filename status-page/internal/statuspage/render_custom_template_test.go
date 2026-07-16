package statuspage

import (
	"strings"
	"testing"
)

func customTemplateTestData() *StatusPageData {
	return &StatusPageData{
		ID:    "6b8f2f6e-0000-0000-0000-000000000000",
		Slug:  "acme",
		Title: "Acme Status",
		Monitors: []MonitorStatus{
			{ID: "m1", Name: "API", MonitorType: "http", Status: "up"},
		},
	}
}

func TestRenderStatusPageHTMLUsesCustomTemplate(t *testing.T) {
	data := customTemplateTestData()
	data.CustomTemplateSource = `<html><body><h1 id="custom">{{.Title}}</h1>{{range .Monitors}}<div>{{.Name}}</div>{{end}}</body></html>`
	data.CustomTemplateVersion = 1

	html, customErr, err := renderStatusPageHTML(data, false)
	if err != nil {
		t.Fatalf("renderStatusPageHTML() error = %v", err)
	}
	if customErr != nil {
		t.Fatalf("custom template should render cleanly, got: %v", customErr)
	}
	if !strings.Contains(html, `<h1 id="custom">Acme Status</h1>`) {
		t.Fatalf("expected custom template output, got: %.200s", html)
	}
	if !strings.Contains(html, "<div>API</div>") {
		t.Fatal("expected monitor rendered by the custom template")
	}
}

func TestRenderStatusPageHTMLFallsBackOnBrokenCustomTemplate(t *testing.T) {
	data := customTemplateTestData()
	// Parses, but fails at execute time: .Title has no field .Nope.
	data.CustomTemplateSource = `<html><body>{{.Title.Nope}}</body></html>`
	data.CustomTemplateVersion = 2

	html, customErr, err := renderStatusPageHTML(data, false)
	if err != nil {
		t.Fatalf("renderStatusPageHTML() must not fail when falling back, got: %v", err)
	}
	if customErr == nil {
		t.Fatal("expected the custom template failure to be reported")
	}
	if !strings.Contains(html, "Acme Status") {
		t.Fatal("fallback must render the built-in template")
	}
}

func TestRenderStatusPageHTMLInjectsTierOneBranding(t *testing.T) {
	data := customTemplateTestData()
	data.CustomCSS = ".header { color: rebeccapurple; }"
	data.CustomHeadHTML = `<link rel="icon" href="/favicon-acme.svg">`
	data.CustomFooterHTML = `<div id="acme-footer">Acme Corp</div>`

	html, _, err := renderStatusPageHTML(data, false)
	if err != nil {
		t.Fatalf("renderStatusPageHTML() error = %v", err)
	}
	for _, want := range []string{
		"<style>.header { color: rebeccapurple; }</style>",
		`<link rel="icon" href="/favicon-acme.svg">`,
		`<div id="acme-footer">Acme Corp</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered page to contain %q", want)
		}
	}
}

func TestRenderStatusPageWithSourceSurfacesErrors(t *testing.T) {
	data := customTemplateTestData()

	if _, err := renderStatusPageWithSource(data, false, `{{if .Title}}unclosed`); err == nil {
		t.Fatal("expected a parse error for a broken draft")
	}

	html, err := renderStatusPageWithSource(data, false, `<p>{{.Slug}} draft</p>`)
	if err != nil {
		t.Fatalf("renderStatusPageWithSource() error = %v", err)
	}
	if !strings.Contains(html, "acme draft") {
		t.Fatalf("expected draft output, got: %s", html)
	}
}

func TestCustomTemplateCacheReusesParsedTemplates(t *testing.T) {
	data := customTemplateTestData()
	source := `<html><body>{{.Title}}</body></html>`

	first, err := customTemplateFor(data.ID, 7, source)
	if err != nil {
		t.Fatalf("customTemplateFor() error = %v", err)
	}
	second, err := customTemplateFor(data.ID, 7, source)
	if err != nil {
		t.Fatalf("customTemplateFor() error = %v", err)
	}
	if first != second {
		t.Fatal("same page+version must return the cached parsed template")
	}

	third, err := customTemplateFor(data.ID, 8, source)
	if err != nil {
		t.Fatalf("customTemplateFor() error = %v", err)
	}
	if third == first {
		t.Fatal("a new version must be parsed fresh, not served from the old key")
	}
}
