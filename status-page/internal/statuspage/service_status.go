// Current status and recent check history of a single monitor.

package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/tlscert"
)

// CurrentStatus represents the current status of a monitor
type CurrentStatus struct {
	Status             string
	LastCheckTime      *time.Time
	LastHTTPStatus     *int
	LastLatency        *int
	TLSDaysUntilExpiry *int
	TLSNotAfter        string
}

// applyCertificate copies the certificate a check recorded in its
// metrics_data (an HTTPS or SIP-over-TLS check's) onto the status. The
// location is shared/tlscert's, the same one the alerter's tls_expiry
// evaluation reads, so the page and the alert never disagree on it.
func (cs *CurrentStatus) applyCertificate(metricsJSON []byte) {
	info := tlscert.Extract(metricsJSON)
	if info == nil {
		return
	}
	if info.DaysUntilExpiry != nil {
		cs.TLSDaysUntilExpiry = info.DaysUntilExpiry
	}
	if info.NotAfter != "" {
		cs.TLSNotAfter = info.NotAfter
	}
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

	result.applyCertificate(metricsJSON)

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
