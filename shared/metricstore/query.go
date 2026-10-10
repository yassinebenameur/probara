package metricstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// namesArray adapts a []string for `= ANY($n)` under the lib/pq driver.
func namesArray(names []string) interface{} {
	return pq.Array(names)
}

// SeriesInfo is a registry row as exposed by discovery and query results.
type SeriesInfo struct {
	SeriesID    int64
	MetricName  string
	Attributes  map[string]string
	Unit        string
	MetricType  string // "gauge" | "counter" (counter = monotonic sum)
	LastSeenAt  time.Time
	IsMonotonic bool
}

// LatestSample is the newest raw sample of one fresh series.
type LatestSample struct {
	SeriesInfo
	TS    time.Time
	Value float64
}

// simplifyType collapses the stored (metric_type, is_monotonic) pair into
// the two-valued type the HTTP API and rules reason about.
func simplifyType(metricType string, isMonotonic bool) string {
	if metricType == "sum" && isMonotonic {
		return "counter"
	}
	return "gauge"
}

// ListSeries returns the registry rows for a monitor, optionally restricted
// to metricNames (nil = all). Tenant scoping is the caller's WHERE input —
// both ids come from an already-authorized monitor lookup.
func ListSeries(ctx context.Context, q DBTX, tenantID, monitorID uuid.UUID, metricNames []string) ([]SeriesInfo, error) {
	query := `
		SELECT id, metric_name, attributes, unit, metric_type, is_monotonic, last_seen_at
		FROM metric_series
		WHERE tenant_id = $1 AND monitor_id = $2`
	args := []interface{}{tenantID, monitorID}
	if len(metricNames) > 0 {
		query += ` AND metric_name = ANY($3)`
		args = append(args, namesArray(metricNames))
	}
	query += ` ORDER BY metric_name, id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list metric series: %w", err)
	}
	defer rows.Close()

	var out []SeriesInfo
	for rows.Next() {
		info, err := scanSeriesInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// LatestSamples returns the newest sample per series for the monitor,
// restricted to metricNames (nil = all) and to series seen within
// `freshness` — the bound that keeps a dead agent's last readings from being
// evaluated forever (the alerter and status pages must always pass one,
// normally monitorstate.FreshnessHorizonSeconds of the monitor interval).
func LatestSamples(ctx context.Context, q DBTX, tenantID, monitorID uuid.UUID, metricNames []string, freshness time.Duration) ([]LatestSample, error) {
	query := `
		SELECT s.id, s.metric_name, s.attributes, s.unit, s.metric_type, s.is_monotonic, s.last_seen_at,
		       ms.ts, ms.value
		FROM metric_series s
		JOIN LATERAL (
			SELECT ts, value FROM metric_samples
			WHERE series_id = s.id
			ORDER BY ts DESC LIMIT 1
		) ms ON TRUE
		WHERE s.tenant_id = $1 AND s.monitor_id = $2
		  AND s.last_seen_at >= NOW() - ($3 * INTERVAL '1 second')`
	args := []interface{}{tenantID, monitorID, int64(freshness.Seconds())}
	if len(metricNames) > 0 {
		query += ` AND s.metric_name = ANY($4)`
		args = append(args, namesArray(metricNames))
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("latest metric samples: %w", err)
	}
	defer rows.Close()

	var out []LatestSample
	for rows.Next() {
		var ls LatestSample
		var attrsJSON []byte
		var rawType string
		if err := rows.Scan(&ls.SeriesID, &ls.MetricName, &attrsJSON, &ls.Unit,
			&rawType, &ls.IsMonotonic, &ls.LastSeenAt, &ls.TS, &ls.Value); err != nil {
			return nil, fmt.Errorf("scan latest metric sample: %w", err)
		}
		if err := json.Unmarshal(attrsJSON, &ls.Attributes); err != nil {
			return nil, fmt.Errorf("decode series attributes: %w", err)
		}
		ls.MetricType = simplifyType(rawType, ls.IsMonotonic)
		out = append(out, ls)
	}
	return out, rows.Err()
}

// SamplePoint is one raw (ts, value) pair.
type SamplePoint struct {
	TS    time.Time
	Value float64
}

// RangeSamples returns the raw samples of one series in [from, to],
// ascending — the sustained-breach (for_duration) evaluation input.
func RangeSamples(ctx context.Context, q DBTX, seriesID int64, from, to time.Time) ([]SamplePoint, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ts, value FROM metric_samples
		WHERE series_id = $1 AND ts >= $2 AND ts <= $3
		ORDER BY ts
	`, seriesID, from, to)
	if err != nil {
		return nil, fmt.Errorf("range metric samples: %w", err)
	}
	defer rows.Close()

	var out []SamplePoint
	for rows.Next() {
		var p SamplePoint
		if err := rows.Scan(&p.TS, &p.Value); err != nil {
			return nil, fmt.Errorf("scan metric sample: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanSeriesInfo(rows interface {
	Scan(dest ...interface{}) error
}) (SeriesInfo, error) {
	var info SeriesInfo
	var attrsJSON []byte
	var rawType string
	if err := rows.Scan(&info.SeriesID, &info.MetricName, &attrsJSON, &info.Unit,
		&rawType, &info.IsMonotonic, &info.LastSeenAt); err != nil {
		return info, fmt.Errorf("scan metric series: %w", err)
	}
	if err := json.Unmarshal(attrsJSON, &info.Attributes); err != nil {
		return info, fmt.Errorf("decode series attributes: %w", err)
	}
	info.MetricType = simplifyType(rawType, info.IsMonotonic)
	return info, nil
}
