package reportschedules

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	"github.com/yassinebenameur/probara/shared/mail"
)

// The report email's wording. It lives with the report code, not in
// shared/notifications/present: it is not an alert notification. The HTML
// part draws no brand mark (CLAUDE.md: mail cannot carry data: images, and
// the alert email's table-cell mark is the one deliberate copy); an orange
// rule and the product name stand in.

// Brand tokens, as in the alert email (shared/notifications/plugin/builtin/email/view.go).
const (
	brandOrange = "#ff5a24"
	brandInk    = "#140a05"
	toneGood    = "#007845"
	toneBad     = "#a11350"
	toneMuted   = "#5f6368"
)

// reportView is the presentation model both parts render from, so the HTML
// and plain-text bodies cannot drift apart.
type reportView struct {
	SLAName      string
	PeriodKey    string
	PeriodRange  string
	Verdict      string
	VerdictColor string
	Availability string
	Target       string
	BudgetUsed   string
	BurnRate     string
	Exhaustion   string
	ReportURL    string
	Attached     string
	Recipient    string
	Unsubscribe  string
}

func newReportView(sla *models.SLA, r *models.SLAReport, appBaseURL, recipient, unsubscribe string, attachments []mail.Attachment) reportView {
	loc, err := time.LoadLocation(r.Definition.Timezone)
	if err != nil {
		loc = time.UTC
	}
	v := reportView{
		SLAName:   r.Definition.Name,
		PeriodKey: r.Period.Key,
		PeriodRange: fmt.Sprintf("%s – %s (%s)", r.Period.Start.In(loc).Format("2 Jan 2006"),
			r.Period.End.Add(-time.Second).In(loc).Format("2 Jan 2006"), r.Definition.Timezone),
		Verdict:      "No data",
		VerdictColor: toneMuted,
		Availability: "no data",
		Target:       strconv.FormatFloat(r.Definition.TargetPct, 'f', -1, 64) + "%",
		BudgetUsed:   "-",
		ReportURL:    appBaseURL + SLAReportPath(sla.ID.String(), sla.TenantID.String()),
		Recipient:    recipient,
		Unsubscribe:  unsubscribe,
	}
	if r.Summary.Met != nil {
		v.Verdict, v.VerdictColor = "Met", toneGood
		if !*r.Summary.Met {
			v.Verdict, v.VerdictColor = "Breached", toneBad
		}
	}
	if r.Summary.AvailabilityPct != nil {
		v.Availability = strconv.FormatFloat(*r.Summary.AvailabilityPct, 'f', 3, 64) + "%"
	}
	if b := r.Budget; r.Summary.AvailabilityPct != nil && b.AllowedSeconds > 0 {
		v.BudgetUsed = fmt.Sprintf("%.1f%% (%s of %s allowed)", b.ConsumedSeconds/b.AllowedSeconds*100,
			slas.HumanDuration(b.ConsumedSeconds), slas.HumanDuration(b.AllowedSeconds))
	}
	if r.Burn != nil {
		for _, w := range r.Burn.Windows {
			if w.Key == models.SLABurnWindowPeriod {
				v.BurnRate = slas.BurnRateText(w.BurnRate)
			}
		}
		v.Exhaustion = slas.ExhaustionText(r, loc)
	}
	names := make([]string, len(attachments))
	for i, a := range attachments {
		names[i] = a.Filename
	}
	v.Attached = strings.Join(names, ", ")
	return v
}

// SLAReportPath is the operator-UI link to an SLA's issued reports, in the
// house deep-link shape (present.AlertPath): the list route with the
// resource and its workspace, which switches to that workspace (a switch on
// a /slas/{id} page would fall back to the list) and then opens the SLA's
// Issued reports tab. web/lib/sla-links.ts parses it.
func SLAReportPath(slaID, tenantID string) string {
	return "/slas?" + url.Values{"sla": {slaID}, "tenant": {tenantID}}.Encode()
}

// subject is one line: control characters in an SLA name (the mail seam
// refuses them in headers) become spaces.
func (v reportView) subject() string {
	s := fmt.Sprintf("SLA report: %s, %s: %s (%s, target %s)", v.SLAName, v.PeriodKey, strings.ToLower(v.Verdict), v.Availability, v.Target)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
}

func (v reportView) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: availability report for %s, %s\n\n", v.SLAName, v.PeriodKey, v.PeriodRange)
	fmt.Fprintf(&b, "Status:            %s\n", v.Verdict)
	fmt.Fprintf(&b, "Availability:      %s (target %s)\n", v.Availability, v.Target)
	fmt.Fprintf(&b, "Error budget used: %s\n", v.BudgetUsed)
	if v.BurnRate != "" {
		fmt.Fprintf(&b, "Burn rate:         %s (1x spends the budget exactly over the period)\n", v.BurnRate)
		fmt.Fprintf(&b, "Budget:            %s\n", v.Exhaustion)
	}
	fmt.Fprintf(&b, "\nThe issued report is attached (%s). It is frozen: later changes to maintenance windows or history do not alter it.\n", v.Attached)
	fmt.Fprintf(&b, "Workspace members can open it in Probara: %s\n", v.ReportURL)
	fmt.Fprintf(&b, "\nYou receive this because %s is on this SLA's report schedule.\nUnsubscribe: %s\n", v.Recipient, v.Unsubscribe)
	return b.String()
}

var reportHTML = htmltemplate.Must(htmltemplate.New("report").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.SLAName}}</title></head>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:{{.Ink}}">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:24px 0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;overflow:hidden">
<tr><td style="height:4px;background:{{.Orange}};font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="padding:24px 28px 8px">
<div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:{{.Muted}}">Probara · SLA report</div>
<h1 style="margin:6px 0 4px;font-size:20px;line-height:1.3;color:{{.Ink}}">{{.SLAName}}</h1>
<div style="font-size:14px;color:{{.Muted}}">{{.PeriodKey}} · {{.PeriodRange}}</div>
</td></tr>
<tr><td style="padding:12px 28px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;line-height:1.5">
<tr><td style="padding:4px 0;color:{{.Muted}}">Status</td><td style="padding:4px 0;font-weight:600;color:{{.VerdictColor}}">{{.Verdict}}</td></tr>
<tr><td style="padding:4px 0;color:{{.Muted}}">Availability</td><td style="padding:4px 0;font-weight:600">{{.Availability}} <span style="font-weight:400;color:{{.Muted}}">(target {{.Target}})</span></td></tr>
<tr><td style="padding:4px 0;color:{{.Muted}}">Error budget used</td><td style="padding:4px 0">{{.BudgetUsed}}</td></tr>
{{if .BurnRate}}<tr><td style="padding:4px 0;color:{{.Muted}}">Burn rate</td><td style="padding:4px 0">{{.BurnRate}}</td></tr>
<tr><td style="padding:4px 0;color:{{.Muted}}">Budget</td><td style="padding:4px 0">{{.Exhaustion}}</td></tr>{{end}}
</table></td></tr>
<tr><td style="padding:8px 28px 4px;font-size:14px;line-height:1.5">The issued report is attached ({{.Attached}}). It is frozen: later changes to maintenance windows or history do not alter it.</td></tr>
<tr><td style="padding:16px 28px 24px"><a href="{{.ReportURL}}" style="display:inline-block;padding:10px 18px;border-radius:6px;background:{{.Orange}};color:{{.Ink}};font-weight:600;font-size:14px;text-decoration:none">Open in Probara</a>
<div style="margin-top:6px;font-size:12px;color:{{.Muted}}">For members of the workspace.</div></td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #e4e4e7;font-size:12px;line-height:1.5;color:{{.Muted}}">You receive this because {{.Recipient}} is on this SLA's report schedule. <a href="{{.Unsubscribe}}" style="color:{{.Muted}}">Unsubscribe</a></td></tr>
</table></td></tr></table></body></html>
`))

func (v reportView) html() (string, error) {
	var b bytes.Buffer
	err := reportHTML.Execute(&b, struct {
		reportView
		Orange, Ink, Muted string
	}{v, brandOrange, brandInk, toneMuted})
	return b.String(), err
}

// message is one recipient's email: the headline in both parts, the
// attachments, and the recipient's own unsubscribe link in the body and as
// List-Unsubscribe with RFC 8058 one-click POST.
func (s *Service) message(sc *row, sla *models.SLA, r *models.SLAReport, to string, attachments []mail.Attachment) (mail.Message, error) {
	page, oneClick, err := s.unsubscribeLinks(sc.TenantID, sc.ID, to)
	if err != nil {
		return mail.Message{}, fmt.Errorf("%w: unsubscribe link: %v", mail.ErrInvalidMessage, err)
	}
	v := newReportView(sla, r, s.appBaseURL, to, page, attachments)
	html, err := v.html()
	if err != nil {
		return mail.Message{}, fmt.Errorf("%w: render: %v", mail.ErrInvalidMessage, err)
	}
	return mail.Message{
		To:          []string{to},
		Subject:     v.subject(),
		Text:        v.text(),
		HTML:        html,
		Attachments: attachments,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + oneClick + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, nil
}
