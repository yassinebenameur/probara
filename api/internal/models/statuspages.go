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
	// EnablePushNotifications offers visitors a browser-notification opt-in on
	// the public page. Off by default: it asks visitors for a permission the
	// operator should choose to request. Effective only when the deployment
	// also configures a VAPID keypair.
	EnablePushNotifications *bool `json:"enable_push_notifications,omitempty"`
	// Tenant-authored branding injected verbatim into the built-in template
	// (custom CSS after the base styles, extra <head> markup, and markup
	// before </body>). Lighter-weight customization than a full template.
	CustomCSS        *string `json:"custom_css,omitempty"`
	CustomHeadHTML   *string `json:"custom_head_html,omitempty"`
	CustomFooterHTML *string `json:"custom_footer_html,omitempty"`
	// CSPMode is the public page's Content-Security-Policy mode: "strict",
	// "compatible", "off", or "" for automatic (compatible when the page has
	// custom CSS, custom head/footer HTML or a published custom template,
	// else strict). See shared/securityheaders.
	CSPMode *string `json:"csp_mode,omitempty"`
	// SEOTitle and SEODescription override the public page's <title> and
	// search/link-preview description (up to MaxSEOTitleLength and
	// MaxSEODescriptionLength characters). "" clears an override: the page
	// then uses "<title> · Status" and its description, else a fixed
	// sentence naming the page.
	SEOTitle       *string `json:"seo_title,omitempty"`
	SEODescription *string `json:"seo_description,omitempty"`
	// AllowSearchIndexing lets search engines index the page. On by
	// default; off sends noindex in a robots meta tag and X-Robots-Tag.
	AllowSearchIndexing *bool `json:"allow_search_indexing,omitempty"`
	// EnableEmailSubscriptions shows a subscribe form on the public page:
	// visitors confirm their address by a link (double opt-in) and are then
	// mailed when an incident is published, updated or resolved there and
	// when maintenance is scheduled (api/internal/services/pagesubscribers).
	// Off by default. Turning it on needs the API's mailer, APP_BASE_URL,
	// STATUS_PAGE_BASE_URL and PROBARA_SECRETS_KEY; it has no effect on
	// password and private pages.
	EnableEmailSubscriptions *bool `json:"enable_email_subscriptions,omitempty"`
	// AutoPublishIncidents publishes a monitor's outage to the page when a
	// monitor the page shows has been down for AutoPublishDelay (outside
	// maintenance, not explained by a dependency, not recovering), and
	// resolves it when the monitor recovers. Off by default.
	AutoPublishIncidents *bool `json:"auto_publish_incidents,omitempty"`
}

// Bounds of the SEO overrides. Search results show about 60 and 160
// characters; link previews a little more.
const (
	MaxSEOTitleLength       = 120
	MaxSEODescriptionLength = 300
)

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
	// Visibility is who may view the page: public, unlisted, password or
	// private (shared/pageaccess). A column, not a setting.
	Visibility string `json:"visibility"`
	// HasPassword reports whether a visitor password is stored; the
	// password itself is write-only.
	HasPassword bool `json:"has_password"`
	// PublicURL is the page's URL on its canonical custom domain (the oldest
	// verified one, shared/pagedomains.PrimaryHostSQL); omitted while it has
	// none, when the page lives only on the platform host.
	PublicURL string `json:"public_url,omitempty"`
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
	// Visibility defaults to public.
	Visibility *string `json:"visibility,omitempty"`
	// Password is the visitor password, required for password visibility.
	Password *string `json:"password,omitempty"`
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
	// Visibility: absent keeps the stored value.
	Visibility *string `json:"visibility,omitempty"`
	// Password is write-only: absent keeps the stored password, "" clears
	// it, anything else replaces it. Setting or clearing it signs every
	// password visitor out.
	Password *string `json:"password,omitempty"`
}

// StatusPageListResponse represents a paginated list of status pages
type StatusPageListResponse struct {
	Items    []StatusPage `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int          `json:"total"`
}

// StatusPageAccessToken is a kiosk token of a status page. It opens a
// password or private page; the token itself is shown once, at creation, and
// only its hash is stored.
type StatusPageAccessToken struct {
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
	// Who created it: an operator or an API key (nil once deleted).
	CreatedByUserID   *uuid.UUID `json:"created_by_user_id,omitempty"`
	CreatedByAPIKeyID *uuid.UUID `json:"created_by_api_key_id,omitempty"`
	// CreatedBy is the operator's username or the API key's name, for
	// display; empty when the creator no longer exists.
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// CreateStatusPageAccessTokenRequest names a new kiosk token.
type CreateStatusPageAccessTokenRequest struct {
	Label string `json:"label"`
}

// CreatedStatusPageAccessToken is the one response that carries the token.
type CreatedStatusPageAccessToken struct {
	StatusPageAccessToken
	Token string `json:"token"`
}

// StatusPageAccessTokenListResponse lists a page's kiosk tokens, newest
// first, revoked ones included.
type StatusPageAccessTokenListResponse struct {
	Items []StatusPageAccessToken `json:"items"`
}
