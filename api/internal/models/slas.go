package models

import (
	"time"

	"github.com/google/uuid"
)

// SLA aggregation modes (docs/state-semantics.md S-U6).
const (
	SLAAggregationSerial = "serial"
	SLAAggregationMean   = "mean"
)

// SLA is a named availability objective over a monitor set.
type SLA struct {
	ID                   uuid.UUID   `json:"id"`
	TenantID             uuid.UUID   `json:"tenant_id"`
	Name                 string      `json:"name"`
	Description          string      `json:"description"`
	TargetPct            float64     `json:"target_pct"`
	Aggregation          string      `json:"aggregation"`
	Period               string      `json:"period"`
	Timezone             string      `json:"timezone"`
	DegradedCountsAsDown bool        `json:"degraded_counts_as_down"`
	Tags                 []string    `json:"tags"`
	MonitorIDs           []uuid.UUID `json:"monitor_ids"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	// Current is the to-date status of the running period (list responses).
	Current *SLAStatus `json:"current,omitempty"`
}

// SLAStatus is the headline of one period.
type SLAStatus struct {
	PeriodKey       string    `json:"period_key"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	MonitorCount    int       `json:"monitor_count"`
	HasData         bool      `json:"has_data"`
	AvailabilityPct *float64  `json:"availability_pct"`
	CoveragePct     float64   `json:"coverage_pct"`
	Met             *bool     `json:"met"`
	// BudgetRemainingPct is the unspent share of the error budget; negative
	// once the budget is overspent. Nil without data.
	BudgetRemainingPct *float64 `json:"budget_remaining_pct"`
}

type CreateSLARequest struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	TargetPct            float64  `json:"target_pct"`
	Aggregation          string   `json:"aggregation"`
	Period               string   `json:"period"`
	Timezone             string   `json:"timezone"`
	DegradedCountsAsDown bool     `json:"degraded_counts_as_down"`
	Tags                 []string `json:"tags"`
	MonitorIDs           []string `json:"monitor_ids"`
}

type UpdateSLARequest struct {
	Name                 *string   `json:"name"`
	Description          *string   `json:"description"`
	TargetPct            *float64  `json:"target_pct"`
	Aggregation          *string   `json:"aggregation"`
	Period               *string   `json:"period"`
	Timezone             *string   `json:"timezone"`
	DegradedCountsAsDown *bool     `json:"degraded_counts_as_down"`
	Tags                 *[]string `json:"tags"`
	MonitorIDs           *[]string `json:"monitor_ids"`
}

// SLAReport is a computed (live) or issued (frozen) report. Issued reports
// store exactly this document, so its shape is a compatibility contract:
// add fields, never repurpose them.
type SLAReport struct {
	SLAID       uuid.UUID           `json:"sla_id"`
	Definition  SLAReportDefinition `json:"definition"`
	Period      SLAReportPeriod     `json:"period"`
	GeneratedAt time.Time           `json:"generated_at"`
	// Issued is set on frozen reports.
	Issued   *SLAReportIssue    `json:"issued,omitempty"`
	Summary  SLAReportSummary   `json:"summary"`
	Budget   SLAReportBudget    `json:"budget"`
	Daily    []SLAReportDay     `json:"daily"`
	Monitors []SLAReportMonitor `json:"monitors"`
	// ServiceOutages are the composite's unplanned-down spans (serial SLAs).
	ServiceOutages []SLAReportOutage `json:"service_outages"`
	// Outages are per-monitor down spans (capped; see OutagesTruncated).
	Outages          []SLAReportOutage `json:"outages"`
	OutagesTruncated bool              `json:"outages_truncated"`
	Response         SLAReportResponse `json:"response"`
	Notes            []string          `json:"notes"`
	// SuspectedCauses is the suspected-cause breakdown of monitor outages.
	// Absent on reports computed before it existed, so every renderer
	// (web, CSV, PDF) must treat it as optional and leave such a report's
	// output unchanged.
	SuspectedCauses *SLAReportSuspectedCauses `json:"suspected_causes,omitempty"`
}

// SLAReportDefinition is the SLA as it stood when the report was computed.
type SLAReportDefinition struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	TargetPct            float64  `json:"target_pct"`
	Aggregation          string   `json:"aggregation"`
	Period               string   `json:"period"`
	Timezone             string   `json:"timezone"`
	DegradedCountsAsDown bool     `json:"degraded_counts_as_down"`
	Tags                 []string `json:"tags"`
}

type SLAReportPeriod struct {
	Key   string    `json:"key"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// EffectiveEnd is End, or now for the running period.
	EffectiveEnd time.Time `json:"effective_end"`
	IsClosed     bool      `json:"is_closed"`
	IsCustom     bool      `json:"is_custom"`
	// PreviousKey / NextKey step through calendar periods (empty for custom
	// ranges; NextKey empty when the next period has not started).
	PreviousKey string `json:"previous_key,omitempty"`
	NextKey     string `json:"next_key,omitempty"`
}

type SLAReportIssue struct {
	ID       uuid.UUID `json:"id"`
	IssuedAt time.Time `json:"issued_at"`
	IssuedBy string    `json:"issued_by"`
}

// SLAReportSummary seconds are the composite's for serial SLAs and
// per-monitor means for mean SLAs.
type SLAReportSummary struct {
	HasData          bool     `json:"has_data"`
	AvailabilityPct  *float64 `json:"availability_pct"`
	CoveragePct      float64  `json:"coverage_pct"`
	Met              *bool    `json:"met"`
	WindowSeconds    float64  `json:"window_seconds"`
	AvailableSeconds float64  `json:"available_seconds"`
	DownSeconds      float64  `json:"down_seconds"`
	ExcludedSeconds  float64  `json:"excluded_seconds"`
	EligibleSeconds  float64  `json:"eligible_seconds"`
}

type SLAReportBudget struct {
	// AllowedSeconds = (1 - target) x eligible time so far.
	AllowedSeconds   float64  `json:"allowed_seconds"`
	ConsumedSeconds  float64  `json:"consumed_seconds"`
	RemainingSeconds float64  `json:"remaining_seconds"`
	RemainingPct     *float64 `json:"remaining_pct"`
	// AllowedFullPeriodSeconds = (1 - target) x the whole calendar period.
	AllowedFullPeriodSeconds float64 `json:"allowed_full_period_seconds"`
}

type SLAReportDay struct {
	Date            string    `json:"date"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	HasData         bool      `json:"has_data"`
	AvailabilityPct *float64  `json:"availability_pct"`
	CoveragePct     float64   `json:"coverage_pct"`
	DownSeconds     float64   `json:"down_seconds"`
}

type SLAReportMonitor struct {
	ID                   uuid.UUID  `json:"id"`
	Name                 string     `json:"name"`
	Type                 string     `json:"type"`
	HasData              bool       `json:"has_data"`
	AvailabilityPct      *float64   `json:"availability_pct"`
	CoveragePct          float64    `json:"coverage_pct"`
	AvailableSeconds     float64    `json:"available_seconds"`
	UnplannedDownSeconds float64    `json:"unplanned_down_seconds"`
	PlannedDownSeconds   float64    `json:"planned_down_seconds"`
	PausedSeconds        float64    `json:"paused_seconds"`
	UnknownSeconds       float64    `json:"unknown_seconds"`
	UntrackedSeconds     float64    `json:"untracked_seconds"`
	TimelineStart        *time.Time `json:"timeline_start"`
	OutageCount          int        `json:"outage_count"`
}

type SLAReportOutage struct {
	MonitorID        *uuid.UUID `json:"monitor_id"`
	MonitorName      string     `json:"monitor_name"`
	Start            time.Time  `json:"start"`
	End              time.Time  `json:"end"`
	DurationSeconds  float64    `json:"duration_seconds"`
	PlannedSeconds   float64    `json:"planned_seconds"`
	UnplannedSeconds float64    `json:"unplanned_seconds"`
	StartedBefore    bool       `json:"started_before"`
	Ongoing          bool       `json:"ongoing"`
	// CauseKind/CauseKey/CauseTitle are the outage's bucket in
	// SuspectedCauses (one of the SLACause* kinds; CauseKey is the catalogue
	// key for suspected causes, else the kind). Set only on per-monitor
	// outages with unplanned time, on reports computed since causes exist.
	CauseKind  string `json:"cause_kind,omitempty"`
	CauseKey   string `json:"cause_key,omitempty"`
	CauseTitle string `json:"cause_title,omitempty"`
}

// Suspected-cause buckets of an SLA report, in display order after the
// suspected (catalogue) causes.
const (
	// SLACauseSuspected: a named catalogue cause, from a ready triage or an
	// operator's label on it.
	SLACauseSuspected = "suspected"
	// SLACauseInconclusive: triage ran and found no supported cause, or an
	// operator confirmed there was none.
	SLACauseInconclusive = "inconclusive"
	// SLACausePending: the alert's triage was queued or running, or the
	// alert was still open and claimable, when the report was computed.
	SLACausePending = "pending"
	// SLACauseUntriaged: the overlapping alerts were never triaged (no row,
	// failed or skipped).
	SLACauseUntriaged = "untriaged"
	// SLACauseNoAlert: no availability alert of the monitor overlapped the
	// outage.
	SLACauseNoAlert = "no_alert"
)

// SLAReportSuspectedCauses rolls monitor outages up by suspected cause.
// Counts are monitor outages (a span on one monitor), not alerts, and
// UnplannedSeconds is summed per monitor, so it can exceed the service's
// downtime when monitors fail together.
type SLAReportSuspectedCauses struct {
	OutageCount int `json:"outage_count"`
	// TriagedOutageCount = suspected + inconclusive outages.
	TriagedOutageCount int     `json:"triaged_outage_count"`
	PendingOutageCount int     `json:"pending_outage_count"`
	UnplannedSeconds   float64 `json:"unplanned_seconds"`
	// Buckets: suspected causes by downtime, then inconclusive, pending,
	// untriaged, no_alert. Empty buckets are omitted; never null.
	Buckets []SLAReportCauseBucket `json:"buckets"`
}

type SLAReportCauseBucket struct {
	Kind string `json:"kind"`
	// Key is the catalogue rca key for suspected causes, else the kind.
	Key string `json:"key"`
	// Title is copied in when the report is computed, so an issued report
	// stays readable whatever the catalogue later calls the key.
	Title       string `json:"title"`
	OutageCount int    `json:"outage_count"`
	// AlertCount is the distinct availability alerts the outages were
	// attributed to; they can have triggered before the period.
	AlertCount       int     `json:"alert_count"`
	MonitorCount     int     `json:"monitor_count"`
	UnplannedSeconds float64 `json:"unplanned_seconds"`
	// ConfirmedOutageCount / CorrectedOutageCount: outages whose cause came
	// from an operator label that confirmed the model's answer, or replaced
	// it.
	ConfirmedOutageCount int `json:"confirmed_outage_count"`
	CorrectedOutageCount int `json:"corrected_outage_count"`
}

// SLAReportResponse covers detection-to-recovery (outages) and operator
// response (availability alerts triggered in the period).
type SLAReportResponse struct {
	OutageCount int `json:"outage_count"`
	// MTTRSeconds is the mean duration of outages that started and ended
	// inside the period with some unplanned time.
	MTTRSeconds       *float64 `json:"mttr_seconds"`
	AlertCount        int      `json:"alert_count"`
	AcknowledgedCount int      `json:"acknowledged_count"`
	MTTASeconds       *float64 `json:"mtta_seconds"`
}

// SLAReportRef is an issued report in a list.
type SLAReportRef struct {
	ID              uuid.UUID `json:"id"`
	SLAID           uuid.UUID `json:"sla_id"`
	PeriodKey       string    `json:"period_key"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	AvailabilityPct *float64  `json:"availability_pct"`
	TargetPct       float64   `json:"target_pct"`
	Met             *bool     `json:"met"`
	IssuedAt        time.Time `json:"issued_at"`
	IssuedBy        string    `json:"issued_by"`
}

type IssueSLAReportRequest struct {
	// Period key; defaults to the last closed period.
	Period string `json:"period"`
}
