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
	DefaultTheme      *string `json:"default_theme,omitempty"`
	AllowThemeToggle  *bool   `json:"allow_theme_toggle,omitempty"`
	// Tenant-authored branding injected verbatim into the built-in template
	// (custom CSS after the base styles, extra <head> markup, and markup
	// before </body>). Lighter-weight customization than a full template.
	CustomCSS        *string `json:"custom_css,omitempty"`
	CustomHeadHTML   *string `json:"custom_head_html,omitempty"`
	CustomFooterHTML *string `json:"custom_footer_html,omitempty"`
}

// StatusPageTemplateVersion summarizes one stored custom-template row.
type StatusPageTemplateVersion struct {
	Version     *int       `json:"version,omitempty"` // nil for drafts
	Status      string     `json:"status"`            // draft | published | archived
	SizeBytes   int        `json:"size_bytes"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// StatusPageTemplateState is the full template-editing state for a page.
type StatusPageTemplateState struct {
	// HasCustom is true when a custom template is published (the public page
	// is not rendering the built-in template).
	HasCustom        bool                        `json:"has_custom"`
	PublishedVersion *int                        `json:"published_version,omitempty"`
	DraftSource      *string                     `json:"draft_source,omitempty"`
	DraftUpdatedAt   *time.Time                  `json:"draft_updated_at,omitempty"`
	Versions         []StatusPageTemplateVersion `json:"versions"`
	// PreviewToken authorizes GET /public/status/{slug}/preview/draft when the
	// deployment configures STATUS_PAGE_PREVIEW_SECRET; empty otherwise.
	PreviewToken string `json:"preview_token,omitempty"`
	MaxSizeBytes int    `json:"max_size_bytes"`
}

// SaveStatusPageTemplateDraftRequest carries a draft template source.
type SaveStatusPageTemplateDraftRequest struct {
	Source string `json:"source"`
}

// StatusPageLibraryTemplate is a named, tenant-scoped reusable template.
// Library entries are source storage only: applying one to a page copies its
// source into that page's draft.
type StatusPageLibraryTemplate struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	Source      string    `json:"source,omitempty"` // omitted in list responses
	SizeBytes   int       `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateStatusPageLibraryTemplateRequest creates a library template.
type CreateStatusPageLibraryTemplateRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Source      string  `json:"source"`
}

// UpdateStatusPageLibraryTemplateRequest partially updates a library template.
type UpdateStatusPageLibraryTemplateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Source      *string `json:"source,omitempty"`
}

// StatusPageLibraryTemplateListResponse lists a tenant's library templates.
type StatusPageLibraryTemplateListResponse struct {
	Items []StatusPageLibraryTemplate `json:"items"`
	Total int                         `json:"total"`
}

// RevertStatusPageTemplateRequest republishes an archived template version.
type RevertStatusPageTemplateRequest struct {
	Version int `json:"version"`
}

type StatusPageSectionMonitor struct {
	MonitorID   string  `json:"monitor_id"`
	DisplayName *string `json:"display_name,omitempty"`
	Position    int     `json:"position,omitempty"`
}

type StatusPageSection struct {
	ID        string                     `json:"id,omitempty"`
	Title     string                     `json:"title"`
	Position  int                        `json:"position,omitempty"`
	Monitors  []StatusPageSectionMonitor `json:"monitors,omitempty"`
	CreatedAt *time.Time                 `json:"created_at,omitempty"`
	UpdatedAt *time.Time                 `json:"updated_at,omitempty"`
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
	Sections       []StatusPageSection `json:"sections,omitempty"`
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
	Sections            []StatusPageSection `json:"sections,omitempty"`
	Settings            *StatusPageSettings `json:"settings,omitempty"`
}

// UpdateStatusPageRequest represents a request to update a status page
type UpdateStatusPageRequest struct {
	Slug                *string              `json:"slug,omitempty"`
	Title               *string              `json:"title,omitempty"`
	Description         *string              `json:"description,omitempty"`
	LogoURL             *string              `json:"logo_url,omitempty"`
	PrimaryColor        *string              `json:"primary_color,omitempty"`
	SecondaryColor      *string              `json:"secondary_color,omitempty"`
	MonitorIDs          *[]string            `json:"monitor_ids,omitempty"`
	MonitorDisplayNames *map[string]string   `json:"monitor_display_names,omitempty"`
	Sections            *[]StatusPageSection `json:"sections,omitempty"`
	Settings            *StatusPageSettings  `json:"settings,omitempty"`
}

// StatusPageListResponse represents a paginated list of status pages
type StatusPageListResponse struct {
	Items    []StatusPage `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int          `json:"total"`
}
