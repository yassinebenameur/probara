package results

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// Service handles results business logic
type Service struct {
	db           db.DB
	groupService GroupReader
	analytics    sharedanalytics.Reader
}

// GroupReader provides the group lookups required by the results service.
type GroupReader interface {
	GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error)
	GetGroupStatus(ctx context.Context, tenantID, groupID uuid.UUID) (string, error)
}

const (
	defaultMonitorResultsLimit = 50
	maxMonitorResultsLimit     = 1000000
	dynamicLimitFloor          = 200
	dynamicLimitPadding        = 100
	dynamicLimitHeadroomNum    = 115
	dynamicLimitHeadroomDen    = 100
)

// NewService creates a new results service
func NewService(database db.DB, groupSvc GroupReader, analytics sharedanalytics.Reader) *Service {
	return &Service{
		db:           database,
		groupService: groupSvc,
		analytics:    analytics,
	}
}

// GetMonitorResults retrieves check results for a monitor
func (s *Service) GetMonitorResults(ctx context.Context, tenantID, monitorID uuid.UUID, limit int, since *time.Time) (*models.MonitorResultsResponse, error) {
	// First verify the monitor exists and belongs to the tenant
	monitor, err := s.getMonitor(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = computeDynamicLimit(since, monitor.IntervalSeconds)
		if limit <= 0 {
			limit = defaultMonitorResultsLimit
		}
	}
	if limit > maxMonitorResultsLimit {
		limit = maxMonitorResultsLimit
	}

	// If this is a group monitor, aggregate results from all member monitors
	if monitor.Type == models.MonitorTypeGroup {
		return s.getGroupResults(ctx, tenantID, monitorID, limit, since)
	}

	// Regular monitor - fetch its own results
	return s.getRegularResults(ctx, tenantID, monitorID, limit, since)
}

func (s *Service) GetMonitorAnalytics(ctx context.Context, tenantID, monitorID uuid.UUID, rangeValue models.MonitorAnalyticsRange) (*models.MonitorAnalyticsResponse, error) {
	monitor, err := s.getMonitor(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	monitorIDs := []uuid.UUID{monitorID}
	if monitor.Type == models.MonitorTypeGroup {
		members, err := s.groupService.GetGroupLeafMembers(ctx, tenantID, monitorID)
		if err != nil {
			return nil, err
		}
		monitorIDs = make([]uuid.UUID, 0, len(members))
		for _, member := range members {
			monitorIDs = append(monitorIDs, member.ID)
		}
	}

	analyticsResult, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, sharedanalytics.Range(rangeValue), time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor analytics: %w", err)
	}

	response := &models.MonitorAnalyticsResponse{
		MonitorID:       monitorID,
		Range:           rangeValue,
		GeneratedAt:     analyticsResult.GeneratedAt,
		Source:          models.AnalyticsSource(analyticsResult.Source),
		CoverageStart:   analyticsResult.CoverageStart,
		IsPartial:       analyticsResult.IsPartial,
		Summary:         mapSummary(analyticsResult.Summary),
		UptimeSeries:    make([]models.MonitorAnalyticsSeriesPoint, 0, len(analyticsResult.Series)),
		LatencySeries:   make([]models.MonitorAnalyticsSeriesPoint, 0, len(analyticsResult.Series)),
		DowntimePeriods: make([]models.MonitorAnalyticsDowntimePeriod, 0, len(analyticsResult.Downtime)),
	}
	for _, point := range analyticsResult.Series {
		seriesPoint := models.MonitorAnalyticsSeriesPoint{
			BucketStart:  point.BucketStart,
			UptimePct:    point.UptimePct,
			AvgLatencyMS: point.AvgLatencyMS,
			TotalChecks:  point.TotalChecks,
			HasData:      point.HasData,
		}
		response.UptimeSeries = append(response.UptimeSeries, seriesPoint)
		response.LatencySeries = append(response.LatencySeries, seriesPoint)
	}
	for _, period := range analyticsResult.Downtime {
		response.DowntimePeriods = append(response.DowntimePeriods, models.MonitorAnalyticsDowntimePeriod{
			StartTime: period.Start,
			EndTime:   period.End,
			IsOpen:    period.IsOpen,
		})
	}
	return response, nil
}

// getGroupResults retrieves aggregated results for a group monitor
func (s *Service) getGroupResults(ctx context.Context, tenantID, monitorID uuid.UUID, limit int, since *time.Time) (*models.MonitorResultsResponse, error) {
	members, err := s.groupService.GetGroupLeafMembers(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}

	if len(members) == 0 {
		return &models.MonitorResultsResponse{
			MonitorID: monitorID,
			Results:   []models.CheckResult{},
		}, nil
	}

	// Build a query that fetches results from all member monitors
	memberIDs := make([]interface{}, len(members))
	for i, member := range members {
		memberIDs[i] = member.ID
	}

	var query string
	var args []interface{}

	if since != nil {
		query = `
			SELECT id, status, result_source, http_status, latency_ms, error_message, created_at
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= $3
			ORDER BY created_at DESC
			LIMIT $4
		`
		args = []interface{}{pq.Array(memberIDs), tenantID, *since, limit}
	} else {
		query = `
			SELECT id, status, result_source, http_status, latency_ms, error_message, created_at
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2
			ORDER BY created_at DESC
			LIMIT $3
		`
		args = []interface{}{pq.Array(memberIDs), tenantID, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query check results: %w", err)
	}
	defer rows.Close()

	var results []models.CheckResult
	for rows.Next() {
		var result models.CheckResult
		var httpStatus sql.NullInt64
		var latencyMS sql.NullInt64
		var errorMessage sql.NullString

		err := rows.Scan(&result.ID, &result.Status, &result.ResultSource, &httpStatus, &latencyMS, &errorMessage, &result.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan check result: %w", err)
		}

		if httpStatus.Valid {
			httpStatusInt := int(httpStatus.Int64)
			result.HTTPStatus = &httpStatusInt
		}
		if latencyMS.Valid {
			latencyInt := int(latencyMS.Int64)
			result.LatencyMS = &latencyInt
		}
		if errorMessage.Valid {
			result.ErrorMessage = &errorMessage.String
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating check results: %w", err)
	}

	// Calculate current aggregated status for the group
	groupStatus, err := s.groupService.GetGroupStatus(ctx, tenantID, monitorID)
	if err != nil {
		// If we can't get group status, just return the member results
		return &models.MonitorResultsResponse{
			MonitorID: monitorID,
			Results:   results,
		}, nil
	}

	// Create a synthetic aggregated result to show at the top
	// This represents the current status of the group
	if groupStatus != "unknown" && len(results) > 0 {
		syntheticResult := models.CheckResult{
			ID:           uuid.New(),
			Status:       groupStatus,
			ResultSource: string(sharedmodels.ResultSourceDerived),
			CreatedAt:    time.Now(),
		}

		// Prepend the synthetic result
		results = append([]models.CheckResult{syntheticResult}, results...)
	}

	return &models.MonitorResultsResponse{
		MonitorID: monitorID,
		Results:   results,
	}, nil
}

// getRegularResults retrieves results for a regular (non-group) monitor
func (s *Service) getRegularResults(ctx context.Context, tenantID, monitorID uuid.UUID, limit int, since *time.Time) (*models.MonitorResultsResponse, error) {
	var query string
	var args []interface{}

	if since != nil {
		query = `
			SELECT id, status, result_source, http_status, latency_ms, error_message, created_at, COALESCE(metrics_data::text, '')
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2 AND created_at >= $3
			ORDER BY created_at DESC
			LIMIT $4
		`
		args = []interface{}{monitorID, tenantID, *since, limit}
	} else {
		query = `
			SELECT id, status, result_source, http_status, latency_ms, error_message, created_at, COALESCE(metrics_data::text, '')
			FROM check_results
			WHERE monitor_id = $1 AND tenant_id = $2
			ORDER BY created_at DESC
			LIMIT $3
		`
		args = []interface{}{monitorID, tenantID, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query check results: %w", err)
	}
	defer rows.Close()

	var results []models.CheckResult
	for rows.Next() {
		var result models.CheckResult
		var httpStatus sql.NullInt64
		var latencyMS sql.NullInt64
		var errorMessage sql.NullString
		var metricsDataStr string

		err := rows.Scan(&result.ID, &result.Status, &result.ResultSource, &httpStatus, &latencyMS, &errorMessage, &result.CreatedAt, &metricsDataStr)
		if err != nil {
			return nil, fmt.Errorf("failed to scan check result: %w", err)
		}

		if httpStatus.Valid {
			httpStatusInt := int(httpStatus.Int64)
			result.HTTPStatus = &httpStatusInt
		}
		if latencyMS.Valid {
			latencyInt := int(latencyMS.Int64)
			result.LatencyMS = &latencyInt
		}
		if errorMessage.Valid {
			result.ErrorMessage = &errorMessage.String
		}
		if metricsDataStr != "" {
			result.MetricsData = json.RawMessage(metricsDataStr)
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating check results: %w", err)
	}

	return &models.MonitorResultsResponse{
		MonitorID: monitorID,
		Results:   results,
	}, nil
}

// getMonitor is a helper to retrieve a monitor by ID
func (s *Service) getMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	query := `
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, next_run_at, created_at, updated_at
		FROM monitors
		WHERE id = $1 AND tenant_id = $2
	`

	var monitor models.Monitor
	var tags []string

	err := s.db.QueryRowContext(ctx, query, monitorID, tenantID).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type,
		&monitor.Config, &monitor.IntervalSeconds, &monitor.TimeoutSeconds,
		&monitor.AlertPolicyID, &monitor.Enabled,
		pq.Array(&tags), &monitor.AgentID, &monitor.NextRunAt, &monitor.CreatedAt, &monitor.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor not found")
		}
		return nil, fmt.Errorf("failed to get monitor: %w", err)
	}

	monitor.Tags = tags
	return &monitor, nil
}

func computeDynamicLimit(since *time.Time, intervalSeconds int) int {
	if since == nil {
		return 0
	}

	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	duration := time.Since(since.UTC())
	if duration <= 0 {
		return dynamicLimitFloor
	}

	expectedPoints := int(duration.Seconds() / float64(intervalSeconds))
	if expectedPoints < 1 {
		expectedPoints = 1
	}

	limit := (expectedPoints*dynamicLimitHeadroomNum)/dynamicLimitHeadroomDen + dynamicLimitPadding
	if limit < dynamicLimitFloor {
		limit = dynamicLimitFloor
	}

	return limit
}

func mapSummary(summary sharedanalytics.Summary) models.MonitorAnalyticsSummary {
	return models.MonitorAnalyticsSummary{
		UptimePct:       summary.UptimePct,
		SLAPct:          summary.SLAPct,
		DowntimePct:     summary.DowntimePct,
		AvgLatencyMS:    summary.AvgLatencyMS,
		MedianLatencyMS: summary.MedianLatencyMS,
		P95LatencyMS:    summary.P95LatencyMS,
		LatestStatus:    summary.LatestStatus,
		LatestCheckAt:   summary.LatestCheckAt,
	}
}
