package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

// ErrStatusPageNotFound is returned when a slug or ID resolves to no status
// page. Handlers match it with errors.Is to answer 404 instead of 500.
var ErrStatusPageNotFound = errors.New("status page not found")

// StatusPageData represents the public status page data
type StatusPageData struct {
	ID                 string                        `json:"id"`
	Slug               string                        `json:"slug"`
	Title              string                        `json:"title"`
	Description        *string                       `json:"description,omitempty"`
	LogoURL            *string                       `json:"logo_url,omitempty"`
	HasLogo            bool                          `json:"-"` // For template use only - true if LogoURL is set and non-empty
	PrimaryColor       *string                       `json:"primary_color,omitempty"`
	SecondaryColor     *string                       `json:"secondary_color,omitempty"`
	Sections           []StatusPageSectionData       `json:"sections,omitempty"`
	Monitors           []MonitorStatus               `json:"monitors"`
	Incidents          []StatusPageIncident          `json:"incidents,omitempty"`
	MaintenanceWindows []StatusPageMaintenanceWindow `json:"maintenance_windows,omitempty"`
	HasMaintenance     bool                          `json:"-"` // For template use only
	HasIssues          bool                          `json:"-"` // For template use only
	ShowIncidents      bool                          `json:"-"` // For template use only
	ShowUptimeHistory  bool                          `json:"-"` // For template use only
	ShowGlobalUptime   bool                          `json:"-"` // For template use only
	ShowFooter         bool                          `json:"-"` // For template use only
	CustomFooterText   *string                       `json:"-"` // For template use only
	DefaultTheme       string                        `json:"-"` // For template use only
	AllowThemeToggle   bool                          `json:"-"` // For template use only
	ShowMonitorTags    bool                          `json:"-"` // For template use only
	ShowMonitorURL     bool                          `json:"-"` // For template use only
	ShowMonitorUptime  bool                          `json:"-"` // For template use only
	ShowMonitorTLS     bool                          `json:"-"` // For template use only
	ShowLatencyCharts  bool                          `json:"-"` // For template use only
	ShowAgentMetrics   bool                          `json:"-"` // For template use only
	// PushNotificationsEnabled is the page's opt-in only; the render layer
	// ANDs it with "the deployment has VAPID keys" before showing anything.
	PushNotificationsEnabled bool `json:"-"` // For template use only
	// PushPublicKey is the deployment's VAPID application server key, injected
	// by the handler (the service layer has no config). Empty means the
	// deployment cannot send push, which disables the control regardless of
	// the page's own setting.
	PushPublicKey string `json:"-"` // For template use only
	// Tenant-authored branding injections (settings JSONB) applied to the
	// built-in template; custom templates can also reference them.
	CustomCSS        string `json:"-"` // For template use only
	CustomHeadHTML   string `json:"-"` // For template use only
	CustomFooterHTML string `json:"-"` // For template use only
	// Published custom template ("" = render with the built-in template).
	CustomTemplateSource  string `json:"-"` // For template use only
	CustomTemplateVersion int    `json:"-"` // For template use only
	// Uptime history for different time ranges
	UptimeHistory7   []DailyUptime  `json:"-"` // Last 7 days
	UptimeHistory1h  []MinuteUptime `json:"-"` // Last 1 hour (1-min buckets)
	UptimeHistory1   []HourlyUptime `json:"-"` // Last 24 hours (hourly buckets)
	UptimeHistory30  []DailyUptime  `json:"-"` // Last 30 days
	UptimeHistory90  []DailyUptime  `json:"-"` // Last 90 days
	UptimeHistory365 []DailyUptime  `json:"-"` // Last 1 year
}

type StatusPageSectionData struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Position int             `json:"position"`
	Monitors []MonitorStatus `json:"monitors"`
}

type StatusPageIncident struct {
	ID                 string                     `json:"id"`
	Title              string                     `json:"title"`
	Summary            string                     `json:"summary"`
	State              string                     `json:"state"`
	PublishedAt        time.Time                  `json:"published_at"`
	ResolvedAt         *time.Time                 `json:"resolved_at,omitempty"`
	AffectedComponents []string                   `json:"affected_components,omitempty"`
	Updates            []StatusPageIncidentUpdate `json:"updates,omitempty"`
}

type StatusPageIncidentUpdate struct {
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// StatusPageMaintenanceWindow is an active or upcoming maintenance window shown
// to public viewers, scoped to windows affecting monitors on this page.
type StatusPageMaintenanceWindow struct {
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	StartsAt         time.Time `json:"starts_at"`
	EndsAt           time.Time `json:"ends_at"`
	IsActive         bool      `json:"is_active"`
	AffectedMonitors []string  `json:"affected_monitors,omitempty"`
}

// MonitorStatus represents a monitor's status on a status page
type MonitorStatus struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	URL                string     `json:"url"`
	MonitorType        string     `json:"monitor_type"` // "http", "ping", "dns", "agent", "group", "push", "sip"
	Status             string     `json:"status"`       // "up", "down", "error", "unknown"
	Tags               []string   `json:"tags,omitempty"`
	LastCheckTime      *time.Time `json:"last_check_time,omitempty"`
	LastHTTPStatus     *int       `json:"last_http_status,omitempty"`
	LastLatency        *int       `json:"last_latency_ms,omitempty"`
	TLSDaysUntilExpiry *int       `json:"tls_days_until_expiry,omitempty"`
	TLSNotAfter        string     `json:"tls_not_after,omitempty"`
	// CertExpiresSoon is set when the monitor has an open tls_expiry alert:
	// the certificate is inside its configured warning window while the
	// endpoint itself is still up.
	CertExpiresSoon    bool                 `json:"cert_expires_soon,omitempty"`
	Uptime24h          *float64             `json:"uptime_24h,omitempty"`
	Uptime24hFormatted string               `json:"-"` // For template use only
	Uptime1h           *float64             `json:"uptime_1h,omitempty"`
	Uptime1hFormatted  string               `json:"-"` // For template use only
	AvgLatency1h       *float64             `json:"avg_latency_1h,omitempty"`
	AvgLatency24h      *float64             `json:"avg_latency_24h,omitempty"`
	HourlyUptime       []HourlyUptime       `json:"-"` // Per-component hourly uptime for 24h (kept for backward compat)
	LatencyHistory     []LatencyPoint       `json:"-"` // Latency data points for sparkline (24h default)
	LatencyHistory1h   []LatencyPoint       `json:"-"` // Latency data for 1h
	LatencyHistory30d  []LatencyPoint       `json:"-"` // Latency data for 30d
	LatencyHistory90d  []LatencyPoint       `json:"-"` // Latency data for 90d
	LatencyHistory365d []LatencyPoint       `json:"-"` // Latency data for 1y
	History            []CheckResultHistory `json:"history"`
	// Downtime periods for different time ranges (to show red shaded areas on charts)
	DowntimePeriods1h   []DowntimePeriod `json:"-"` // Downtime periods in last 1h
	DowntimePeriods24h  []DowntimePeriod `json:"-"` // Downtime periods in last 24h
	DowntimePeriods30d  []DowntimePeriod `json:"-"` // Downtime periods in last 30d
	DowntimePeriods90d  []DowntimePeriod `json:"-"` // Downtime periods in last 90d
	DowntimePeriods365d []DowntimePeriod `json:"-"` // Downtime periods in last 1y
	// Multi-range uptime history for each monitor
	UptimeHistory7d   []DailyUptime  `json:"-"` // Last 7 days
	UptimeHistory1h   []MinuteUptime `json:"-"` // Last 1 hour (1-min buckets)
	UptimeHistory24h  []HourlyUptime `json:"-"` // Last 24 hours (hourly buckets)
	UptimeHistory30d  []DailyUptime  `json:"-"` // Last 30 days
	UptimeHistory90d  []DailyUptime  `json:"-"` // Last 90 days
	UptimeHistory365d []DailyUptime  `json:"-"` // Last 1 year
	// Agent-specific metrics (only populated for agent monitors)
	AgentMetrics *AgentMetricsData `json:"-"`
	// Push-specific metrics (only populated for push monitors - dynamically detected)
	PushMetrics map[string]interface{} `json:"-"`
}

// AgentMetricsData represents the latest agent metrics for display
type AgentMetricsData struct {
	CPUPercent      float64   `json:"cpu_percent"`
	MemoryUsed      uint64    `json:"memory_used"`
	MemoryTotal     uint64    `json:"memory_total"`
	MemoryPercent   float64   `json:"-"` // Calculated
	DiskUsed        uint64    `json:"disk_used"`
	DiskTotal       uint64    `json:"disk_total"`
	DiskPercent     float64   `json:"-"` // Calculated
	NetworkBytesIn  uint64    `json:"network_bytes_in"`
	NetworkBytesOut uint64    `json:"network_bytes_out"`
	LoadAvg1        float64   `json:"load_avg_1"`
	LoadAvg5        float64   `json:"load_avg_5"`
	LoadAvg15       float64   `json:"load_avg_15"`
	ProcessCount    int       `json:"process_count"`
	Timestamp       time.Time `json:"timestamp"`
}

// MinuteUptime represents uptime percentage for one minute (the 1h view's buckets)
type MinuteUptime struct {
	Time   string  `json:"time"`
	Uptime float64 `json:"uptime"`
}

// LatencyPoint represents a latency data point for sparkline charts
type LatencyPoint struct {
	Timestamp time.Time `json:"timestamp"`
	LatencyMS int       `json:"latency_ms"`
	Time      string    `json:"-"` // Formatted time for template
	Unix      int64     `json:"-"` // Unix timestamp for chart positioning
}

// DowntimePeriod represents a continuous downtime period for displaying on charts
type DowntimePeriod struct {
	StartTime string `json:"start_time"` // Formatted start time for display
	EndTime   string `json:"end_time"`   // Formatted end time for display
	StartUnix int64  `json:"start_unix"` // Unix timestamp for positioning
	EndUnix   int64  `json:"end_unix"`   // Unix timestamp for positioning
}

// CheckResultHistory represents a check result in history
type CheckResultHistory struct {
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"` // "up", "down", "error"
	LatencyMS *int      `json:"latency_ms,omitempty"`
}

// DailyUptime represents uptime percentage for a single day
type DailyUptime struct {
	Date   string  `json:"date"`
	Uptime float64 `json:"uptime"`
}

// HourlyUptime represents uptime percentage for a single hour
type HourlyUptime struct {
	Hour   string  `json:"hour"`
	Uptime float64 `json:"uptime"`
}

// batchAnalyticsReader is the optional batched counterpart of sharedanalytics.Reader.
// *sharedanalytics.Repository implements it; when available the status page resolves all
// per-monitor long-range analytics with a constant number of queries instead of O(monitors).
type batchAnalyticsReader interface {
	GetScopeAnalyticsBatch(ctx context.Context, tenantID uuid.UUID, scopes []sharedanalytics.ScopeAnalyticsBatchRequest, ranges []sharedanalytics.Range, now time.Time) (map[uuid.UUID]map[sharedanalytics.Range]*sharedanalytics.Result, error)
}

// Service handles status page business logic
type Service struct {
	db             db.Querier
	analytics      sharedanalytics.Reader
	analyticsBatch batchAnalyticsReader
	presenters     map[string]monitorPresenter
	now            func() time.Time
}

// NewService creates a new status page service
func NewService(database db.Querier, analytics sharedanalytics.Reader) *Service {
	svc := &Service{
		db:         database,
		analytics:  analytics,
		presenters: newMonitorPresenters(),
		now:        func() time.Time { return time.Now().UTC() },
	}
	if batch, ok := analytics.(batchAnalyticsReader); ok {
		svc.analyticsBatch = batch
	}
	return svc
}
func (s *Service) GetTenantIDByStatusPageID(ctx context.Context, statusPageID uuid.UUID) (uuid.UUID, error) {
	query := `SELECT tenant_id FROM status_pages WHERE id = $1`
	var tenantID uuid.UUID
	if err := s.db.QueryRowContext(ctx, query, statusPageID).Scan(&tenantID); err != nil {
		if err == sql.ErrNoRows {
			return uuid.UUID{}, ErrStatusPageNotFound
		}
		return uuid.UUID{}, fmt.Errorf("failed to get tenant id: %w", err)
	}
	return tenantID, nil
}

// getPublishedTemplate returns the published custom template for a page, or
// ("", 0, nil) when the page renders with the built-in template. A missing
// status_page_templates table (migration not yet applied) also means "no
// custom template" — same tolerance as the section-table fallbacks.
func (s *Service) getPublishedTemplate(ctx context.Context, pageID uuid.UUID) (string, int, error) {
	var source string
	var version int
	err := s.db.QueryRowContext(ctx, `
		SELECT source, version
		FROM status_page_templates
		WHERE status_page_id = $1 AND status = 'published'
	`, pageID).Scan(&source, &version)
	if err != nil {
		if err == sql.ErrNoRows || isUndefinedTableError(err) {
			return "", 0, nil
		}
		return "", 0, err
	}
	return source, version, nil
}

// GetDraftTemplateBySlug resolves a slug to its page ID and draft template
// source. hasDraft is false when the page exists but has no draft.
func (s *Service) GetDraftTemplateBySlug(ctx context.Context, slug string) (pageID uuid.UUID, source string, hasDraft bool, err error) {
	var draftSource sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT sp.id, t.source
		FROM status_pages sp
		LEFT JOIN status_page_templates t
			ON t.status_page_id = sp.id AND t.status = 'draft'
		WHERE sp.slug = $1
	`, slug).Scan(&pageID, &draftSource)
	if err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, "", false, ErrStatusPageNotFound
		}
		if isUndefinedTableError(err) {
			// Templates table missing: resolve the page alone so previews
			// degrade to the live render instead of erroring.
			if err := s.db.QueryRowContext(ctx, `SELECT id FROM status_pages WHERE slug = $1`, slug).Scan(&pageID); err != nil {
				if err == sql.ErrNoRows {
					return uuid.Nil, "", false, ErrStatusPageNotFound
				}
				return uuid.Nil, "", false, fmt.Errorf("failed to get status page: %w", err)
			}
			return pageID, "", false, nil
		}
		return uuid.Nil, "", false, fmt.Errorf("failed to get draft template: %w", err)
	}
	return pageID, draftSource.String, draftSource.Valid, nil
}

// GetStatusPageBySlug retrieves a status page by slug
func (s *Service) GetStatusPageBySlug(ctx context.Context, slug string) (*StatusPageData, error) {
	// Get status page
	query := `
		SELECT id, tenant_id, slug, title, description, logo_url, primary_color, secondary_color, settings
		FROM status_pages
		WHERE slug = $1
	`

	var pageID, tenantID uuid.UUID
	var page StatusPageData
	var settingsJSON []byte
	err := s.db.QueryRowContext(ctx, query, slug).Scan(
		&pageID, &tenantID, &page.Slug, &page.Title,
		&page.Description, &page.LogoURL, &page.PrimaryColor, &page.SecondaryColor, &settingsJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("failed to get status page: %w", err)
	}

	page.ID = pageID.String()

	// Set HasLogo flag for template use
	page.HasLogo = page.LogoURL != nil && *page.LogoURL != ""

	sections, err := s.GetStatusPageSections(ctx, pageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitors: %w", err)
	}
	page.Sections = sections
	page.Monitors = flattenStatusPageSections(sections)
	page.Incidents, err = s.loadPublishedIncidents(ctx, pageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get incidents: %w", err)
	}
	page.MaintenanceWindows, err = s.loadMaintenanceWindows(ctx, pageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get maintenance windows: %w", err)
	}
	page.HasMaintenance = len(page.MaintenanceWindows) > 0

	// Determine if there are any issues
	for _, monitor := range page.Monitors {
		if monitor.Status == "down" || monitor.Status == "error" {
			page.HasIssues = true
			break
		}
	}

	// Set default visibility flags (can be made configurable later)
	page.ShowIncidents = true

	settings := parseStatusPageSettings(settingsJSON)
	page.ShowGlobalUptime = settings.ShowGlobalUptime
	page.ShowUptimeHistory = settings.ShowGlobalUptime // Backward-compatible flag used by some template parts
	page.ShowFooter = settings.ShowFooter
	page.CustomFooterText = settings.FooterText
	page.DefaultTheme = settings.DefaultTheme
	page.AllowThemeToggle = settings.AllowThemeToggle
	page.ShowMonitorTags = settings.ShowMonitorTags
	page.ShowMonitorURL = settings.ShowMonitorURL
	page.ShowMonitorUptime = settings.ShowMonitorUptime
	page.ShowMonitorTLS = settings.ShowMonitorTLS
	page.ShowLatencyCharts = settings.ShowLatencyCharts
	page.ShowAgentMetrics = settings.ShowAgentMetrics
	page.PushNotificationsEnabled = settings.EnablePushNotifications
	page.CustomCSS = settings.CustomCSS
	page.CustomHeadHTML = settings.CustomHeadHTML
	page.CustomFooterHTML = settings.CustomFooterHTML

	if source, version, err := s.getPublishedTemplate(ctx, pageID); err != nil {
		return nil, fmt.Errorf("failed to get custom template: %w", err)
	} else {
		page.CustomTemplateSource = source
		page.CustomTemplateVersion = version
	}

	// Fetch only short-range and rollup-backed global uptime to keep public page responses bounded.
	// Rollup-backed ranges (7/30/90/365d) avoid large scans over check_results.
	// The 1h strip reads raw check_results (one-minute buckets), 24h the hourly rollups plus
	// the raw tail. The operational monitor set is resolved once for all ranges.
	if globalMonitorIDs, err := s.resolveStatusPageOperationalMonitorIDs(ctx, pageID, tenantID); err == nil {
		if uptimeHistory1h, err := s.GetGlobalMinuteUptime(ctx, globalMonitorIDs, tenantID); err == nil {
			page.UptimeHistory1h = uptimeHistory1h
		}
		if uptimeHistory1, err := s.GetGlobalHourlyUptime(ctx, globalMonitorIDs, tenantID); err == nil {
			page.UptimeHistory1 = uptimeHistory1
		}
		s.applyGlobalLongRangeAnalytics(ctx, &page, tenantID, globalMonitorIDs)
	}

	return &page, nil
}
