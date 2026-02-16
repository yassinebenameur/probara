package models

import (
	"time"

	"github.com/google/uuid"
)

// StatusPageSettings represents optional rendering settings for the public status page.
// All fields are optional for PATCH semantics.
type StatusPageSettings struct {
	ShowMonitorTags   *bool   `json:"show_monitor_tags,omitempty"`
	ShowMonitorURL    *bool   `json:"show_monitor_url,omitempty"`
	ShowMonitorUptime *bool   `json:"show_monitor_uptime,omitempty"`
	ShowMonitorTLS    *bool   `json:"show_monitor_tls,omitempty"`
	ShowLatencyCharts *bool   `json:"show_latency_charts,omitempty"`
	ShowAgentMetrics  *bool   `json:"show_agent_metrics,omitempty"`
	ShowGlobalUptime  *bool   `json:"show_global_uptime,omitempty"`
	ShowFooter        *bool   `json:"show_footer,omitempty"`
	FooterText        *string `json:"footer_text,omitempty"`
}

// StatusPage represents a status page
type StatusPage struct {
	ID             uuid.UUID           `json:"id"`
	TenantID       uuid.UUID           `json:"tenant_id"`
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Description    *string             `json:"description,omitempty"`
	LogoURL        *string             `json:"logo_url,omitempty"`
	PrimaryColor   *string             `json:"primary_color,omitempty"`
	SecondaryColor *string             `json:"secondary_color,omitempty"`
	MonitorIDs     []uuid.UUID         `json:"monitor_ids,omitempty"`
	Settings       *StatusPageSettings `json:"settings,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

// CreateStatusPageRequest represents a request to create a status page
type CreateStatusPageRequest struct {
	Slug                string              `json:"slug"`
	Title               string              `json:"title"`
	Description         *string             `json:"description,omitempty"`
	LogoURL             *string             `json:"logo_url,omitempty"`
	PrimaryColor        *string             `json:"primary_color,omitempty"`
	SecondaryColor      *string             `json:"secondary_color,omitempty"`
	MonitorIDs          []string            `json:"monitor_ids,omitempty"`
	MonitorDisplayNames map[string]string   `json:"monitor_display_names,omitempty"`
	Settings            *StatusPageSettings `json:"settings,omitempty"`
}

// UpdateStatusPageRequest represents a request to update a status page
type UpdateStatusPageRequest struct {
	Slug                *string             `json:"slug,omitempty"`
	Title               *string             `json:"title,omitempty"`
	Description         *string             `json:"description,omitempty"`
	LogoURL             *string             `json:"logo_url,omitempty"`
	PrimaryColor        *string             `json:"primary_color,omitempty"`
	SecondaryColor      *string             `json:"secondary_color,omitempty"`
	MonitorIDs          *[]string           `json:"monitor_ids,omitempty"`
	MonitorDisplayNames *map[string]string  `json:"monitor_display_names,omitempty"`
	Settings            *StatusPageSettings `json:"settings,omitempty"`
}

// StatusPageListResponse represents a paginated list of status pages
type StatusPageListResponse struct {
	Items    []StatusPage `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int          `json:"total"`
}
