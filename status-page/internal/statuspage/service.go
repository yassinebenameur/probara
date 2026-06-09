package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

// StatusPageData represents the public status page data
type StatusPageData struct {
	ID                string                  `json:"id"`
	Slug              string                  `json:"slug"`
	Title             string                  `json:"title"`
	Description       *string                 `json:"description,omitempty"`
	LogoURL           *string                 `json:"logo_url,omitempty"`
	HasLogo           bool                    `json:"-"` // For template use only - true if LogoURL is set and non-empty
	PrimaryColor      *string                 `json:"primary_color,omitempty"`
	SecondaryColor    *string                 `json:"secondary_color,omitempty"`
	Sections          []StatusPageSectionData `json:"sections,omitempty"`
	Monitors          []MonitorStatus         `json:"monitors"`
	Incidents         []StatusPageIncident    `json:"incidents,omitempty"`
	HasIssues         bool                    `json:"-"` // For template use only
	ShowIncidents     bool                    `json:"-"` // For template use only
	ShowUptimeHistory bool                    `json:"-"` // For template use only
	ShowGlobalUptime  bool                    `json:"-"` // For template use only
	ShowFooter        bool                    `json:"-"` // For template use only
	CustomFooterText  *string                 `json:"-"` // For template use only
	DefaultTheme      string                  `json:"-"` // For template use only
	AllowThemeToggle  bool                    `json:"-"` // For template use only
	ShowMonitorTags   bool                    `json:"-"` // For template use only
	ShowMonitorURL    bool                    `json:"-"` // For template use only
	ShowMonitorUptime bool                    `json:"-"` // For template use only
	ShowMonitorTLS    bool                    `json:"-"` // For template use only
	ShowLatencyCharts bool                    `json:"-"` // For template use only
	ShowAgentMetrics  bool                    `json:"-"` // For template use only
	// Uptime history for different time ranges
	UptimeHistory7   []DailyUptime  `json:"-"` // Last 7 days
	UptimeHistory1h  []MinuteUptime `json:"-"` // Last 1 hour (5-min buckets)
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

// MonitorStatus represents a monitor's status on a status page
type MonitorStatus struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	URL                string               `json:"url"`
	MonitorType        string               `json:"monitor_type"` // "http", "ping", "dns", "agent", "group", "push", "sip"
	Status             string               `json:"status"`       // "up", "down", "error", "unknown"
	Tags               []string             `json:"tags,omitempty"`
	LastCheckTime      *time.Time           `json:"last_check_time,omitempty"`
	LastHTTPStatus     *int                 `json:"last_http_status,omitempty"`
	LastLatency        *int                 `json:"last_latency_ms,omitempty"`
	TLSDaysUntilExpiry *int                 `json:"tls_days_until_expiry,omitempty"`
	TLSNotAfter        string               `json:"tls_not_after,omitempty"`
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
	UptimeHistory1h   []MinuteUptime `json:"-"` // Last 1 hour (5-min buckets)
	UptimeHistory24h  []HourlyUptime `json:"-"` // Last 24 hours (hourly buckets)
	UptimeHistory30d  []DailyUptime  `json:"-"` // Last 30 days
	UptimeHistory90d  []DailyUptime  `json:"-"` // Last 90 days
	UptimeHistory365d []DailyUptime  `json:"-"` // Last 1 year
	// Agent-specific metrics (only populated for agent monitors)
	AgentMetrics *AgentMetricsData `json:"-"`
	// Push-specific metrics (only populated for push monitors - dynamically detected)
	PushMetrics map[string]interface{} `json:"-"`
}

type httpMetricsEnvelope struct {
	HTTP *httpMetrics `json:"http,omitempty"`
}

type httpMetrics struct {
	TLS *httpTLSInfo `json:"tls,omitempty"`
}

type httpTLSInfo struct {
	NotAfter        string `json:"not_after,omitempty"`
	DaysUntilExpiry *int   `json:"days_until_expiry,omitempty"`
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

// MinuteUptime represents uptime percentage for a time bucket (used for 1h view with 5-min buckets)
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

type statusPageSettingsPatch struct {
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
}

type statusPageSettingsStored struct {
	ShowMonitorTags   bool
	ShowMonitorURL    bool
	ShowMonitorUptime bool
	ShowMonitorTLS    bool
	ShowLatencyCharts bool
	ShowAgentMetrics  bool
	ShowGlobalUptime  bool
	ShowFooter        bool
	FooterText        *string
	DefaultTheme      string
	AllowThemeToggle  bool
}

func defaultStatusPageSettings() statusPageSettingsStored {
	return statusPageSettingsStored{
		ShowMonitorTags:   true,
		ShowMonitorURL:    true,
		ShowMonitorUptime: true,
		ShowMonitorTLS:    true,
		ShowLatencyCharts: true,
		ShowAgentMetrics:  true,
		ShowGlobalUptime:  true,
		ShowFooter:        true,
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
	}
}

func parseStatusPageSettings(settingsJSON []byte) statusPageSettingsStored {
	stored := defaultStatusPageSettings()
	if len(settingsJSON) == 0 {
		return stored
	}
	var patch statusPageSettingsPatch
	if err := json.Unmarshal(settingsJSON, &patch); err != nil {
		return stored
	}
	if patch.ShowMonitorTags != nil {
		stored.ShowMonitorTags = *patch.ShowMonitorTags
	}
	if patch.ShowMonitorURL != nil {
		stored.ShowMonitorURL = *patch.ShowMonitorURL
	}
	if patch.ShowMonitorUptime != nil {
		stored.ShowMonitorUptime = *patch.ShowMonitorUptime
	}
	if patch.ShowMonitorTLS != nil {
		stored.ShowMonitorTLS = *patch.ShowMonitorTLS
	}
	if patch.ShowLatencyCharts != nil {
		stored.ShowLatencyCharts = *patch.ShowLatencyCharts
	}
	if patch.ShowAgentMetrics != nil {
		stored.ShowAgentMetrics = *patch.ShowAgentMetrics
	}
	if patch.ShowGlobalUptime != nil {
		stored.ShowGlobalUptime = *patch.ShowGlobalUptime
	}
	if patch.ShowFooter != nil {
		stored.ShowFooter = *patch.ShowFooter
	}
	if patch.FooterText != nil {
		v := strings.TrimSpace(*patch.FooterText)
		if v == "" {
			stored.FooterText = nil
		} else {
			stored.FooterText = &v
		}
	}
	if patch.DefaultTheme != nil {
		switch strings.ToLower(strings.TrimSpace(*patch.DefaultTheme)) {
		case "light":
			stored.DefaultTheme = "light"
		default:
			stored.DefaultTheme = "dark"
		}
	}
	if patch.AllowThemeToggle != nil {
		stored.AllowThemeToggle = *patch.AllowThemeToggle
	}
	return stored
}

func (s *Service) GetTenantIDByStatusPageID(ctx context.Context, statusPageID uuid.UUID) (uuid.UUID, error) {
	query := `SELECT tenant_id FROM status_pages WHERE id = $1`
	var tenantID uuid.UUID
	if err := s.db.QueryRowContext(ctx, query, statusPageID).Scan(&tenantID); err != nil {
		if err == sql.ErrNoRows {
			return uuid.UUID{}, fmt.Errorf("status page not found")
		}
		return uuid.UUID{}, fmt.Errorf("failed to get tenant id: %w", err)
	}
	return tenantID, nil
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
			return nil, fmt.Errorf("status page not found")
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

	// Fetch only short-range and rollup-backed global uptime to keep public page responses bounded.
	// Rollup-backed ranges (7/30/90/365d) avoid large scans over check_results.
	if uptimeHistory1h, err := s.GetGlobal5MinuteUptime(ctx, pageID, tenantID); err == nil {
		page.UptimeHistory1h = uptimeHistory1h
	}
	if uptimeHistory1, err := s.GetGlobalHourlyUptime(ctx, pageID, tenantID); err == nil {
		page.UptimeHistory1 = uptimeHistory1
	}
	if globalMonitorIDs, err := s.resolveStatusPageOperationalMonitorIDs(ctx, pageID, tenantID); err == nil {
		s.applyGlobalLongRangeAnalytics(ctx, &page, tenantID, globalMonitorIDs)
	}

	return &page, nil
}

func (s *Service) loadPublishedIncidents(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageIncident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.title, COALESCE(i.summary, ''), i.state, isp.published_at, i.resolved_at
		FROM incident_status_page_publications isp
		JOIN incidents i ON i.id = isp.incident_id AND i.tenant_id = isp.tenant_id
		WHERE isp.status_page_id = $1 AND isp.tenant_id = $2 AND isp.unpublished_at IS NULL
		ORDER BY CASE WHEN i.state = 'resolved' THEN 1 ELSE 0 END, i.updated_at DESC, isp.published_at DESC
	`, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query published incidents: %w", err)
	}
	defer rows.Close()

	incidents := make([]StatusPageIncident, 0)
	for rows.Next() {
		var incident StatusPageIncident
		var incidentID uuid.UUID
		var resolvedAt sql.NullTime
		if err := rows.Scan(&incidentID, &incident.Title, &incident.Summary, &incident.State, &incident.PublishedAt, &resolvedAt); err != nil {
			return nil, fmt.Errorf("scan published incident: %w", err)
		}
		incident.ID = incidentID.String()
		if resolvedAt.Valid {
			resolvedTime := resolvedAt.Time.UTC()
			incident.ResolvedAt = &resolvedTime
		}

		incident.AffectedComponents, err = s.loadIncidentAffectedComponents(ctx, incidentID, statusPageID)
		if err != nil {
			return nil, err
		}
		incident.Updates, err = s.loadIncidentPublicUpdates(ctx, tenantID, incidentID)
		if err != nil {
			return nil, err
		}

		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published incidents: %w", err)
	}

	return incidents, nil
}

func (s *Service) loadIncidentAffectedComponents(ctx context.Context, incidentID, statusPageID uuid.UUID) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.name
		FROM incident_status_page_monitors ispm
		JOIN monitors m ON m.id = ispm.monitor_id
		WHERE ispm.incident_id = $1 AND ispm.status_page_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name ASC
	`, incidentID, statusPageID)
	if err != nil {
		return nil, fmt.Errorf("query incident affected components: %w", err)
	}
	defer rows.Close()

	components := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan incident affected component: %w", err)
		}
		if strings.TrimSpace(name) != "" {
			components = append(components, strings.TrimSpace(name))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident affected components: %w", err)
	}

	return components, nil
}

func (s *Service) loadIncidentPublicUpdates(ctx context.Context, tenantID, incidentID uuid.UUID) ([]StatusPageIncidentUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message, created_at
		FROM incident_timeline_entries
		WHERE tenant_id = $1 AND incident_id = $2 AND entry_type = 'public_update'
		ORDER BY created_at DESC, id DESC
	`, tenantID, incidentID)
	if err != nil {
		return nil, fmt.Errorf("query incident public updates: %w", err)
	}
	defer rows.Close()

	updates := make([]StatusPageIncidentUpdate, 0)
	for rows.Next() {
		var update StatusPageIncidentUpdate
		if err := rows.Scan(&update.Message, &update.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan incident public update: %w", err)
		}
		update.Message = strings.TrimSpace(update.Message)
		if update.Message != "" {
			updates = append(updates, update)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident public updates: %w", err)
	}

	return updates, nil
}

// GetGlobal5MinuteUptime calculates 5-minute bucket uptime across all monitors in a status page for the last 1 hour
func (s *Service) GetGlobal5MinuteUptime(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	monitorIDs, err := s.resolveStatusPageOperationalMonitorIDs(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []MinuteUptime{}, nil
	}

	query := `
		WITH buckets AS (
			SELECT generate_series(
				date_trunc('minute', NOW() - INTERVAL '55 minutes') - (EXTRACT(minute FROM NOW())::int % 5) * INTERVAL '1 minute',
				date_trunc('minute', NOW()),
				INTERVAL '5 minutes'
			) AS bucket
		),
		bucket_stats AS (
			SELECT 
				date_trunc('minute', cr.created_at) - (EXTRACT(minute FROM cr.created_at)::int % 5) * INTERVAL '1 minute' AS bucket,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '1 hour'
			GROUP BY 1
		)
		SELECT 
			b.bucket,
			COALESCE(bs.total, 0) AS total,
			COALESCE(bs.successful, 0) AS successful
		FROM buckets b
		LEFT JOIN bucket_stats bs ON b.bucket = bs.bucket
		ORDER BY b.bucket
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query global 5-minute uptime: %w", err)
	}
	defer rows.Close()

	var result []MinuteUptime
	for rows.Next() {
		var bucket time.Time
		var total, successful int

		if err := rows.Scan(&bucket, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan global 5-minute uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, MinuteUptime{
			Time:   bucket.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating global 5-minute uptime: %w", err)
	}

	return result, nil
}

// GetStatusPageSections loads the page's sections with their monitors fully populated
// (status, uptime, history and analytics). Loading happens in two phases: cheap section
// "skeletons" first, then a single batched metric pass across every monitor on the page.
func (s *Service) GetStatusPageSections(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	sections, err := s.loadStatusPageSectionSkeletons(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := s.populatePageMonitors(ctx, tenantID, sections); err != nil {
		return nil, err
	}
	return sections, nil
}

func (s *Service) loadStatusPageSectionSkeletons(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	type sectionRow struct {
		ID       uuid.UUID
		Title    string
		Position int
	}

	sectionRows, err := s.db.QueryContext(ctx, `
		SELECT id, title, position
		FROM status_page_sections
		WHERE status_page_id = $1
		ORDER BY position ASC, created_at ASC, id ASC
	`, statusPageID)
	if err != nil {
		if !isUndefinedTableError(err) {
			return nil, fmt.Errorf("failed to query status page sections: %w", err)
		}
		return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
	}
	defer sectionRows.Close()

	sections := make([]StatusPageSectionData, 0)
	for sectionRows.Next() {
		var row sectionRow
		if err := sectionRows.Scan(&row.ID, &row.Title, &row.Position); err != nil {
			return nil, fmt.Errorf("failed to scan status page section: %w", err)
		}
		monitors, err := s.getStatusPageSectionMonitors(ctx, row.ID, tenantID)
		if err != nil {
			if isUndefinedTableError(err) {
				return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
			}
			return nil, err
		}
		sections = append(sections, StatusPageSectionData{
			ID:       row.ID.String(),
			Title:    strings.TrimSpace(row.Title),
			Position: row.Position,
			Monitors: monitors,
		})
	}
	if err := sectionRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status page sections: %w", err)
	}
	if len(sections) == 0 {
		return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
	}
	return sections, nil
}

// GetStatusPageMonitors retrieves monitors for a status page with their status.
func (s *Service) GetStatusPageMonitors(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]MonitorStatus, error) {
	sections, err := s.GetStatusPageSections(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	return flattenStatusPageSections(sections), nil
}

func (s *Service) getStatusPageSectionMonitors(ctx context.Context, sectionID, tenantID uuid.UUID) ([]MonitorStatus, error) {
	query := `
		SELECT m.id, m.name, spsm.display_name, m.type, m.config, m.tags
		FROM monitors m
		INNER JOIN status_page_section_monitors spsm ON m.id = spsm.monitor_id
		WHERE spsm.section_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY spsm.position ASC, m.name
	`
	rows, err := s.db.QueryContext(ctx, query, sectionID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query section monitors: %w", err)
	}
	defer rows.Close()

	return s.scanMonitorStatuses(ctx, rows, tenantID)
}

func (s *Service) loadLegacyStatusPageSections(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.name, spm.display_name, m.type, m.config, m.tags
		FROM monitors m
		INNER JOIN status_page_monitors spm ON m.id = spm.monitor_id
		WHERE spm.status_page_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY spm.position ASC, m.name
	`, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query legacy monitors: %w", err)
	}
	defer rows.Close()

	monitors, err := s.scanMonitorStatuses(ctx, rows, tenantID)
	if err != nil {
		return nil, err
	}
	if len(monitors) == 0 {
		return nil, nil
	}
	return []StatusPageSectionData{{
		ID:       "",
		Title:    "Services",
		Position: 0,
		Monitors: monitors,
	}}, nil
}

func (s *Service) scanMonitorStatuses(ctx context.Context, rows *sql.Rows, tenantID uuid.UUID) ([]MonitorStatus, error) {
	monitors := make([]MonitorStatus, 0)
	for rows.Next() {
		var monitor MonitorStatus
		var monitorID uuid.UUID
		var displayName sql.NullString
		var monitorType string
		var configJSON []byte
		var tags pq.StringArray
		if err := rows.Scan(&monitorID, &monitor.Name, &displayName, &monitorType, &configJSON, &tags); err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}
		monitor.ID = monitorID.String()
		monitor.MonitorType = monitorType
		monitor.Tags = []string(tags)
		presenter := s.monitorPresenter(monitorType)

		if displayName.Valid && strings.TrimSpace(displayName.String) != "" {
			monitor.Name = strings.TrimSpace(displayName.String)
		}

		presenter.Configure(&monitor, configJSON)
		// Metrics (status, uptime, history, analytics) are populated in a single batched
		// pass by populatePageMonitors after all section skeletons are loaded. Doing it here,
		// per monitor, was the source of the N+1 query storm that timed out large pages.
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitors: %w", err)
	}
	return monitors, nil
}

func flattenStatusPageSections(sections []StatusPageSectionData) []MonitorStatus {
	monitors := make([]MonitorStatus, 0)
	for _, section := range sections {
		monitors = append(monitors, section.Monitors...)
	}
	return monitors
}

func (s *Service) listStatusPageMonitorIDs(ctx context.Context, statusPageID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT spsm.monitor_id
		FROM status_page_sections sps
		JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
		WHERE sps.status_page_id = $1
		ORDER BY sps.position ASC, spsm.position ASC, spsm.monitor_id ASC
	`, statusPageID)
	if err != nil {
		if !isUndefinedTableError(err) {
			return nil, fmt.Errorf("failed to list section monitor ids: %w", err)
		}
		return s.listLegacyStatusPageMonitorIDs(ctx, statusPageID)
	}
	defer rows.Close()

	monitorIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("failed to scan section monitor id: %w", err)
		}
		monitorIDs = append(monitorIDs, monitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating section monitor ids: %w", err)
	}
	if len(monitorIDs) == 0 {
		return s.listLegacyStatusPageMonitorIDs(ctx, statusPageID)
	}
	return dedupeMonitorIDs(monitorIDs), nil
}

func (s *Service) listLegacyStatusPageMonitorIDs(ctx context.Context, statusPageID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id
		FROM status_page_monitors
		WHERE status_page_id = $1
		ORDER BY position ASC, monitor_id ASC
	`, statusPageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list legacy status page monitor ids: %w", err)
	}
	defer rows.Close()

	monitorIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("failed to scan legacy status page monitor id: %w", err)
		}
		monitorIDs = append(monitorIDs, monitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating legacy status page monitor ids: %w", err)
	}
	return dedupeMonitorIDs(monitorIDs), nil
}

func isUndefinedTableError(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01"
}

func (s *Service) monitorPresenter(monitorType string) monitorPresenter {
	presenter, ok := s.presenters[monitorType]
	if ok {
		return presenter
	}
	return regularMonitorPresenter{}
}

// populateGroupMonitorShortRange fills a group monitor's status, uptime, latency, hourly
// strip and recent history by aggregating over its (pre-resolved) leaf member IDs. Long-range
// analytics are applied separately by the batched pass. Per-monitor widgets that the public
// page never renders (5-minute strip, raw latency series, short-range downtime) are
// intentionally not populated.
func (s *Service) populateGroupMonitorShortRange(ctx context.Context, monitor *MonitorStatus, memberIDs []uuid.UUID, tenantID uuid.UUID) {
	if len(memberIDs) == 0 {
		monitor.Status = "unknown"
		monitor.Uptime24hFormatted = "N/A"
		monitor.Uptime1hFormatted = "N/A"
		return
	}

	groupStatus, err := s.GetGroupAggregatedStatus(ctx, memberIDs, tenantID)
	if err != nil {
		monitor.Status = "unknown"
	} else {
		monitor.Status = groupStatus.Status
		monitor.LastCheckTime = groupStatus.LastCheckTime
		monitor.LastLatency = groupStatus.LastLatency
	}

	uptime24h, err := s.CalculateGroupUptime24h(ctx, memberIDs, tenantID)
	monitor.Uptime24h = uptime24h
	monitor.Uptime24hFormatted = formatUptime(uptime24h, err)

	uptime1h, err := s.CalculateGroupUptime1h(ctx, memberIDs, tenantID)
	monitor.Uptime1h = uptime1h
	monitor.Uptime1hFormatted = formatUptime(uptime1h, err)

	avgLatency1h, _ := s.CalculateGroupAvgLatency(ctx, memberIDs, tenantID, "1 hour")
	monitor.AvgLatency1h = avgLatency1h

	avgLatency24h, _ := s.CalculateGroupAvgLatency(ctx, memberIDs, tenantID, "24 hours")
	monitor.AvgLatency24h = avgLatency24h

	hourlyUptime, err := s.GetGroupHourlyUptime(ctx, memberIDs, tenantID)
	if err == nil {
		monitor.HourlyUptime = hourlyUptime
		monitor.UptimeHistory24h = hourlyUptime
	}

	history, err := s.GetGroupHistory(ctx, memberIDs, tenantID, 50)
	if err == nil {
		monitor.History = history
	}
}

func formatUptime(uptime *float64, err error) string {
	if err != nil || uptime == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f%%", *uptime)
}

// longRangeRanges are the rollup-backed ranges rendered per monitor on the status page.
var longRangeRanges = []sharedanalytics.Range{sharedanalytics.Range7d, sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d}

// applyMonitorLongRangeAnalytics is the per-monitor (unbatched) long-range path, retained as
// a fallback for analytics readers that do not implement batched access.
func (s *Service) applyMonitorLongRangeAnalytics(ctx context.Context, monitor *MonitorStatus, tenantID uuid.UUID, monitorIDs []uuid.UUID) {
	now := s.now()
	for _, rangeValue := range longRangeRanges {
		result, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, rangeValue, now)
		if err != nil {
			continue
		}
		assignLongRangeResult(monitor, rangeValue, result)
	}
}

// assignLongRangeResult maps one analytics Result onto the monitor's range-specific fields.
// Shared by the batched and unbatched long-range paths so they produce identical output.
func assignLongRangeResult(monitor *MonitorStatus, rangeValue sharedanalytics.Range, result *sharedanalytics.Result) {
	if result == nil {
		return
	}
	uptimeHistory := mapDailySeries(result.Series)
	latencyHistory := mapLatencySeries(result.Series, rangeValue)
	downtime := mapDowntimePeriods(result.Downtime)
	switch rangeValue {
	case sharedanalytics.Range7d:
		monitor.UptimeHistory7d = uptimeHistory
	case sharedanalytics.Range30d:
		monitor.UptimeHistory30d = uptimeHistory
		monitor.LatencyHistory30d = latencyHistory
		monitor.DowntimePeriods30d = downtime
	case sharedanalytics.Range90d:
		monitor.UptimeHistory90d = uptimeHistory
		monitor.LatencyHistory90d = latencyHistory
		monitor.DowntimePeriods90d = downtime
	case sharedanalytics.Range365d:
		monitor.UptimeHistory365d = uptimeHistory
		monitor.LatencyHistory365d = latencyHistory
		monitor.DowntimePeriods365d = downtime
	}
}

func (s *Service) applyGlobalLongRangeAnalytics(ctx context.Context, page *StatusPageData, tenantID uuid.UUID, monitorIDs []uuid.UUID) {
	ranges := []sharedanalytics.Range{sharedanalytics.Range7d, sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d}
	now := s.now()
	for _, rangeValue := range ranges {
		result, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, rangeValue, now)
		if err != nil {
			continue
		}
		uptimeHistory := mapDailySeries(result.Series)
		switch rangeValue {
		case sharedanalytics.Range7d:
			page.UptimeHistory7 = uptimeHistory
		case sharedanalytics.Range30d:
			page.UptimeHistory30 = uptimeHistory
		case sharedanalytics.Range90d:
			page.UptimeHistory90 = uptimeHistory
		case sharedanalytics.Range365d:
			page.UptimeHistory365 = uptimeHistory
		}
	}
}

func (s *Service) resolveStatusPageOperationalMonitorIDs(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]uuid.UUID, error) {
	monitorIDs, err := s.listStatusPageMonitorIDs(ctx, statusPageID)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []uuid.UUID{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.type
		FROM monitors m
		WHERE m.id = ANY($1) AND m.tenant_id = $2 AND m.deleted_at IS NULL
	`, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list status page monitor ids: %w", err)
	}
	defer rows.Close()

	resolved := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		var monitorType string
		if err := rows.Scan(&monitorID, &monitorType); err != nil {
			return nil, fmt.Errorf("failed to scan status page monitor id: %w", err)
		}
		operationalIDs, err := s.monitorPresenter(monitorType).ResolveOperationalIDs(ctx, s, monitorID, tenantID)
		if err != nil {
			continue
		}
		resolved = append(resolved, operationalIDs...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status page monitor ids: %w", err)
	}
	return dedupeMonitorIDs(resolved), nil
}

func mapDailySeries(series []sharedanalytics.SeriesPoint) []DailyUptime {
	result := make([]DailyUptime, 0, len(series))
	for _, point := range series {
		uptime := point.UptimePct
		if !point.HasData {
			uptime = -1
		}
		result = append(result, DailyUptime{
			Date:   point.BucketStart.UTC().Format("2006-01-02"),
			Uptime: uptime,
		})
	}
	return result
}

func mapLatencySeries(series []sharedanalytics.SeriesPoint, rangeValue sharedanalytics.Range) []LatencyPoint {
	result := make([]LatencyPoint, 0, len(series))
	timeFormat := "Jan 2"
	if rangeValue == sharedanalytics.Range30d {
		timeFormat = "Jan 2"
	}
	for _, point := range series {
		if point.AvgLatencyMS == nil || !point.HasData {
			continue
		}
		result = append(result, LatencyPoint{
			Timestamp: point.BucketStart.UTC(),
			LatencyMS: int(*point.AvgLatencyMS),
			Time:      point.BucketStart.UTC().Format(timeFormat),
			Unix:      point.BucketStart.UTC().Unix(),
		})
	}
	return result
}

func mapDowntimePeriods(periods []sharedanalytics.DowntimePeriod) []DowntimePeriod {
	result := make([]DowntimePeriod, 0, len(periods))
	for _, period := range periods {
		result = append(result, DowntimePeriod{
			StartTime: period.Start.UTC().Format("Jan 2 15:04"),
			EndTime:   period.End.UTC().Format("Jan 2 15:04"),
			StartUnix: period.Start.UTC().Unix(),
			EndUnix:   period.End.UTC().Unix(),
		})
	}
	return result
}

func dedupeMonitorIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// CurrentStatus represents the current status of a monitor
type CurrentStatus struct {
	Status             string
	LastCheckTime      *time.Time
	LastHTTPStatus     *int
	LastLatency        *int
	TLSDaysUntilExpiry *int
	TLSNotAfter        string
}

// GetMonitorCurrentStatus gets the current status derived from the monitor's persisted state and
// the latest check result (for display fields). Status is derived from current_state, not the
// raw result status, for consistency with batchCurrentStatus.
func (s *Service) GetMonitorCurrentStatus(ctx context.Context, monitorID, tenantID uuid.UUID) (*CurrentStatus, error) {
	query := `
		SELECT mon.current_state, cr.status, cr.http_status, cr.latency_ms, cr.created_at, cr.metrics_data
		FROM monitors mon
		LEFT JOIN LATERAL (
			SELECT cr.status, cr.http_status, cr.latency_ms, cr.created_at, cr.metrics_data
			FROM check_results cr
			WHERE cr.monitor_id = mon.id AND cr.tenant_id = $2
			ORDER BY cr.created_at DESC
			LIMIT 1
		) cr ON TRUE
		WHERE mon.id = $1
	`

	var currentState string
	var resultStatus sql.NullString
	var httpStatus sql.NullInt64
	var latencyMS sql.NullInt64
	var createdAt sql.NullTime
	var metricsJSON []byte

	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&currentState, &resultStatus, &httpStatus, &latencyMS, &createdAt, &metricsJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no check results found")
		}
		return nil, fmt.Errorf("failed to get current status: %w", err)
	}

	result := &CurrentStatus{
		Status: mapMonitorState(currentState),
	}
	if createdAt.Valid {
		t := createdAt.Time
		result.LastCheckTime = &t
	}

	if httpStatus.Valid {
		httpStatusInt := int(httpStatus.Int64)
		result.LastHTTPStatus = &httpStatusInt
	}

	if latencyMS.Valid {
		latencyInt := int(latencyMS.Int64)
		result.LastLatency = &latencyInt
	}

	if len(metricsJSON) > 0 {
		var env httpMetricsEnvelope
		if err := json.Unmarshal(metricsJSON, &env); err == nil && env.HTTP != nil && env.HTTP.TLS != nil {
			if env.HTTP.TLS.DaysUntilExpiry != nil {
				result.TLSDaysUntilExpiry = env.HTTP.TLS.DaysUntilExpiry
			}
			if env.HTTP.TLS.NotAfter != "" {
				result.TLSNotAfter = env.HTTP.TLS.NotAfter
			}
		}
	}

	return result, nil
}

// GetMonitorHistory gets recent check results for a monitor
func (s *Service) GetMonitorHistory(ctx context.Context, monitorID, tenantID uuid.UUID, limit int, since *time.Time) ([]CheckResultHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var query string
	var args []interface{}

	if since != nil {
		query = `
			SELECT status, latency_ms, created_at
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= $3
			ORDER BY created_at DESC
			LIMIT $4
		`
		args = []interface{}{monitorID, tenantID, *since, limit}
	} else {
		// Default to last 24 hours
		query = `
			SELECT status, latency_ms, created_at
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '24 hours'
			ORDER BY created_at DESC
			LIMIT $3
		`
		args = []interface{}{monitorID, tenantID, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query history: %w", err)
	}
	defer rows.Close()

	var history []CheckResultHistory
	for rows.Next() {
		var result CheckResultHistory
		var status string
		var latencyMS sql.NullInt64

		err := rows.Scan(&status, &latencyMS, &result.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan history: %w", err)
		}

		result.Status = mapResultStatus(status)
		if latencyMS.Valid {
			latencyInt := int(latencyMS.Int64)
			result.LatencyMS = &latencyInt
		}

		history = append(history, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating history: %w", err)
	}

	// Reverse to chronological order for charts
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	return history, nil
}

// CalculateUptime24h calculates uptime percentage over the last 24 hours
func (s *Service) CalculateUptime24h(ctx context.Context, monitorID, tenantID uuid.UUID) (*float64, error) {
	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success') as successful
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '24 hours'
	`

	var total, successful int
	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&total, &successful)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate uptime: %w", err)
	}

	if total == 0 {
		// No checks yet, return null
		return nil, nil
	}

	uptime := (float64(successful) / float64(total)) * 100.0
	return &uptime, nil
}

// CalculateUptime1h calculates uptime percentage over the last 1 hour
func (s *Service) CalculateUptime1h(ctx context.Context, monitorID, tenantID uuid.UUID) (*float64, error) {
	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success') as successful
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '1 hour'
	`

	var total, successful int
	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&total, &successful)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate 1h uptime: %w", err)
	}

	if total == 0 {
		return nil, nil
	}

	uptime := (float64(successful) / float64(total)) * 100.0
	return &uptime, nil
}

// CalculateAvgLatency calculates average latency for a given time interval
func (s *Service) CalculateAvgLatency(ctx context.Context, monitorID, tenantID uuid.UUID, interval string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT AVG(latency_ms)
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 
		  AND created_at >= NOW() - INTERVAL '%s'
		  AND latency_ms IS NOT NULL
		  AND status = 'success'
	`, interval)

	var avgLatency sql.NullFloat64
	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&avgLatency)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate avg latency: %w", err)
	}

	if !avgLatency.Valid {
		return nil, nil
	}

	val := avgLatency.Float64
	return &val, nil
}

// GetMonitorHourlyUptime calculates hourly uptime for a single monitor over the last 24 hours
func (s *Service) GetMonitorHourlyUptime(ctx context.Context, monitorID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	query := `
		WITH hours AS (
			SELECT generate_series(
				date_trunc('hour', NOW() - INTERVAL '23 hours'),
				date_trunc('hour', NOW()),
				INTERVAL '1 hour'
			) AS hour
		),
		hourly_stats AS (
			SELECT 
				date_trunc('hour', cr.created_at) AS hour,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = $1
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '24 hours'
			GROUP BY date_trunc('hour', cr.created_at)
		)
		SELECT 
			h.hour,
			COALESCE(hs.total, 0) AS total,
			COALESCE(hs.successful, 0) AS successful
		FROM hours h
		LEFT JOIN hourly_stats hs ON h.hour = hs.hour
		ORDER BY h.hour
	`

	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor hourly uptime: %w", err)
	}
	defer rows.Close()

	var result []HourlyUptime
	for rows.Next() {
		var hour time.Time
		var total, successful int

		if err := rows.Scan(&hour, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan monitor hourly uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, HourlyUptime{
			Hour:   hour.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor hourly uptime: %w", err)
	}

	return result, nil
}

// GetMonitor5MinuteUptime calculates 5-minute bucket uptime for a single monitor over the last 1 hour
func (s *Service) GetMonitor5MinuteUptime(ctx context.Context, monitorID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	query := `
		WITH buckets AS (
			SELECT generate_series(
				date_trunc('minute', NOW() - INTERVAL '55 minutes') - (EXTRACT(minute FROM NOW())::int % 5) * INTERVAL '1 minute',
				date_trunc('minute', NOW()),
				INTERVAL '5 minutes'
			) AS bucket
		),
		bucket_stats AS (
			SELECT 
				date_trunc('minute', cr.created_at) - (EXTRACT(minute FROM cr.created_at)::int % 5) * INTERVAL '1 minute' AS bucket,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = $1
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '1 hour'
			GROUP BY 1
		)
		SELECT 
			b.bucket,
			COALESCE(bs.total, 0) AS total,
			COALESCE(bs.successful, 0) AS successful
		FROM buckets b
		LEFT JOIN bucket_stats bs ON b.bucket = bs.bucket
		ORDER BY b.bucket
	`

	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor 5-minute uptime: %w", err)
	}
	defer rows.Close()

	var result []MinuteUptime
	for rows.Next() {
		var bucket time.Time
		var total, successful int

		if err := rows.Scan(&bucket, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan monitor 5-minute uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, MinuteUptime{
			Time:   bucket.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor 5-minute uptime: %w", err)
	}

	return result, nil
}

// GetMonitorDailyUptime calculates daily uptime for a single monitor for N days
func (s *Service) GetMonitorDailyUptime(ctx context.Context, monitorID, tenantID uuid.UUID, days int) ([]DailyUptime, error) {
	query := `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', NOW() - $3::INTERVAL),
				date_trunc('day', NOW()),
				INTERVAL '1 day'
			) AS day
		),
		daily_stats AS (
			SELECT 
				date_trunc('day', cr.created_at) AS day,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = $1
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - $3::INTERVAL
			GROUP BY date_trunc('day', cr.created_at)
		)
		SELECT 
			d.day,
			COALESCE(ds.total, 0) AS total,
			COALESCE(ds.successful, 0) AS successful
		FROM days d
		LEFT JOIN daily_stats ds ON d.day = ds.day
		ORDER BY d.day
	`

	interval := fmt.Sprintf("%d days", days)
	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor daily uptime: %w", err)
	}
	defer rows.Close()

	var result []DailyUptime
	for rows.Next() {
		var day time.Time
		var total, successful int

		if err := rows.Scan(&day, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan monitor daily uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, DailyUptime{
			Date:   day.Format("2006-01-02"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor daily uptime: %w", err)
	}

	return result, nil
}

// GetMonitorLatencyHistory gets latency data points for a monitor for sparkline chart
func (s *Service) GetMonitorLatencyHistory(ctx context.Context, monitorID, tenantID uuid.UUID, limit int) ([]LatencyPoint, error) {
	return s.GetMonitorLatencyHistoryForRange(ctx, monitorID, tenantID, "24 hours", limit)
}

// GetMonitorLatencyHistoryForRange gets latency data points for a monitor for a specific time range
// For longer time ranges, it aggregates data into time buckets to reduce visual clutter
func (s *Service) GetMonitorLatencyHistoryForRange(ctx context.Context, monitorID, tenantID uuid.UUID, interval string, limit int) ([]LatencyPoint, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var query string
	var bucketInterval string
	timeFormat := "15:04"

	// Determine aggregation strategy based on time range
	// For shorter ranges, use raw data; for longer ranges, aggregate into buckets
	switch interval {
	case "1 hour":
		// 1h: Use raw data points (most recent 'limit' points)
		query = `
			SELECT latency_ms, created_at FROM (
				SELECT latency_ms, created_at
				FROM check_results
				WHERE monitor_id = $1 AND tenant_id = $2 
				  AND created_at >= NOW() - $3::INTERVAL
				  AND latency_ms IS NOT NULL
				  AND status = 'success'
				ORDER BY created_at DESC
				LIMIT $4
			) sub
			ORDER BY created_at ASC
		`
	case "24 hours":
		// 24h: Aggregate into 10-minute buckets (~144 buckets, limit to ~60)
		bucketInterval = "10 minutes"
		timeFormat = "15:04"
		limit = 60
	case "30 days":
		// 30d: Aggregate into 4-hour buckets (~180 buckets, limit to ~80)
		bucketInterval = "4 hours"
		timeFormat = "Jan 2 15:04"
		limit = 80
	case "90 days":
		// 90d: Aggregate into 12-hour buckets (~180 buckets, limit to ~80)
		bucketInterval = "12 hours"
		timeFormat = "Jan 2"
		limit = 80
	case "365 days":
		// 1y: Aggregate into 2-day buckets (~183 buckets, limit to ~100)
		bucketInterval = "2 days"
		timeFormat = "Jan 2"
		limit = 100
	default:
		// Default: raw data
		query = `
			SELECT latency_ms, created_at FROM (
				SELECT latency_ms, created_at
				FROM check_results
				WHERE monitor_id = $1 AND tenant_id = $2 
				  AND created_at >= NOW() - $3::INTERVAL
				  AND latency_ms IS NOT NULL
				  AND status = 'success'
				ORDER BY created_at DESC
				LIMIT $4
			) sub
			ORDER BY created_at ASC
		`
	}

	// For aggregated queries, build a different query
	if bucketInterval != "" {
		query = fmt.Sprintf(`
			SELECT 
				ROUND(AVG(latency_ms))::int as avg_latency,
				date_trunc('hour', created_at) + 
					(EXTRACT(EPOCH FROM created_at - date_trunc('hour', created_at)) / 
					 EXTRACT(EPOCH FROM '%s'::interval))::int * '%s'::interval as bucket_time
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2 
			  AND created_at >= NOW() - $3::INTERVAL
			  AND latency_ms IS NOT NULL
			  AND status = 'success'
			GROUP BY bucket_time
			ORDER BY bucket_time ASC
			LIMIT $4
		`, bucketInterval, bucketInterval)
	}

	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID, interval, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query latency history: %w", err)
	}
	defer rows.Close()

	var result []LatencyPoint

	for rows.Next() {
		var latencyMS int
		var timestamp time.Time

		if err := rows.Scan(&latencyMS, &timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan latency history: %w", err)
		}

		result = append(result, LatencyPoint{
			Timestamp: timestamp,
			LatencyMS: latencyMS,
			Time:      timestamp.Format(timeFormat),
			Unix:      timestamp.Unix(),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating latency history: %w", err)
	}

	return result, nil
}

// GetGlobalHourlyUptime calculates hourly uptime across all monitors in a status page for the last 24 hours
func (s *Service) GetGlobalHourlyUptime(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	monitorIDs, err := s.resolveStatusPageOperationalMonitorIDs(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []HourlyUptime{}, nil
	}

	// Read the rollup cursor first and pass all hour-strip bounds as parameters:
	// bounds derived from a CTE join on rollup_job_state cannot be pushed into
	// index conditions on check_results.created_at and forced a full sequential scan.
	cursor, err := sharedanalytics.LoadRollupCursor(ctx, s.db)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	startHour := now.Truncate(time.Hour).Add(-23 * time.Hour)
	endExclusive := now.Truncate(time.Hour).Add(time.Hour)
	rawStart := cursor.RawStart(startHour)

	query := `
		WITH rollup_per_hour AS (
			SELECT
				mhr.bucket_hour,
				SUM(mhr.total_checks)::bigint AS total_checks,
				SUM(mhr.success_checks)::bigint AS success_checks
			FROM monitor_hourly_rollups mhr
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($2)
			  AND mhr.bucket_hour >= $3
			  AND mhr.bucket_hour < $4
			GROUP BY mhr.bucket_hour
		),
		raw_per_hour AS (
			SELECT
				date_trunc('hour', cr.created_at) AS bucket_hour,
				COUNT(*)::bigint AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success')::bigint AS success_checks
			FROM check_results cr
			WHERE cr.tenant_id = $1
			  AND cr.monitor_id = ANY($2)
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $5
			  AND cr.created_at < $4
			  AND (
				$6::timestamptz IS NULL
				OR (cr.created_at, cr.id) > ($6::timestamptz, $7::uuid)
			  )
			GROUP BY 1
		)
		SELECT
			b.bucket_hour,
			COALESCE(rh.total_checks, 0) + COALESCE(rwh.total_checks, 0) AS total_checks,
			COALESCE(rh.success_checks, 0) + COALESCE(rwh.success_checks, 0) AS success_checks
		FROM generate_series($3::timestamptz, $4::timestamptz - INTERVAL '1 hour', INTERVAL '1 hour') AS b(bucket_hour)
		LEFT JOIN rollup_per_hour rh ON rh.bucket_hour = b.bucket_hour
		LEFT JOIN raw_per_hour rwh ON rwh.bucket_hour = b.bucket_hour
		ORDER BY b.bucket_hour
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), startHour, endExclusive, rawStart, cursor.LastCreatedAt, cursor.LastCheckResultID)
	if err != nil {
		return nil, fmt.Errorf("failed to query hourly uptime: %w", err)
	}
	defer rows.Close()

	var result []HourlyUptime
	for rows.Next() {
		var hour time.Time
		var total, successful int

		if err := rows.Scan(&hour, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan hourly uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, HourlyUptime{
			Hour:   hour.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hourly uptime: %w", err)
	}

	return result, nil
}

// GetGlobalDailyUptime calculates daily uptime across all monitors in a status page for N days
func (s *Service) GetGlobalDailyUptime(ctx context.Context, statusPageID, tenantID uuid.UUID, days int) ([]DailyUptime, error) {
	monitorIDs, err := s.resolveStatusPageOperationalMonitorIDs(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []DailyUptime{}, nil
	}

	query := `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', NOW() - $3::INTERVAL),
				date_trunc('day', NOW()),
				INTERVAL '1 day'
			) AS day
		),
		daily_stats AS (
			SELECT 
				date_trunc('day', cr.created_at) AS day,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - $3::INTERVAL
			GROUP BY date_trunc('day', cr.created_at)
		)
		SELECT 
			d.day,
			COALESCE(ds.total, 0) AS total,
			COALESCE(ds.successful, 0) AS successful
		FROM days d
		LEFT JOIN daily_stats ds ON d.day = ds.day
		ORDER BY d.day
	`

	interval := fmt.Sprintf("%d days", days)
	rows, err := s.db.QueryContext(ctx, query, pq.Array(monitorIDs), tenantID, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query daily uptime: %w", err)
	}
	defer rows.Close()

	var result []DailyUptime
	for rows.Next() {
		var day time.Time
		var total, successful int

		if err := rows.Scan(&day, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan daily uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, DailyUptime{
			Date:   day.Format("2006-01-02"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating daily uptime: %w", err)
	}

	return result, nil
}

// GetLatestAgentMetrics retrieves the most recent agent metrics for a monitor
func (s *Service) GetLatestAgentMetrics(ctx context.Context, monitorID, tenantID uuid.UUID) (*AgentMetricsData, error) {
	query := `
		SELECT metrics_data, created_at
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 AND metrics_data IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1
	`

	var metricsJSON []byte
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&metricsJSON, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no agent metrics found")
		}
		return nil, fmt.Errorf("failed to get agent metrics: %w", err)
	}

	// Parse JSON metrics
	var rawMetrics struct {
		CPUPercent      float64 `json:"cpu_percent"`
		MemoryUsed      uint64  `json:"memory_used"`
		MemoryTotal     uint64  `json:"memory_total"`
		DiskUsed        uint64  `json:"disk_used"`
		DiskTotal       uint64  `json:"disk_total"`
		NetworkBytesIn  uint64  `json:"network_bytes_in"`
		NetworkBytesOut uint64  `json:"network_bytes_out"`
		LoadAvg1        float64 `json:"load_avg_1"`
		LoadAvg5        float64 `json:"load_avg_5"`
		LoadAvg15       float64 `json:"load_avg_15"`
		ProcessCount    int     `json:"process_count"`
	}

	if err := json.Unmarshal(metricsJSON, &rawMetrics); err != nil {
		return nil, fmt.Errorf("failed to parse agent metrics: %w", err)
	}

	metrics := &AgentMetricsData{
		CPUPercent:      rawMetrics.CPUPercent,
		MemoryUsed:      rawMetrics.MemoryUsed,
		MemoryTotal:     rawMetrics.MemoryTotal,
		DiskUsed:        rawMetrics.DiskUsed,
		DiskTotal:       rawMetrics.DiskTotal,
		NetworkBytesIn:  rawMetrics.NetworkBytesIn,
		NetworkBytesOut: rawMetrics.NetworkBytesOut,
		LoadAvg1:        rawMetrics.LoadAvg1,
		LoadAvg5:        rawMetrics.LoadAvg5,
		LoadAvg15:       rawMetrics.LoadAvg15,
		ProcessCount:    rawMetrics.ProcessCount,
		Timestamp:       createdAt,
	}

	// Calculate percentages
	if metrics.MemoryTotal > 0 {
		metrics.MemoryPercent = float64(metrics.MemoryUsed) / float64(metrics.MemoryTotal) * 100
	}
	if metrics.DiskTotal > 0 {
		metrics.DiskPercent = float64(metrics.DiskUsed) / float64(metrics.DiskTotal) * 100
	}

	return metrics, nil
}

// GetMonitorDowntimePeriods gets downtime periods for a monitor for a specific time range
// It detects continuous periods of failure by analyzing check results
func (s *Service) GetMonitorDowntimePeriods(ctx context.Context, monitorID, tenantID uuid.UUID, interval string) ([]DowntimePeriod, error) {
	// Query all check results in order to detect transitions
	query := `
		SELECT status, created_at
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 
		  AND created_at >= NOW() - $3::INTERVAL
		ORDER BY created_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query, monitorID, tenantID, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query check results: %w", err)
	}
	defer rows.Close()

	timeFormat := "15:04"
	if interval != "1 hour" && interval != "24 hours" {
		timeFormat = "Jan 2 15:04"
	}

	var periods []DowntimePeriod
	var currentPeriodStart *time.Time
	var lastFailureTime *time.Time

	for rows.Next() {
		var status string
		var timestamp time.Time

		if err := rows.Scan(&status, &timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan check result: %w", err)
		}

		isFailure := status != "success"

		if isFailure {
			if currentPeriodStart == nil {
				// Start of a new downtime period
				currentPeriodStart = &timestamp
			}
			lastFailureTime = &timestamp
		} else {
			if currentPeriodStart != nil && lastFailureTime != nil {
				// End of downtime period - transition from failure to success
				periods = append(periods, DowntimePeriod{
					StartTime: currentPeriodStart.Format(timeFormat),
					EndTime:   lastFailureTime.Format(timeFormat),
					StartUnix: currentPeriodStart.Unix(),
					EndUnix:   lastFailureTime.Unix(),
				})
				currentPeriodStart = nil
				lastFailureTime = nil
			}
		}
	}

	// Handle case where we're still in a downtime period at the end
	if currentPeriodStart != nil && lastFailureTime != nil {
		periods = append(periods, DowntimePeriod{
			StartTime: currentPeriodStart.Format(timeFormat),
			EndTime:   lastFailureTime.Format(timeFormat),
			StartUnix: currentPeriodStart.Unix(),
			EndUnix:   lastFailureTime.Unix(),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating check results: %w", err)
	}

	return periods, nil
}

// GetLatestPushMetrics retrieves the most recent push metrics for a monitor
// Returns a dynamic map of metric names to values (auto-detected from push data)
func (s *Service) GetLatestPushMetrics(ctx context.Context, monitorID, tenantID uuid.UUID) (map[string]interface{}, error) {
	query := `
		SELECT metrics_data
		FROM check_results
		WHERE monitor_id = $1 AND tenant_id = $2 AND metrics_data IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1
	`

	var metricsJSON []byte

	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(&metricsJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no push metrics found")
		}
		return nil, fmt.Errorf("failed to get push metrics: %w", err)
	}

	// Parse JSON metrics - push metrics are a dynamic map
	var metrics map[string]interface{}
	if err := json.Unmarshal(metricsJSON, &metrics); err != nil {
		return nil, fmt.Errorf("failed to parse push metrics: %w", err)
	}

	return metrics, nil
}

// mapResultStatus maps database status to public status
func mapResultStatus(status string) string {
	switch status {
	case "success":
		return "up"
	case "failure":
		return "down"
	case "error":
		return "error"
	default:
		return "unknown"
	}
}

// getGroupMemberIDs retrieves non-group member monitor IDs for a group recursively, deduplicated.
func (s *Service) getGroupMemberIDs(ctx context.Context, groupID, tenantID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		WITH RECURSIVE member_tree AS (
			SELECT mg.monitor_id
			FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id
			WHERE mg.group_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
			UNION
			SELECT mg.monitor_id
			FROM member_tree mt
			JOIN monitors parent ON parent.id = mt.monitor_id AND parent.tenant_id = $2
			JOIN monitor_groups mg ON mg.group_id = parent.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = $2
			WHERE parent.type = 'group'
			  AND parent.deleted_at IS NULL
			  AND child.deleted_at IS NULL
		)
		SELECT m.id
		FROM monitors m
		JOIN member_tree mt ON mt.monitor_id = m.id
		WHERE m.tenant_id = $2
		  AND m.type <> 'group'
		  AND m.deleted_at IS NULL
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query group members: %w", err)
	}
	defer rows.Close()

	var memberIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan member ID: %w", err)
		}
		memberIDs = append(memberIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group members: %w", err)
	}

	return memberIDs, nil
}

// GetGroupAggregatedStatus calculates the aggregated status for a group from its members
// Returns "up" if all members are up, "down" if all are down, "degraded" if mixed
func (s *Service) GetGroupAggregatedStatus(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) (*CurrentStatus, error) {
	if len(memberIDs) == 0 {
		return &CurrentStatus{Status: "unknown"}, nil
	}

	// Query the latest check result for each member monitor
	query := `
		SELECT DISTINCT ON (monitor_id) status, latency_ms, created_at
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2
		ORDER BY monitor_id, created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query member statuses: %w", err)
	}
	defer rows.Close()

	successCount := 0
	failureCount := 0
	errorCount := 0
	var latestCheckTime *time.Time
	var totalLatency int64
	var latencyCount int

	for rows.Next() {
		var status string
		var latencyMS sql.NullInt64
		var createdAt time.Time

		if err := rows.Scan(&status, &latencyMS, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan member status: %w", err)
		}

		// Track latest check time
		if latestCheckTime == nil || createdAt.After(*latestCheckTime) {
			latestCheckTime = &createdAt
		}

		// Track latency for averaging
		if latencyMS.Valid {
			totalLatency += latencyMS.Int64
			latencyCount++
		}

		switch status {
		case "success":
			successCount++
		case "failure":
			failureCount++
		case "error":
			errorCount++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating member statuses: %w", err)
	}

	totalChecked := successCount + failureCount + errorCount

	if totalChecked == 0 {
		return &CurrentStatus{Status: "unknown"}, nil
	}

	result := &CurrentStatus{
		LastCheckTime: latestCheckTime,
	}

	// Calculate average latency
	if latencyCount > 0 {
		avgLatency := int(totalLatency / int64(latencyCount))
		result.LastLatency = &avgLatency
	}

	// Apply status logic: all up = UP, all down = DOWN, otherwise = DEGRADED
	if successCount == totalChecked {
		result.Status = "up"
	} else if failureCount+errorCount == totalChecked {
		result.Status = "down"
	} else {
		result.Status = "degraded"
	}

	return result, nil
}

// CalculateGroupUptime24h calculates aggregated uptime for a group over the last 24 hours
func (s *Service) CalculateGroupUptime24h(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) (*float64, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success') as successful
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '24 hours'
	`

	var total, successful int
	err := s.db.QueryRowContext(ctx, query, pq.Array(memberIDs), tenantID).Scan(&total, &successful)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate group uptime: %w", err)
	}

	if total == 0 {
		return nil, nil
	}

	uptime := (float64(successful) / float64(total)) * 100.0
	return &uptime, nil
}

// CalculateGroupUptime1h calculates aggregated uptime for a group over the last 1 hour
func (s *Service) CalculateGroupUptime1h(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) (*float64, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'success') as successful
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '1 hour'
	`

	var total, successful int
	err := s.db.QueryRowContext(ctx, query, pq.Array(memberIDs), tenantID).Scan(&total, &successful)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate group 1h uptime: %w", err)
	}

	if total == 0 {
		return nil, nil
	}

	uptime := (float64(successful) / float64(total)) * 100.0
	return &uptime, nil
}

// CalculateGroupAvgLatency calculates average latency for a group for a given time interval
func (s *Service) CalculateGroupAvgLatency(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, interval string) (*float64, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`
		SELECT AVG(latency_ms)
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2 
		  AND created_at >= NOW() - INTERVAL '%s'
		  AND latency_ms IS NOT NULL
		  AND status = 'success'
	`, interval)

	var avgLatency sql.NullFloat64
	err := s.db.QueryRowContext(ctx, query, pq.Array(memberIDs), tenantID).Scan(&avgLatency)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate group avg latency: %w", err)
	}

	if !avgLatency.Valid {
		return nil, nil
	}

	val := avgLatency.Float64
	return &val, nil
}

// GetGroupHourlyUptime calculates hourly uptime for a group over the last 24 hours
func (s *Service) GetGroupHourlyUptime(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := `
		WITH hours AS (
			SELECT generate_series(
				date_trunc('hour', NOW() - INTERVAL '23 hours'),
				date_trunc('hour', NOW()),
				INTERVAL '1 hour'
			) AS hour
		),
		hourly_stats AS (
			SELECT 
				date_trunc('hour', cr.created_at) AS hour,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '24 hours'
			GROUP BY date_trunc('hour', cr.created_at)
		)
		SELECT 
			h.hour,
			COALESCE(hs.total, 0) AS total,
			COALESCE(hs.successful, 0) AS successful
		FROM hours h
		LEFT JOIN hourly_stats hs ON h.hour = hs.hour
		ORDER BY h.hour
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query group hourly uptime: %w", err)
	}
	defer rows.Close()

	var result []HourlyUptime
	for rows.Next() {
		var hour time.Time
		var total, successful int

		if err := rows.Scan(&hour, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan group hourly uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, HourlyUptime{
			Hour:   hour.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group hourly uptime: %w", err)
	}

	return result, nil
}

// GetGroup5MinuteUptime calculates 5-minute bucket uptime for a group over the last 1 hour
func (s *Service) GetGroup5MinuteUptime(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := `
		WITH buckets AS (
			SELECT generate_series(
				date_trunc('minute', NOW() - INTERVAL '55 minutes') - (EXTRACT(minute FROM NOW())::int % 5) * INTERVAL '1 minute',
				date_trunc('minute', NOW()),
				INTERVAL '5 minutes'
			) AS bucket
		),
		bucket_stats AS (
			SELECT 
				date_trunc('minute', cr.created_at) - (EXTRACT(minute FROM cr.created_at)::int % 5) * INTERVAL '1 minute' AS bucket,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '1 hour'
			GROUP BY 1
		)
		SELECT 
			b.bucket,
			COALESCE(bs.total, 0) AS total,
			COALESCE(bs.successful, 0) AS successful
		FROM buckets b
		LEFT JOIN bucket_stats bs ON b.bucket = bs.bucket
		ORDER BY b.bucket
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query group 5-minute uptime: %w", err)
	}
	defer rows.Close()

	var result []MinuteUptime
	for rows.Next() {
		var bucket time.Time
		var total, successful int

		if err := rows.Scan(&bucket, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan group 5-minute uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, MinuteUptime{
			Time:   bucket.Format("15:04"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group 5-minute uptime: %w", err)
	}

	return result, nil
}

// GetGroupDailyUptime calculates daily uptime for a group for N days
func (s *Service) GetGroupDailyUptime(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, days int) ([]DailyUptime, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	query := `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', NOW() - $3::INTERVAL),
				date_trunc('day', NOW()),
				INTERVAL '1 day'
			) AS day
		),
		daily_stats AS (
			SELECT 
				date_trunc('day', cr.created_at) AS day,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - $3::INTERVAL
			GROUP BY date_trunc('day', cr.created_at)
		)
		SELECT 
			d.day,
			COALESCE(ds.total, 0) AS total,
			COALESCE(ds.successful, 0) AS successful
		FROM days d
		LEFT JOIN daily_stats ds ON d.day = ds.day
		ORDER BY d.day
	`

	interval := fmt.Sprintf("%d days", days)
	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query group daily uptime: %w", err)
	}
	defer rows.Close()

	var result []DailyUptime
	for rows.Next() {
		var day time.Time
		var total, successful int

		if err := rows.Scan(&day, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan group daily uptime: %w", err)
		}

		uptime := -1.0 // -1 indicates no data
		if total > 0 {
			uptime = (float64(successful) / float64(total)) * 100.0
		}

		result = append(result, DailyUptime{
			Date:   day.Format("2006-01-02"),
			Uptime: uptime,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group daily uptime: %w", err)
	}

	return result, nil
}

// GetGroupLatencyHistoryForRange gets aggregated latency data points for a group for a specific time range
func (s *Service) GetGroupLatencyHistoryForRange(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, interval string, limit int) ([]LatencyPoint, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var query string
	var bucketInterval string
	timeFormat := "15:04"

	// Determine aggregation strategy based on time range
	switch interval {
	case "1 hour":
		// 1h: Use raw data points averaged across members
		query = `
			SELECT ROUND(AVG(latency_ms))::int as avg_latency, created_at FROM (
				SELECT ROUND(AVG(latency_ms))::int as latency_ms, date_trunc('minute', created_at) as created_at
				FROM check_results
				WHERE monitor_id = ANY($1) AND tenant_id = $2 
				  AND created_at >= NOW() - $3::INTERVAL
				  AND latency_ms IS NOT NULL
				  AND status = 'success'
				GROUP BY date_trunc('minute', created_at)
				ORDER BY created_at DESC
				LIMIT $4
			) sub
			ORDER BY created_at ASC
		`
	case "24 hours":
		bucketInterval = "10 minutes"
		timeFormat = "15:04"
		limit = 60
	case "30 days":
		bucketInterval = "4 hours"
		timeFormat = "Jan 2 15:04"
		limit = 80
	case "90 days":
		bucketInterval = "12 hours"
		timeFormat = "Jan 2"
		limit = 80
	case "365 days":
		bucketInterval = "2 days"
		timeFormat = "Jan 2"
		limit = 100
	default:
		query = `
			SELECT ROUND(AVG(latency_ms))::int as avg_latency, created_at FROM (
				SELECT ROUND(AVG(latency_ms))::int as latency_ms, date_trunc('minute', created_at) as created_at
				FROM check_results
				WHERE monitor_id = ANY($1) AND tenant_id = $2 
				  AND created_at >= NOW() - $3::INTERVAL
				  AND latency_ms IS NOT NULL
				  AND status = 'success'
				GROUP BY date_trunc('minute', created_at)
				ORDER BY created_at DESC
				LIMIT $4
			) sub
			ORDER BY created_at ASC
		`
	}

	// For aggregated queries, build a different query
	if bucketInterval != "" {
		query = fmt.Sprintf(`
			SELECT 
				ROUND(AVG(latency_ms))::int as avg_latency,
				date_trunc('hour', created_at) + 
					(EXTRACT(EPOCH FROM created_at - date_trunc('hour', created_at)) / 
					 EXTRACT(EPOCH FROM '%s'::interval))::int * '%s'::interval as bucket_time
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2 
			  AND created_at >= NOW() - $3::INTERVAL
			  AND latency_ms IS NOT NULL
			  AND status = 'success'
			GROUP BY bucket_time
			ORDER BY bucket_time ASC
			LIMIT $4
		`, bucketInterval, bucketInterval)
	}

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID, interval, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query group latency history: %w", err)
	}
	defer rows.Close()

	var result []LatencyPoint

	for rows.Next() {
		var latencyMS int
		var timestamp time.Time

		if err := rows.Scan(&latencyMS, &timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan group latency history: %w", err)
		}

		result = append(result, LatencyPoint{
			Timestamp: timestamp,
			LatencyMS: latencyMS,
			Time:      timestamp.Format(timeFormat),
			Unix:      timestamp.Unix(),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group latency history: %w", err)
	}

	return result, nil
}

// GetGroupDowntimePeriods gets downtime periods for a group for a specific time range
// A group is considered down when ANY member is down
func (s *Service) GetGroupDowntimePeriods(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, interval string) ([]DowntimePeriod, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	// Query all check results from all members in order to detect transitions
	// We aggregate by timestamp to see if any member was down at each point
	query := `
		WITH time_points AS (
			SELECT DISTINCT date_trunc('minute', created_at) as minute
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2 
			  AND created_at >= NOW() - $3::INTERVAL
		),
		minute_status AS (
			SELECT 
				tp.minute,
				CASE WHEN COUNT(*) FILTER (WHERE cr.status != 'success') > 0 THEN 'failure' ELSE 'success' END as status
			FROM time_points tp
			LEFT JOIN check_results cr ON date_trunc('minute', cr.created_at) = tp.minute
			  AND cr.monitor_id = ANY($1) AND cr.tenant_id = $2
			GROUP BY tp.minute
		)
		SELECT status, minute
		FROM minute_status
		ORDER BY minute ASC
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query group check results: %w", err)
	}
	defer rows.Close()

	timeFormat := "15:04"
	if interval != "1 hour" && interval != "24 hours" {
		timeFormat = "Jan 2 15:04"
	}

	var periods []DowntimePeriod
	var currentPeriodStart *time.Time
	var lastFailureTime *time.Time

	for rows.Next() {
		var status string
		var timestamp time.Time

		if err := rows.Scan(&status, &timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan group check result: %w", err)
		}

		isFailure := status != "success"

		if isFailure {
			if currentPeriodStart == nil {
				currentPeriodStart = &timestamp
			}
			lastFailureTime = &timestamp
		} else {
			if currentPeriodStart != nil && lastFailureTime != nil {
				periods = append(periods, DowntimePeriod{
					StartTime: currentPeriodStart.Format(timeFormat),
					EndTime:   lastFailureTime.Format(timeFormat),
					StartUnix: currentPeriodStart.Unix(),
					EndUnix:   lastFailureTime.Unix(),
				})
				currentPeriodStart = nil
				lastFailureTime = nil
			}
		}
	}

	// Handle case where we're still in a downtime period at the end
	if currentPeriodStart != nil && lastFailureTime != nil {
		periods = append(periods, DowntimePeriod{
			StartTime: currentPeriodStart.Format(timeFormat),
			EndTime:   lastFailureTime.Format(timeFormat),
			StartUnix: currentPeriodStart.Unix(),
			EndUnix:   lastFailureTime.Unix(),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group check results: %w", err)
	}

	return periods, nil
}

// GetGroupHistory gets aggregated check results history for a group
func (s *Service) GetGroupHistory(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, limit int) ([]CheckResultHistory, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// Aggregate results by minute - if any member fails, mark that minute as failed
	query := `
		WITH minute_results AS (
			SELECT 
				date_trunc('minute', created_at) as minute,
				CASE WHEN COUNT(*) FILTER (WHERE status != 'success') > 0 THEN 'failure' ELSE 'success' END as status,
				ROUND(AVG(latency_ms))::int as avg_latency
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '24 hours'
			GROUP BY date_trunc('minute', created_at)
			ORDER BY minute DESC
			LIMIT $3
		)
		SELECT status, avg_latency, minute
		FROM minute_results
		ORDER BY minute ASC
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query group history: %w", err)
	}
	defer rows.Close()

	var history []CheckResultHistory
	for rows.Next() {
		var result CheckResultHistory
		var status string
		var latencyMS sql.NullInt64

		err := rows.Scan(&status, &latencyMS, &result.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group history: %w", err)
		}

		result.Status = mapResultStatus(status)
		if latencyMS.Valid {
			latencyInt := int(latencyMS.Int64)
			result.LatencyMS = &latencyInt
		}

		history = append(history, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group history: %w", err)
	}

	return history, nil
}
