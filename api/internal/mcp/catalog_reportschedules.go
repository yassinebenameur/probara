package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// slaReportScheduleTools manage an SLA's emailed report schedule.
func slaReportScheduleTools() []spec {
	sla := map[string]string{"id": "SLA ID."}
	return []spec{
		{
			Name:  "get_sla_report_schedule",
			Title: "Get SLA report schedule",
			Description: "An SLA's emailed report schedule (schedule is null when it has none): recipients, who " +
				"unsubscribed, the next send (period close plus a grace hour), the last period's per-recipient " +
				"delivery and last_error. mail_ready and mail_problems say whether this installation can send at all.",
			Method: http.MethodGet, Route: "/sla-report-schedules/{id}",
			PathDocs: sla,
		},
		{
			Name:  "set_sla_report_schedule",
			Title: "Set SLA report schedule",
			Description: "Create or replace an SLA's report schedule. When each period closes the report is issued " +
				"(frozen) and emailed with the PDF attached (CSV optional) to every recipient who has not unsubscribed. " +
				"Enabling needs SMTP, APP_BASE_URL and PROBARA_SECRETS_KEY on the API.",
			Method: http.MethodPut, Route: "/sla-report-schedules/{id}",
			PathDocs: sla,
			Body:     typeOf[models.PutSLAReportScheduleRequest](), Required: []string{"recipients"},
			FieldDocs: map[string]string{
				"enabled":     "Send at each period close (default true).",
				"recipients":  "Plain email addresses, 1-50; replaces the list.",
				"include_csv": "Attach the CSV export next to the PDF.",
				"resubscribe": "Recipients who unsubscribed and should get the report again (they must be in recipients).",
			},
		},
		{
			Name:        "delete_sla_report_schedule",
			Title:       "Delete SLA report schedule",
			Description: "Stop emailing an SLA's reports and forget its recipients and unsubscribes.",
			Method:      http.MethodDelete, Route: "/sla-report-schedules/{id}",
			PathDocs: sla, Destructive: true,
		},
	}
}

// reportUnsubscribeNotExposed are the public unsubscribe routes of report
// emails: authorized by a token from the email, not the caller's credential.
var reportUnsubscribeNotExposed = map[string]string{
	"GET /api/v1/report-unsubscribe":  "report email unsubscribe link, authorized by its signed token; set_sla_report_schedule manages recipients",
	"POST /api/v1/report-unsubscribe": "report email unsubscribe link, authorized by its signed token; set_sla_report_schedule manages recipients",
}

func init() {
	for route, reason := range reportUnsubscribeNotExposed {
		notExposed[route] = reason
	}
}
