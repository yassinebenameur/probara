package email

import (
	"embed"
	htmltemplate "html/template"
	"strings"
)

//go:embed alert.gohtml
var templateFS embed.FS

// alertTemplate is parsed once at init; a parse failure is a build-time bug in
// the embedded markup, so it panics rather than degrading silently.
var alertTemplate = htmltemplate.Must(htmltemplate.ParseFS(templateFS, "alert.gohtml"))

// Outlook's Word rendering engine ignores max-width, so the fluid card is
// wrapped in a fixed-width table behind a conditional comment. These are typed
// htmltemplate.HTML because html/template elides comments written in the
// template source — the only way to emit one is to inject it as trusted HTML.
const (
	msoWrapOpen  = `<!--[if mso]><table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" align="center"><tr><td><![endif]-->`
	msoWrapClose = `<!--[if mso]></td></tr></table><![endif]-->`
)

// htmlData is the template context: the presentation model plus the brand
// constants and raw fragments the markup interpolates.
type htmlData struct {
	alertView
	BrandInk     string
	BrandOrange  string
	MSOWrapOpen  htmltemplate.HTML
	MSOWrapClose htmltemplate.HTML
}

// renderAlertHTML renders the branded HTML part for a view.
func renderAlertHTML(v alertView) (string, error) {
	var sb strings.Builder
	if err := alertTemplate.Execute(&sb, htmlData{
		alertView:    v,
		BrandInk:     brandInk,
		BrandOrange:  brandOrange,
		MSOWrapOpen:  htmltemplate.HTML(msoWrapOpen),
		MSOWrapClose: htmltemplate.HTML(msoWrapClose),
	}); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// renderAlertText renders the plain-text part from the same view, so the two
// MIME parts of one message always describe the same alert. Layout is a short
// prose lead followed by an aligned key/value block — readable both in a
// text-only client and as the fallback a mail archive indexes.
func renderAlertText(v alertView) string {
	var b strings.Builder

	b.WriteString(strings.ToUpper(v.Tone.Label))
	b.WriteString(" · ")
	b.WriteString(v.Label)
	b.WriteString("\n")
	b.WriteString(v.MonitorName)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", textRuleWidth(v.MonitorName)))
	b.WriteString("\n\n")

	b.WriteString(wrapText(v.Summary, 72))
	b.WriteString("\n")

	if v.Metric != nil {
		b.WriteString("\n")
		b.WriteString(v.Metric.Label)
		b.WriteString(": ")
		b.WriteString(v.Metric.Value)
		if v.Metric.Note != "" {
			b.WriteString(" (")
			b.WriteString(v.Metric.Note)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}

	if len(v.Rows) > 0 {
		b.WriteString("\n")
		width := 0
		for _, row := range v.Rows {
			if len(row.Label) > width {
				width = len(row.Label)
			}
		}
		for _, row := range v.Rows {
			b.WriteString(row.Label)
			b.WriteString(":")
			b.WriteString(strings.Repeat(" ", width-len(row.Label)+1))
			b.WriteString(row.Value)
			b.WriteString("\n")
		}
	}

	if v.LastError != "" {
		b.WriteString("\nLast error:\n  ")
		b.WriteString(strings.ReplaceAll(wrapText(v.LastError, 70), "\n", "\n  "))
		b.WriteString("\n")
	}

	if v.RootCause != "" {
		b.WriteString("\nLikely root cause:\n  ")
		b.WriteString(v.RootCause)
		b.WriteString("\n")
	}

	if len(v.Locations) > 0 {
		b.WriteString("\nFailing locations:\n")
		for _, loc := range v.Locations {
			b.WriteString("  - ")
			b.WriteString(loc.Name)
			if loc.DownSince != "" {
				b.WriteString(" (")
				b.WriteString(loc.DownSince)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
	}

	if v.ActionURL != "" {
		b.WriteString("\n")
		b.WriteString(v.ActionLabel)
		b.WriteString(": ")
		b.WriteString(v.ActionURL)
		b.WriteString("\n")
	}

	b.WriteString("\n--\nProbara · uptime & voice observability\n")
	b.WriteString("You are receiving this because your address is on an email alert\n")
	b.WriteString("channel for this workspace.\n")
	b.WriteString("alert ")
	b.WriteString(v.AlertID)
	if v.TenantID != "" {
		b.WriteString(" · workspace ")
		b.WriteString(v.TenantID)
	}
	b.WriteString("\n")

	return b.String()
}

// textRuleWidth keeps the underline under the monitor name bounded so a very
// long name does not produce a 300-character rule.
func textRuleWidth(name string) int {
	n := len([]rune(name))
	if n < 8 {
		return 8
	}
	if n > 64 {
		return 64
	}
	return n
}

// wrapText greedy-wraps to width columns, preserving existing newlines (probe
// error output is often already multi-line).
func wrapText(s string, width int) string {
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
