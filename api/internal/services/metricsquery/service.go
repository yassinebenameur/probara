// Package metricsquery serves the operator UI's read access to the generic
// metric store: series discovery and the batched range query. It is a thin
// tenant-scoping layer over shared/metricstore — aggregation, raw-vs-rollup
// selection, and rate derivation live there.
package metricsquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/metricstore"
)

// ErrMonitorNotFound maps to 404 (also covers cross-tenant probing).
var ErrMonitorNotFound = errors.New("monitor not found")

const (
	maxQueriesPerRequest = 12
	maxQueryRange        = 90 * 24 * time.Hour
	minStepSeconds       = 10
	maxStepSeconds       = 86400
)

// Service answers metric discovery and range queries.
type Service struct {
	db *sql.DB
}

// NewService creates the query service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// SeriesItem is one discovery row.
type SeriesItem struct {
	SeriesKey  string            `json:"series_key"`
	MetricName string            `json:"metric_name"`
	Attributes map[string]string `json:"attributes"`
	Unit       string            `json:"unit"`
	MetricType string            `json:"metric_type"` // gauge | counter
	LastSeenAt time.Time         `json:"last_seen_at"`
}

// QueryRequest is the batch body of POST /monitors/{id}/metrics/query.
type QueryRequest struct {
	Start       time.Time   `json:"start"`
	End         time.Time   `json:"end"`
	StepSeconds int         `json:"step_seconds"`
	Queries     []QuerySpec `json:"queries"`
}

// QuerySpec is one query in the batch; Ref keys its result.
type QuerySpec struct {
	Ref              string            `json:"ref"`
	MetricName       string            `json:"metric_name"`
	AttributeFilters map[string]string `json:"attribute_filters,omitempty"`
	Agg              string            `json:"agg,omitempty"`
	Rate             bool              `json:"rate,omitempty"`
}

// QueryResponse carries all per-ref results.
type QueryResponse struct {
	Results []QueryRefResult `json:"results"`
}

// QueryRefResult is one query's fan-out.
type QueryRefResult struct {
	Ref         string       `json:"ref"`
	StepSeconds int          `json:"step_seconds"`
	Source      string       `json:"source"`
	Truncated   bool         `json:"truncated"`
	Series      []SeriesData `json:"series"`
}

// SeriesData is one matched series with aggregated points.
type SeriesData struct {
	SeriesKey  string            `json:"series_key"`
	MetricName string            `json:"metric_name"`
	Attributes map[string]string `json:"attributes"`
	Unit       string            `json:"unit"`
	MetricType string            `json:"metric_type"`
	// Points are [epoch_ms, value] pairs, bucket starts, ascending.
	Points [][2]float64 `json:"points"`
}

// ValidationError distinguishes 422-worthy input problems from 500s.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func validationErrorf(format string, args ...interface{}) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// IsValidationError reports whether err is a client-input problem.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func (s *Service) verifyMonitor(ctx context.Context, monitorID, tenantID uuid.UUID) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM monitors WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		)
	`, monitorID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("verify monitor: %w", err)
	}
	if !exists {
		return ErrMonitorNotFound
	}
	return nil
}

// ListSeries returns every registered series of the monitor.
func (s *Service) ListSeries(ctx context.Context, monitorID, tenantID uuid.UUID) ([]SeriesItem, error) {
	if err := s.verifyMonitor(ctx, monitorID, tenantID); err != nil {
		return nil, err
	}
	infos, err := metricstore.ListSeries(ctx, s.db, tenantID, monitorID, nil)
	if err != nil {
		return nil, err
	}
	items := make([]SeriesItem, 0, len(infos))
	for _, info := range infos {
		items = append(items, SeriesItem{
			SeriesKey:  metricstore.SeriesKeyString(info.MetricName, info.Attributes),
			MetricName: info.MetricName,
			Attributes: info.Attributes,
			Unit:       info.Unit,
			MetricType: info.MetricType,
			LastSeenAt: info.LastSeenAt,
		})
	}
	return items, nil
}

// Query runs the batch. Specs run sequentially — one dashboard load is a
// single request, and the store queries are index-bound.
func (s *Service) Query(ctx context.Context, monitorID, tenantID uuid.UUID, req QueryRequest) (*QueryResponse, error) {
	if err := s.verifyMonitor(ctx, monitorID, tenantID); err != nil {
		return nil, err
	}
	if req.Start.IsZero() || req.End.IsZero() || !req.End.After(req.Start) {
		return nil, validationErrorf("start must be before end")
	}
	if req.End.Sub(req.Start) > maxQueryRange {
		return nil, validationErrorf("range too large (max %d days)", int(maxQueryRange.Hours()/24))
	}
	if req.StepSeconds < minStepSeconds || req.StepSeconds > maxStepSeconds {
		return nil, validationErrorf("step_seconds must be between %d and %d", minStepSeconds, maxStepSeconds)
	}
	if len(req.Queries) == 0 || len(req.Queries) > maxQueriesPerRequest {
		return nil, validationErrorf("queries must contain 1-%d entries", maxQueriesPerRequest)
	}

	resp := &QueryResponse{Results: make([]QueryRefResult, 0, len(req.Queries))}
	for i, q := range req.Queries {
		if q.MetricName == "" {
			return nil, validationErrorf("queries[%d]: metric_name is required", i)
		}
		result, err := metricstore.Query(ctx, s.db, metricstore.QuerySpec{
			TenantID:         tenantID,
			MonitorID:        monitorID,
			MetricName:       q.MetricName,
			AttributeFilters: q.AttributeFilters,
			From:             req.Start,
			To:               req.End,
			StepSeconds:      req.StepSeconds,
			Agg:              q.Agg,
			Rate:             q.Rate,
		})
		if err != nil {
			if errors.Is(err, metricstore.ErrBadQuery) {
				return nil, validationErrorf("queries[%d] (%s): %v", i, q.Ref, err)
			}
			return nil, fmt.Errorf("query %s: %w", q.Ref, err)
		}
		ref := QueryRefResult{
			Ref:         q.Ref,
			StepSeconds: result.StepSeconds,
			Source:      result.Source,
			Truncated:   result.Truncated,
			Series:      make([]SeriesData, 0, len(result.Series)),
		}
		for _, sd := range result.Series {
			points := make([][2]float64, 0, len(sd.Points))
			for _, p := range sd.Points {
				points = append(points, [2]float64{float64(p.TSMillis), p.Value})
			}
			ref.Series = append(ref.Series, SeriesData{
				SeriesKey:  metricstore.SeriesKeyString(sd.MetricName, sd.Attributes),
				MetricName: sd.MetricName,
				Attributes: sd.Attributes,
				Unit:       sd.Unit,
				MetricType: sd.MetricType,
				Points:     points,
			})
		}
		resp.Results = append(resp.Results, ref)
	}
	return resp, nil
}
