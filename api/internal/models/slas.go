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
