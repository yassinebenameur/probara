package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	defaultFailuresLimit = 10
	defaultAlertsLimit   = 10
	maxListLimit         = 50
	problemMonitorLimit  = 5
	problemCandidateMult = 4
)

// rollupCursorLagWarnThreshold is how far the hourly-rollup cursor may trail
// "now" before the dashboard logs a warning: past this point every rolling-24h
// computation is scanning raw check_results for the whole lag tail, which is
// exactly the load the rollups exist to avoid. The window itself is never
// truncated — results stay correct, only slower.
const rollupCursorLagWarnThreshold = 2 * time.Hour

// The `tag` filter on every dashboard endpoint scopes to monitors carrying ANY
// of the selected tags (`tags && $n::text[]`, array overlap), never all of
// them (`@>`): the dashboard links its counts into /monitors with the same
// tags (web/lib/dashboard-view-model.ts), and that list matches any tag
// (monitorMatchesFilters in web/app/monitors/page.tsx), as SLA tag scopes do.
// With containment the two views counted different monitors. Every tag
// clause in this package and alerts.GetRecentAlertsForTags must use overlap.

// tenantSettingsReader is the minimal slice of the tenant service used by the dashboard
// service for resolving the curated dashboard_group_tags list.
type tenantSettingsReader interface {
	GetTenantSettings(ctx context.Context, tenantID uuid.UUID) (*models.TenantSettings, error)
}

// Service handles dashboard aggregation logic.
type Service struct {
	db           db.DB
	alertService alertservice.AlertService
	analytics    sharedanalytics.Reader
	tenants      tenantSettingsReader
	log          *logger.Logger // optional; nil disables service-level logging
	rolling24h   *rolling24hCache
}

// NewService creates a new dashboard service. log may be nil (e.g. in tests);
// it is only used for operational warnings such as rollup-cursor lag.
func NewService(database db.DB, alerts alertservice.AlertService, analytics sharedanalytics.Reader, tenants tenantSettingsReader, log *logger.Logger) *Service {
	return &Service{
		db:           database,
		alertService: alerts,
		analytics:    analytics,
		tenants:      tenants,
		log:          log,
		rolling24h:   newRolling24hCache(rolling24hCacheTTL),
	}
}

// GetOverview returns dashboard overview data for a tenant.
func (s *Service) GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error) {
	normalized := normalizeOverviewParams(params)

	summary, err := s.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{
		Range: normalized.Range,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	problemMonitors, err := s.GetProblemMonitors(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: problemMonitorLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	recentFailures, err := s.GetRecentFailures(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: normalized.FailuresLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	recentAlerts, err := s.GetRecentAlerts(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: normalized.AlertsLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	return &models.DashboardOverviewResponse{
		Range:           summary.Range,
		GeneratedAt:     summary.GeneratedAt,
		AvailableTags:   summary.AvailableTags,
		Stats:           summary.Stats,
		Trend:           summary.Trend,
		Activity24h:     summary.Activity24h,
		OpsSummary:      summary.OpsSummary,
		MonitorHealth:   summary.MonitorHealth,
		ProblemMonitors: problemMonitors.ProblemMonitors,
		RecentFailures:  recentFailures.RecentFailures,
		RecentAlerts:    recentAlerts.RecentAlerts,
	}, nil
}

// rolling24hData holds the DB-heavy exact-rolling-24h aggregates that several
// dashboard sections (stats, trend, activity, groups) all derive from. For the
// 24h range GetSummary computes these once and shares them across sections so
// the same expensive queries don't run multiple times per request. It is nil
// for every other range.
type rolling24hData struct {
	totals       map[uuid.UUID]MonitorRolling24hTotals
	hourlySeries []HourlyBucketPoint
}

// GetSummary returns lightweight dashboard data for first paint.
func (s *Service) GetSummary(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardSummaryResponse, error) {
	normalized := normalizeOverviewParams(params)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	availableTags, err := s.getAvailableTags(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// For the 24h range, compute the shared exact-rolling totals and the hourly
	// bucket series once at a single "now" and reuse them across stats, trend,
	// activity, and groups instead of re-running each heavy query per section.
	var precomp *rolling24hData
	if normalized.Range == models.DashboardRange24h {
		precomp, _, err = s.loadRolling24hData(ctx, tenantID, normalized.Tags)
		if err != nil {
			return nil, err
		}
	}

	stats, err := s.getStats(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	trend, err := s.getTrend(ctx, tenantID, normalized.Range, rangeStart, rangeEnd, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	activity := []models.DashboardActivityHour{}
	if normalized.Range == models.DashboardRange24h {
		activity, err = s.getActivity24h(ctx, tenantID, normalized.Tags, precomp)
		if err != nil {
			return nil, err
		}
	}

	monitorHealth, err := s.getMonitorHealth(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	opsSummary, err := s.getOpsSummary(ctx, tenantID, monitorHealth, normalized.Tags)
	if err != nil {
		return nil, err
	}

	settings, err := s.tenants.GetTenantSettings(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load tenant settings for groups: %w", err)
	}
	groupTags := settings.DashboardGroupTags
	if groupTags == nil {
		groupTags = []string{}
	}

	groups, err := s.loadGroups(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, groupTags, precomp)
	if err != nil {
		return nil, err
	}

	return &models.DashboardSummaryResponse{
		Range:         normalized.Range,
		GeneratedAt:   time.Now().UTC(),
		AvailableTags: availableTags,
		GroupTags:     groupTags,
		Stats:         stats,
		Trend:         trend,
		Activity24h:   activity,
		OpsSummary:    opsSummary,
		MonitorHealth: monitorHealth,
		Groups:        groups,
	}, nil
}

// GetProblemMonitors returns the problem monitors dashboard section.
func (s *Service) GetProblemMonitors(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardProblemMonitorsResponse, error) {
	normalized := normalizeListParams(params, problemMonitorLimit)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	problemMonitors, err := s.getProblemMonitors(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Limit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	return &models.DashboardProblemMonitorsResponse{
		Range:           normalized.Range,
		GeneratedAt:     time.Now().UTC(),
		ProblemMonitors: problemMonitors,
	}, nil
}

// GetRecentFailures returns the recent failures dashboard section.
func (s *Service) GetRecentFailures(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentFailuresResponse, error) {
	normalized := normalizeListParams(params, defaultFailuresLimit)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	recentFailures, err := s.getRecentFailures(ctx, tenantID, rangeStart, rangeEndExclusive, normalized.Limit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	return &models.DashboardRecentFailuresResponse{
		Range:          normalized.Range,
		GeneratedAt:    time.Now().UTC(),
		RecentFailures: recentFailures,
	}, nil
}

// GetRecentAlerts returns the recent alerts dashboard section.
func (s *Service) GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentAlertsResponse, error) {
	normalized := normalizeListParams(params, defaultAlertsLimit)

	recentAlerts := []models.AlertWithDetails{}
	if s.alertService != nil {
		alerts, err := s.alertService.GetRecentAlertsForTags(ctx, tenantID, normalized.Tags, normalized.Limit)
		if err != nil {
			return nil, fmt.Errorf("failed to get recent alerts: %w", err)
		}
		recentAlerts = alerts
	}

	return &models.DashboardRecentAlertsResponse{
		Range:        normalized.Range,
		GeneratedAt:  time.Now().UTC(),
		RecentAlerts: recentAlerts,
	}, nil
}

// loadRolling24hData returns the shared 24h aggregates — the enabled
// operational monitor set, the exact-rolling-24h totals, and the hour-aligned
// bucket series, all anchored to a single "now" — through the short-TTL
// rolling24h cache. The summary, problem-monitors, and sparkline requests of
// one dashboard page load therefore cost a single computation per (tenant,
// tag-filter); the returned data and monitorIDs are shared and must be
// treated as read-only.
func (s *Service) loadRolling24hData(ctx context.Context, tenantID uuid.UUID, tags []string) (*rolling24hData, []uuid.UUID, error) {
	return s.rolling24h.Get(rolling24hCacheKey(tenantID, tags), func() (*rolling24hData, []uuid.UUID, error) {
		// Detach from the triggering request's cancellation (see
		// rolling24hBuildTimeout): concurrent waiters share this build.
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rolling24hBuildTimeout)
		defer cancel()

		monitorIDs, err := s.listEnabledOperationalMonitorIDs(buildCtx, tenantID, tags)
		if err != nil {
			return nil, nil, err
		}
		now := time.Now().UTC()
		// Cheap one-row cursor read purely for the lag warning: a failure here
		// is not fatal (the stitched queries below load the cursor themselves
		// and will surface any real error).
		if cursor, err := sharedanalytics.LoadRollupCursor(buildCtx, s.db); err == nil {
			s.warnIfRollupCursorLagging(now, cursor)
		}
		totals, err := loadExactRolling24hSummary(buildCtx, s.db, tenantID, monitorIDs, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load 24h rolling summary: %w", err)
		}
		series, err := loadHourlyBucketSeries24h(buildCtx, s.db, tenantID, monitorIDs, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load 24h hourly bucket series: %w", err)
		}
		return &rolling24hData{
			totals:       totals,
			hourlySeries: series,
		}, monitorIDs, nil
	})
}

// warnIfRollupCursorLagging logs a structured warning when the hourly-rollup
// cursor trails now by more than rollupCursorLagWarnThreshold: every rolling-24h
// dashboard query is then scanning raw check_results for the whole lag tail
// past the cursor. The 24h window is never truncated; this is observability only.
func (s *Service) warnIfRollupCursorLagging(now time.Time, cursor sharedanalytics.RollupCursor) {
	if s.log == nil || cursor.LastCreatedAt == nil {
		return
	}
	lag := now.Sub(cursor.LastCreatedAt.UTC())
	if lag <= rollupCursorLagWarnThreshold {
		return
	}
	s.log.WithFields(map[string]interface{}{
		"cursor_last_created_at": cursor.LastCreatedAt.UTC().Format(time.RFC3339),
		"lag":                    lag.Round(time.Second).String(),
		"threshold":              rollupCursorLagWarnThreshold.String(),
	}).Warn("rollup cursor is lagging; dashboard 24h queries are scanning raw check_results past the cursor")
}
