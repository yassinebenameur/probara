package slas

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Export formats. Both render from the SLAReport document alone, so an
// issued report renders identically however long after it was frozen.
const (
	FormatJSON = "json"
	FormatCSV  = "csv"
	FormatPDF  = "pdf"
)

// FileName is the download name for a report in format.
func FileName(r *models.SLAReport, format string) string { return fileStem(r) + "." + format }

func reportLocation(r *models.SLAReport) *time.Location {
	loc, err := time.LoadLocation(r.Definition.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func pctString(p *float64, decimals int) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', decimals, 64)
}

func secondsString(s float64) string { return strconv.FormatFloat(s, 'f', 0, 64) }

// WriteCSV writes the report as consecutive sections separated by blank
// lines: summary, daily, monitors, outages.
func WriteCSV(w io.Writer, r *models.SLAReport) error {
	loc := reportLocation(r)
	ts := func(t time.Time) string { return t.In(loc).Format(time.RFC3339) }
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	rows := [][]string{
		{"SLA", r.Definition.Name},
		{"Period", r.Period.Key, ts(r.Period.Start), ts(r.Period.End)},
		{"Effective end", ts(r.Period.EffectiveEnd)},
		{"Closed", strconv.FormatBool(r.Period.IsClosed)},
		{"Timezone", r.Definition.Timezone},
		{"Aggregation", r.Definition.Aggregation},
		{"Degraded counts as down", strconv.FormatBool(r.Definition.DegradedCountsAsDown)},
		{"Target %", strconv.FormatFloat(r.Definition.TargetPct, 'f', -1, 64)},
		{"Availability %", pctString(r.Summary.AvailabilityPct, 5)},
		{"Coverage %", strconv.FormatFloat(r.Summary.CoveragePct, 'f', 3, 64)},
		{"Met", metString(r.Summary.Met)},
		{"Eligible seconds", secondsString(r.Summary.EligibleSeconds)},
		{"Down seconds", secondsString(r.Summary.DownSeconds)},
		{"Excluded seconds", secondsString(r.Summary.ExcludedSeconds)},
		{"Budget allowed seconds", secondsString(r.Budget.AllowedSeconds)},
		{"Budget consumed seconds", secondsString(r.Budget.ConsumedSeconds)},
		{"Budget remaining seconds", secondsString(r.Budget.RemainingSeconds)},
		{"Outages", strconv.Itoa(r.Response.OutageCount)},
		{"MTTR seconds", optSeconds(r.Response.MTTRSeconds)},
		{"Alerts", strconv.Itoa(r.Response.AlertCount)},
		{"Acknowledged alerts", strconv.Itoa(r.Response.AcknowledgedCount)},
		{"MTTA seconds", optSeconds(r.Response.MTTASeconds)},
		{"Generated at", ts(r.GeneratedAt)},
	}
	if r.Issued != nil {
		rows = append(rows, []string{"Issued at", ts(r.Issued.IssuedAt)}, []string{"Issued by", r.Issued.IssuedBy})
	}
	for _, n := range r.Notes {
		rows = append(rows, []string{"Note", n})
	}

	rows = append(rows, nil, []string{"Date", "Availability %", "Coverage %", "Down seconds"})
	for _, d := range r.Daily {
		rows = append(rows, []string{d.Date, pctString(d.AvailabilityPct, 5), strconv.FormatFloat(d.CoveragePct, 'f', 3, 64), secondsString(d.DownSeconds)})
	}

	rows = append(rows, nil, []string{"Monitor", "Monitor ID", "Type", "Availability %", "Coverage %", "Available seconds",
		"Unplanned down seconds", "Planned down seconds", "Paused seconds", "Unknown seconds", "Untracked seconds", "Outages"})
	for _, m := range r.Monitors {
		rows = append(rows, []string{m.Name, m.ID.String(), m.Type, pctString(m.AvailabilityPct, 5),
			strconv.FormatFloat(m.CoveragePct, 'f', 3, 64), secondsString(m.AvailableSeconds),
			secondsString(m.UnplannedDownSeconds), secondsString(m.PlannedDownSeconds), secondsString(m.PausedSeconds),
			secondsString(m.UnknownSeconds), secondsString(m.UntrackedSeconds), strconv.Itoa(m.OutageCount)})
	}

	rows = append(rows, nil, []string{"Outage scope", "Monitor ID", "Start", "End", "Duration seconds",
		"Planned seconds", "Unplanned seconds", "Started before period", "Ongoing"})
	for _, o := range r.ServiceOutages {
		rows = append(rows, outageCSV("Service", "", o, ts))
	}
	for _, o := range r.Outages {
		id := ""
		if o.MonitorID != nil {
			id = o.MonitorID.String()
		}
		rows = append(rows, outageCSV(o.MonitorName, id, o, ts))
	}
	if r.OutagesTruncated {
		rows = append(rows, []string{fmt.Sprintf("Outage list truncated at %d rows", len(r.Outages))})
	}

	for _, row := range rows {
		if row == nil {
			row = []string{}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func outageCSV(scope, id string, o models.SLAReportOutage, ts func(time.Time) string) []string {
	return []string{scope, id, ts(o.Start), ts(o.End), secondsString(o.DurationSeconds), secondsString(o.PlannedSeconds),
		secondsString(o.UnplannedSeconds), strconv.FormatBool(o.StartedBefore), strconv.FormatBool(o.Ongoing)}
}

func metString(m *bool) string {
	if m == nil {
		return ""
	}
	return strconv.FormatBool(*m)
}

func optSeconds(v *float64) string {
	if v == nil {
		return ""
	}
	return secondsString(*v)
}

// humanDuration renders seconds as "2h 05m", "43m 49s" or "12s".
func humanDuration(seconds float64) string {
	neg := seconds < 0
	s := int64(math.Round(math.Abs(seconds)))
	d, h, m, sec := s/86400, (s%86400)/3600, (s%3600)/60, s%60
	var out string
	switch {
	case d > 0:
		out = fmt.Sprintf("%dd %02dh %02dm", d, h, m)
	case h > 0:
		out = fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		out = fmt.Sprintf("%dm %02ds", m, sec)
	default:
		out = fmt.Sprintf("%ds", sec)
	}
	if neg {
		return "-" + out
	}
	return out
}

func humanPct(p *float64) string {
	if p == nil {
		return "No data"
	}
	return strconv.FormatFloat(*p, 'f', 3, 64) + "%"
}

// pdfMaxOutages bounds the outage rows printed; the CSV/JSON carry all.
const pdfMaxOutages = 60

type rgb struct{ r, g, b int }

var (
	pdfInk     = rgb{24, 24, 27}
	pdfMuted   = rgb{113, 113, 122}
	pdfRule    = rgb{228, 228, 231}
	pdfGood    = rgb{22, 163, 74}
	pdfWarn    = rgb{217, 119, 6}
	pdfBad     = rgb{220, 38, 38}
	pdfNoData  = rgb{212, 212, 216}
	pdfAccent  = rgb{234, 88, 12}
	pdfPanelBg = rgb{250, 250, 250}
)

// WritePDF renders the report as an A4 document. Core fonts only (cp1252
// via the translator), so no font files ship with the API.
func WritePDF(w io.Writer, r *models.SLAReport) error {
	loc := reportLocation(r)
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	color := func(c rgb) { pdf.SetTextColor(c.r, c.g, c.b) }
	fill := func(c rgb) { pdf.SetFillColor(c.r, c.g, c.b) }
	draw := func(c rgb) { pdf.SetDrawColor(c.r, c.g, c.b) }
	const width = 180.0

	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 8)
		color(pdfMuted)
		pdf.CellFormat(width/2, 5, tr("Generated by Probara"), "", 0, "L", false, 0, "")
		pdf.CellFormat(width/2, 5, fmt.Sprintf("Page %d/{nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AliasNbPages("")
	pdf.AddPage()

	// Header.
	fill(pdfAccent)
	pdf.Rect(15, 15, 3, 14, "F")
	pdf.SetXY(21, 15)
	pdf.SetFont("Helvetica", "B", 18)
	color(pdfInk)
	pdf.CellFormat(width-6, 8, tr(r.Definition.Name), "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	color(pdfMuted)
	dateFmt := "2 Jan 2006 15:04"
	// Periods are whole local days; print the inclusive last day, not the
	// exclusive midnight bound.
	pdf.CellFormat(width-6, 6, tr(fmt.Sprintf("Availability report %s · %s – %s (%s)", r.Period.Key,
		r.Period.Start.In(loc).Format("2 Jan 2006"), r.Period.End.Add(-time.Second).In(loc).Format("2 Jan 2006"),
		r.Definition.Timezone)), "", 1, "L", false, 0, "")
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "", 9)
	status := "Live report generated " + r.GeneratedAt.In(loc).Format(dateFmt)
	if r.Issued != nil {
		status = fmt.Sprintf("Issued %s by %s", r.Issued.IssuedAt.In(loc).Format(dateFmt), r.Issued.IssuedBy)
	}
	if !r.Period.IsClosed {
		status += " - period in progress, figures to " + r.Period.EffectiveEnd.In(loc).Format(dateFmt)
	}
	pdf.CellFormat(width, 5, tr(status), "", 1, "L", false, 0, "")
	if r.Definition.Description != "" {
		color(pdfInk)
		pdf.MultiCell(width, 5, tr(r.Definition.Description), "", "L", false)
	}
	pdf.Ln(4)

	// KPI tiles.
	verdict, verdictColor := "NO DATA", pdfMuted
	if r.Summary.Met != nil {
		if *r.Summary.Met {
			verdict, verdictColor = "MET", pdfGood
		} else {
			verdict, verdictColor = "BREACHED", pdfBad
		}
	}
	tiles := []struct {
		label, value string
		c            rgb
	}{
		{"Availability", humanPct(r.Summary.AvailabilityPct), pdfInk},
		{"Target", strconv.FormatFloat(r.Definition.TargetPct, 'f', -1, 64) + "%", pdfInk},
		{"Status", verdict, verdictColor},
		{"Coverage", strconv.FormatFloat(r.Summary.CoveragePct, 'f', 1, 64) + "%", pdfInk},
	}
	tileW := (width - 3*4) / 4
	y := pdf.GetY()
	for i, t := range tiles {
		x := 15 + float64(i)*(tileW+4)
		fill(pdfPanelBg)
		draw(pdfRule)
		pdf.Rect(x, y, tileW, 20, "FD")
		pdf.SetXY(x+3, y+3)
		pdf.SetFont("Helvetica", "", 8)
		color(pdfMuted)
		pdf.CellFormat(tileW-6, 4, tr(t.label), "", 2, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 14)
		color(t.c)
		pdf.CellFormat(tileW-6, 9, tr(t.value), "", 0, "L", false, 0, "")
	}
	pdf.SetXY(15, y+24)

	// Error budget.
	section(pdf, tr, "Error budget")
	kv(pdf, tr, [][2]string{
		{"Allowed downtime (full period)", humanDuration(r.Budget.AllowedFullPeriodSeconds)},
		{"Allowed so far (eligible time)", humanDuration(r.Budget.AllowedSeconds)},
		{"Consumed", humanDuration(r.Budget.ConsumedSeconds)},
		{"Remaining", budgetRemaining(r.Budget)},
	})

	// Daily strip.
	if len(r.Daily) > 0 {
		section(pdf, tr, "Daily availability")
		barW := width / float64(len(r.Daily))
		y := pdf.GetY()
		for i, d := range r.Daily {
			c := pdfNoData
			if d.AvailabilityPct != nil {
				switch {
				case metTarget(*d.AvailabilityPct, r.Definition.TargetPct):
					c = pdfGood
				case *d.AvailabilityPct >= 99:
					c = pdfWarn
				default:
					c = pdfBad
				}
			}
			fill(c)
			pdf.Rect(15+float64(i)*barW+0.3, y, math.Max(barW-0.6, 0.3), 9, "F")
		}
		pdf.SetFont("Helvetica", "", 7)
		color(pdfMuted)
		step := int(math.Ceil(float64(len(r.Daily)) / 10))
		for i := 0; i < len(r.Daily); i += step {
			pdf.SetXY(15+float64(i)*barW, y+10)
			pdf.CellFormat(barW*float64(step), 4, r.Daily[i].Date[5:], "", 0, "L", false, 0, "")
		}
		pdf.SetXY(15, y+15)
		pdf.CellFormat(width, 4, tr("Green: at or above target. Amber: below target, at least 99%. Red: below 99%. Grey: no data."), "", 1, "L", false, 0, "")
		pdf.Ln(2)
	}

	// Monitors.
	section(pdf, tr, fmt.Sprintf("Monitors (%d)", len(r.Monitors)))
	cols := []float64{64, 26, 22, 26, 26, 16}
	tableHeader(pdf, tr, cols, []string{"Monitor", "Availability", "Coverage", "Downtime", "Planned", "Outages"})
	for _, m := range r.Monitors {
		tableRow(pdf, tr, cols, []string{m.Name, humanPct(m.AvailabilityPct), strconv.FormatFloat(m.CoveragePct, 'f', 1, 64) + "%",
			humanDuration(m.UnplannedDownSeconds), humanDuration(m.PlannedDownSeconds), strconv.Itoa(m.OutageCount)})
	}
	pdf.Ln(3)

	// Response.
	section(pdf, tr, "Response")
	kv(pdf, tr, [][2]string{
		{"Outages", strconv.Itoa(r.Response.OutageCount)},
		{"Mean time to recover", optHuman(r.Response.MTTRSeconds)},
		{"Availability alerts", fmt.Sprintf("%d (%d acknowledged)", r.Response.AlertCount, r.Response.AcknowledgedCount)},
		{"Mean time to acknowledge", optHuman(r.Response.MTTASeconds)},
	})

	// Outages.
	outages := r.Outages
	title := "Outages"
	if r.Definition.Aggregation == models.SLAAggregationSerial {
		outages, title = r.ServiceOutages, "Service outages (any monitor down, maintenance excluded)"
	}
	section(pdf, tr, title)
	if len(outages) == 0 {
		pdf.SetFont("Helvetica", "", 9)
		color(pdfMuted)
		pdf.CellFormat(width, 5, tr("No outages in this period."), "", 1, "L", false, 0, "")
	} else {
		ocols := []float64{40, 40, 28, 28, 44}
		tableHeader(pdf, tr, ocols, []string{"Start", "End", "Duration", "Planned", "Monitor"})
		for i, o := range outages {
			if i >= pdfMaxOutages {
				pdf.SetFont("Helvetica", "I", 8)
				color(pdfMuted)
				pdf.CellFormat(width, 5, tr(fmt.Sprintf("%d more in the CSV/JSON export.", len(outages)-pdfMaxOutages)), "", 1, "L", false, 0, "")
				break
			}
			end := o.End.In(loc).Format(dateFmt)
			if o.Ongoing {
				end += " (ongoing)"
			}
			name := o.MonitorName
			if name == "" {
				name = "Service"
			}
			tableRow(pdf, tr, ocols, []string{o.Start.In(loc).Format(dateFmt), end, humanDuration(o.DurationSeconds),
				humanDuration(o.PlannedSeconds), name})
		}
	}
	pdf.Ln(3)

	// Method and notes.
	section(pdf, tr, "Method")
	pdf.SetFont("Helvetica", "", 8)
	color(pdfMuted)
	composite := "Serial: the service counts as down whenever any monitor is down outside maintenance."
	if r.Definition.Aggregation == models.SLAAggregationMean {
		composite = "Mean: the unweighted average of each monitor's availability."
	}
	degraded := "Degraded time counts as available."
	if r.Definition.DegradedCountsAsDown {
		degraded = "Degraded time counts as down."
	}
	method := "Availability = available time / (available time + unplanned downtime), measured from the monitors' state history. " +
		"Downtime inside maintenance windows, paused time and time without monitoring evidence are excluded; coverage is the observed share of the period. " +
		composite + " " + degraded
	pdf.MultiCell(width, 4, tr(method), "", "L", false)
	for _, n := range r.Notes {
		pdf.MultiCell(width, 4, tr("- "+n), "", "L", false)
	}

	return pdf.Output(w)
}

func budgetRemaining(b models.SLAReportBudget) string {
	if b.RemainingPct == nil {
		return "-"
	}
	return fmt.Sprintf("%s (%.1f%%)", humanDuration(b.RemainingSeconds), *b.RemainingPct)
}

func optHuman(v *float64) string {
	if v == nil {
		return "-"
	}
	return humanDuration(*v)
}

func section(pdf *fpdf.Fpdf, tr func(string) string, title string) {
	if pdf.GetY() > 250 {
		pdf.AddPage()
	}
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(pdfInk.r, pdfInk.g, pdfInk.b)
	pdf.CellFormat(180, 7, tr(title), "", 1, "L", false, 0, "")
	pdf.SetDrawColor(pdfRule.r, pdfRule.g, pdfRule.b)
	pdf.Line(15, pdf.GetY(), 195, pdf.GetY())
	pdf.Ln(2)
}

func kv(pdf *fpdf.Fpdf, tr func(string) string, rows [][2]string) {
	for _, row := range rows {
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(pdfMuted.r, pdfMuted.g, pdfMuted.b)
		pdf.CellFormat(70, 5, tr(row[0]), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetTextColor(pdfInk.r, pdfInk.g, pdfInk.b)
		pdf.CellFormat(110, 5, tr(row[1]), "", 1, "L", false, 0, "")
	}
	pdf.Ln(3)
}

func tableHeader(pdf *fpdf.Fpdf, tr func(string) string, cols []float64, labels []string) {
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetTextColor(pdfMuted.r, pdfMuted.g, pdfMuted.b)
	for i, l := range labels {
		pdf.CellFormat(cols[i], 6, tr(l), "B", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
}

func tableRow(pdf *fpdf.Fpdf, tr func(string) string, cols []float64, cells []string) {
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(pdfInk.r, pdfInk.g, pdfInk.b)
	pdf.SetDrawColor(pdfRule.r, pdfRule.g, pdfRule.b)
	for i, c := range cells {
		pdf.CellFormat(cols[i], 5.5, fitText(pdf, tr(c), cols[i]-1), "B", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
}

// fitText truncates s with "..." to fit width mm in the current font.
func fitText(pdf *fpdf.Fpdf, s string, width float64) string {
	if pdf.GetStringWidth(s) <= width {
		return s
	}
	for len(s) > 0 && pdf.GetStringWidth(s+"...") > width {
		s = s[:len(s)-1]
	}
	return s + "..."
}
