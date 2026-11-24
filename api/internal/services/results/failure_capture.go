package results

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// A failed HTTP check stores its redacted response with the result, at
// metrics_data.http.failure_response (worker http_failure_capture.go, up to
// ~16 KiB). Reads that return many rows (the history tab's week of results,
// the monitors list's batch, metrics snapshots, and the MCP tools on those
// routes) ship metrics_data through listMetricsDataSQL, which drops the
// capture and leaves http.failure_response_omitted: true, so a monitor down
// for a week behind a large error page does not move tens of megabytes per
// page load. GetMonitorResult returns one row whole; the UI fetches it when
// a failed check's response is opened.

// FailureResponseOmittedKey is the marker a bulk read leaves under
// metrics_data.http where it dropped a capture.
const FailureResponseOmittedKey = "failure_response_omitted"

// listMetricsDataSQL is col (a jsonb expression) without the failure
// capture, as text (” for NULL). col is interpolated, never a user value.
func listMetricsDataSQL(col string) string {
	return fmt.Sprintf(`COALESCE(CASE WHEN %[1]s #> '{http,failure_response}' IS NOT NULL
		THEN jsonb_set(%[1]s #- '{http,failure_response}', '{http,%[2]s}', 'true'::jsonb)
		ELSE %[1]s END::text, '')`, col, FailureResponseOmittedKey)
}

// ErrResultNotFound: the check result is not one of the monitor's.
var ErrResultNotFound = errors.New("check result not found")

// GetMonitorResult returns one of a monitor's raw check results whole, its
// failure capture included (the bulk reads drop it, listMetricsDataSQL).
func (s *Service) GetMonitorResult(ctx context.Context, tenantID, monitorID, resultID uuid.UUID) (*models.CheckResult, error) {
	if _, err := s.getMonitor(ctx, tenantID, monitorID); err != nil {
		return nil, err
	}
	var result models.CheckResult
	var httpStatus, latencyMS sql.NullInt64
	var errorMessage, locationName sql.NullString
	var metricsData string
	err := s.db.QueryRowContext(ctx, `
		SELECT cr.id, cr.status, cr.result_source, cr.http_status, cr.latency_ms, cr.error_message, cr.created_at,
			COALESCE(cr.metrics_data::text, ''), cr.location_id, l.name
		FROM check_results cr
		LEFT JOIN locations l ON l.id = cr.location_id
		WHERE cr.id = $1 AND cr.monitor_id = $2 AND cr.tenant_id = $3
	`, resultID, monitorID, tenantID).Scan(&result.ID, &result.Status, &result.ResultSource, &httpStatus, &latencyMS,
		&errorMessage, &result.CreatedAt, &metricsData, &result.LocationID, &locationName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResultNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get check result: %w", err)
	}
	setNullableResultFields(&result, httpStatus, latencyMS, errorMessage)
	if metricsData != "" {
		result.MetricsData = json.RawMessage(metricsData)
	}
	if locationName.Valid {
		result.LocationName = &locationName.String
	}
	return &result, nil
}
