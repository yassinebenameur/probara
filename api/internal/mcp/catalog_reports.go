package mcp

import (
	"net/http"

	reportscheduleservice "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
)

// reportScheduleTools cover emailed SLA report schedules.
func reportScheduleTools() []spec {
	schedule := map[string]string{"id": "Report schedule ID, from list_report_schedules."}
	docs := map[string]string{
		"recipients": "Email addresses (inside or outside the workspace, at most 50); each gets their own message with an unsubscribe link.",
		"formats":    "Attachments: any of pdf, csv (default [\"pdf\"]).",
		"auto_issue": "Issue (freeze) the closed period before sending it (default true); off sends the issued copy when one exists, else the live report.",
		"enabled":    "Send at each period close (default true). Needs SMTP and APP_BASE_URL on the API; list_report_schedules' mail_ready says whether they are set.",
	}
	createDocs := map[string]string{"sla_id": "SLA ID, from list_slas."}
	for k, v := range docs {
		createDocs[k] = v
	}
	return []spec{
		{
			Name:  "list_report_schedules",
			Title: "List SLA report schedules",
			Description: "Emailed SLA report schedules with their recipients, who unsubscribed, the next run and the last " +
				"run's outcome. mail_ready is false (with mail_problem) when the API cannot send email.",
			Method: http.MethodGet, Route: "/report-schedules",
			Query: []param{{Name: "sla_id", Description: "Only this SLA's schedules."}},
		},
		{
			Name:  "create_report_schedule",
			Title: "Create SLA report schedule",
			Description: "Email an SLA's report to recipients about an hour after each period closes (weekly, monthly " +
				"or quarterly, in the SLA's timezone), issuing it first by default. The first report is the running period's.",
			Method: http.MethodPost, Route: "/report-schedules",
			Body: typeOf[reportscheduleservice.CreateRequest](), Required: []string{"sla_id", "recipients"},
			FieldDocs: createDocs,
		},
		{
			Name:        "get_report_schedule",
			Title:       "Get SLA report schedule",
			Description: "One report schedule.",
			Method:      http.MethodGet, Route: "/report-schedules/{id}",
			PathDocs: schedule,
		},
		{
			Name:  "update_report_schedule",
			Title: "Update SLA report schedule",
			Description: "Change a schedule; unsent fields stay, recipients/formats when sent replace the list. " +
				"Re-enabling starts from the running period (no backlog).",
			Method: http.MethodPatch, Route: "/report-schedules/{id}",
			PathDocs: schedule,
			Body:     typeOf[reportscheduleservice.UpdateRequest](), FieldDocs: docs,
		},
		{
			Name:        "delete_report_schedule",
			Title:       "Delete SLA report schedule",
			Description: "Delete a schedule; the unsubscribe links it mailed stop working and its opt-outs are forgotten.",
			Method:      http.MethodDelete, Route: "/report-schedules/{id}",
			PathDocs: schedule, Destructive: true,
		},
		{
			Name:  "send_report_schedule",
			Title: "Send SLA report now",
			Description: "Email the last closed period's report to the schedule's recipients now (minus those who " +
				"unsubscribed), without issuing it: the issued copy when there is one, else the live report. The " +
				"next scheduled run is unchanged.",
			Method: http.MethodPost, Route: "/report-schedules/{id}/send",
			PathDocs: schedule,
		},
	}
}

// reportNotExposed are the report routes deliberately left out of the
// catalog, merged into notExposed.
var reportNotExposed = map[string]string{
	"GET /api/v1/report-unsubscribe/{token}":  "public unsubscribe link of report emails, authenticated by the token in the URL",
	"POST /api/v1/report-unsubscribe/{token}": "public unsubscribe link of report emails (RFC 8058 one-click)",
}

func init() {
	for route, reason := range reportNotExposed {
		notExposed[route] = reason
	}
}
