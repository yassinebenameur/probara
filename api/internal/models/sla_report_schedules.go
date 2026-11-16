package models

import (
	"time"

	"github.com/google/uuid"
)

// Delivery states of one recipient's copy of one period's emailed SLA
// report (api/internal/services/reportschedules).
const (
	// SLAReportDeliveryPending: waiting for its first attempt or a retry.
	SLAReportDeliveryPending = "pending"
	// SLAReportDeliveryClaimed: reserved by a runner's batch; nothing sent
	// yet, so it is handed back if the runner stops or dies.
	SLAReportDeliveryClaimed = "claimed"
	// SLAReportDeliverySending: its message is going out right now; the only
	// state that can become uncertain.
	SLAReportDeliverySending = "sending"
	// SLAReportDeliverySent: the mail relay accepted it.
	SLAReportDeliverySent = "sent"
	// SLAReportDeliveryFailed: refused for good, or out of retries.
	SLAReportDeliveryFailed = "failed"
	// SLAReportDeliveryUncertain: the connection was lost after the message
	// went out, or the API stopped mid-send; it may have arrived, so it is
	// never sent again.
	SLAReportDeliveryUncertain = "uncertain"
	// SLAReportDeliverySkipped: the recipient had unsubscribed.
	SLAReportDeliverySkipped = "skipped"
	// SLAReportDeliveryCancelled: the schedule was disabled, or the
	// recipient removed, before it was sent.
	SLAReportDeliveryCancelled = "cancelled"
)

// SLAReportSchedule emails an SLA's report to a recipient list when each
// period closes.
type SLAReportSchedule struct {
	ID         uuid.UUID `json:"id"`
	SLAID      uuid.UUID `json:"sla_id"`
	Enabled    bool      `json:"enabled"`
	Recipients []string  `json:"recipients"`
	// Unsubscribed are the recipients who followed their unsubscribe link;
	// they are skipped until re-subscribed.
	Unsubscribed []string `json:"unsubscribed"`
	// IncludeCSV attaches the CSV export next to the PDF.
	IncludeCSV bool `json:"include_csv"`
	// NextRunAt is when the running period's report is issued and sent:
	// the period's close plus a grace for late results. Only meaningful
	// while enabled.
	NextRunAt time.Time `json:"next_run_at"`
	// LastPeriodKey is the last period claimed for sending.
	LastPeriodKey string     `json:"last_period_key,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at"`
	// LastError says why the last period could not be issued, followed by,
	// per period still kept, how many recipients failed, are uncertain or
	// are retrying.
	LastError string `json:"last_error,omitempty"`
	// Deliveries is each kept period's outcome per recipient, newest period
	// first: the last claimed one, and an older one while it has retries.
	Deliveries []SLAReportDelivery `json:"deliveries"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// SLAReportDelivery is one recipient's copy of one period's report.
type SLAReportDelivery struct {
	PeriodKey string `json:"period_key"`
	Address   string `json:"address"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	// NextAttemptAt is set while a failed attempt waits for its retry.
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// SLAReportScheduleResponse is an SLA's schedule (null when it has none)
// and whether this API can send scheduled reports at all, so a client can
// explain why a schedule cannot be enabled before anyone tries.
type SLAReportScheduleResponse struct {
	Schedule *SLAReportSchedule `json:"schedule"`
	// MailReady is false when SMTP, APP_BASE_URL or PROBARA_SECRETS_KEY is
	// missing on the API; MailProblems then names each.
	MailReady    bool     `json:"mail_ready"`
	MailProblems []string `json:"mail_problems"`
}

// PutSLAReportScheduleRequest creates or replaces an SLA's schedule.
type PutSLAReportScheduleRequest struct {
	// Enabled defaults to true.
	Enabled    *bool    `json:"enabled"`
	Recipients []string `json:"recipients"`
	IncludeCSV bool     `json:"include_csv"`
	// Resubscribe lists recipients to re-subscribe after they unsubscribed.
	// An address added to the list (absent from it before) is re-subscribed
	// without being named here.
	Resubscribe []string `json:"resubscribe"`
}

// Report unsubscribe link states (the public unsubscribe routes).
const (
	ReportUnsubscribeSubscribed   = "subscribed"
	ReportUnsubscribeUnsubscribed = "unsubscribed"
	ReportUnsubscribeExpired      = "expired"
	ReportUnsubscribeInvalid      = "invalid"
)

// ReportUnsubscribeResponse is all an unsubscribe link holder learns: the
// link's state and the SLA's name.
type ReportUnsubscribeResponse struct {
	State   string `json:"state"`
	SLAName string `json:"sla_name,omitempty"`
}
