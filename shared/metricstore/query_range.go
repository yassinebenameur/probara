package metricstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ErrBadQuery marks semantic misuse of a query spec (unknown aggregation,
// rate over a non-counter) as opposed to storage failures — callers map it
// to a 4xx.
var ErrBadQuery = errors.New("bad metric query")

// Range-query engine behind POST /monitors/{id}/metrics/query. One spec can
// fan out to several series (attribute_filters is a subset match); the
// server — not the client — derives counter rates so temporality never
// leaks past this package.

const (
	// Ranges longer than this (or steps of an hour and up) read the hourly
	// rollups instead of raw samples.
	rollupRangeThreshold = 48 * time.Hour
	// DefaultMaxSeriesPerQuery bounds fan-out per query spec.
	DefaultMaxSeriesPerQuery = 50
	// DefaultMaxPointsPerSeries bounds response size per series.
	DefaultMaxPointsPerSeries = 2000
)

// QuerySpec is one aggregated range query over a monitor's series.
type QuerySpec struct {
	TenantID         uuid.UUID
	MonitorID        uuid.UUID
	MetricName       string
	AttributeFilters map[string]string
	From, To         time.Time
	StepSeconds      int
	Agg              string // avg|min|max|sum|last
	Rate             bool   // per-second rate; monotonic sums only
}

// Point is one aggregated bucket. TSMillis is the bucket start (UTC).
type Point struct {
	TSMillis int64
	Value    float64
}

// SeriesData is one series' aggregated result.
type SeriesData struct {
	SeriesInfo
	Points []Point
}

// QueryResult carries the fan-out result plus what the engine actually did.
type QueryResult struct {
	StepSeconds int    // effective step (coerced up for rollups)
	Source      string // "raw" | "rollup"
	Truncated   bool   // series fan-out exceeded the cap
	Series      []SeriesData
}

// Query runs one spec. Validation (metric name shape, agg whitelist, sane
// range) is the caller's job; this enforces only the structural caps.
func Query(ctx context.Context, q DBTX, spec QuerySpec) (*QueryResult, error) {
	switch spec.Agg {
	case "avg", "min", "max", "sum", "last":
	case "":
		spec.Agg = "avg"
	default:
		return nil, fmt.Errorf("%w: unsupported aggregation %q", ErrBadQuery, spec.Agg)
	}

	useRollup := spec.To.Sub(spec.From) > rollupRangeThreshold || spec.StepSeconds >= 3600
	step := spec.StepSeconds
	if step <= 0 {
		step = 60
	}
	if useRollup {
		if step < 3600 {
			step = 3600
		}
		step = (step / 3600) * 3600
	}
	// Clamp the step so one series never exceeds the point cap.
	if minStep := int(spec.To.Sub(spec.From).Seconds()) / DefaultMaxPointsPerSeries; step < minStep {
		step = minStep
	}

	series, truncated, err := matchSeries(ctx, q, spec)
	if err != nil {
		return nil, err
	}
	result := &QueryResult{StepSeconds: step, Source: "raw", Truncated: truncated}
	if useRollup {
		result.Source = "rollup"
	}
	if len(series) == 0 {
		return result, nil
	}
	if spec.Rate {
		for _, s := range series {
			if s.MetricType != "counter" {
				return nil, fmt.Errorf("%w: rate requires a monotonic sum; %s is a %s", ErrBadQuery, SeriesKeyString(s.MetricName, s.Attributes), s.MetricType)
			}
		}
	}

	ids := make([]int64, len(series))
	byID := make(map[int64]*SeriesData, len(series))
	for i := range series {
		ids[i] = series[i].SeriesID
		sd := &SeriesData{SeriesInfo: series[i]}
		byID[series[i].SeriesID] = sd
	}

	var rowsErr error
	switch {
	case useRollup:
		rowsErr = queryRollup(ctx, q, spec, ids, step, byID)
	case spec.Rate:
		rowsErr = queryRawRate(ctx, q, spec, ids, step, byID)
	default:
		rowsErr = queryRawAgg(ctx, q, spec, ids, step, byID)
	}
	if rowsErr != nil {
		return nil, rowsErr
	}

	for _, s := range series {
		result.Series = append(result.Series, *byID[s.SeriesID])
	}
	return result, nil
}

func matchSeries(ctx context.Context, q DBTX, spec QuerySpec) ([]SeriesInfo, bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, metric_name, attributes, unit, metric_type, is_monotonic, last_seen_at
		FROM metric_series
		WHERE tenant_id = $1 AND monitor_id = $2 AND metric_name = $3
		  AND attributes @> $4::jsonb
		ORDER BY id
		LIMIT $5
	`, spec.TenantID, spec.MonitorID, spec.MetricName,
		CanonicalAttributes(spec.AttributeFilters), DefaultMaxSeriesPerQuery+1)
	if err != nil {
		return nil, false, fmt.Errorf("match metric series: %w", err)
	}
	defer rows.Close()

	var out []SeriesInfo
	for rows.Next() {
		info, err := scanSeriesInfo(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > DefaultMaxSeriesPerQuery {
		return out[:DefaultMaxSeriesPerQuery], true, nil
	}
	return out, false, nil
}

var aggExpr = map[string]string{
	"avg":  "AVG(value)",
	"min":  "MIN(value)",
	"max":  "MAX(value)",
	"sum":  "SUM(value)",
	"last": "(array_agg(value ORDER BY ts DESC))[1]",
}

func queryRawAgg(ctx context.Context, q DBTX, spec QuerySpec, ids []int64, step int, byID map[int64]*SeriesData) error {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT series_id,
		       (floor(extract(epoch FROM ts) / $2)::bigint * $2) AS bucket,
		       %s
		FROM metric_samples
		WHERE series_id = ANY($1) AND ts >= $3 AND ts <= $4
		GROUP BY series_id, bucket
		ORDER BY series_id, bucket
	`, aggExpr[spec.Agg]), pq.Array(ids), step, spec.From, spec.To)
	if err != nil {
		return fmt.Errorf("raw metric query: %w", err)
	}
	return collectPoints(rows, byID)
}

// queryRawRate derives reset-aware per-second rates: each sample's positive
// delta vs its predecessor (a drop = counter reset, delta := value) lands in
// the bucket of the later sample; bucket rate = Σdeltas / step. The scan
// window extends before `from` so the first in-range sample has a
// predecessor.
func queryRawRate(ctx context.Context, q DBTX, spec QuerySpec, ids []int64, step int, byID map[int64]*SeriesData) error {
	lookback := time.Duration(step) * time.Second
	if lookback < 5*time.Minute {
		lookback = 5 * time.Minute
	}
	rows, err := q.QueryContext(ctx, `
		SELECT series_id, bucket, GREATEST(SUM(delta), 0) / $2
		FROM (
			SELECT series_id,
			       (floor(extract(epoch FROM ts) / $2)::bigint * $2) AS bucket,
			       CASE WHEN value >= prev THEN value - prev ELSE value END AS delta
			FROM (
				SELECT series_id, ts, value,
				       lag(value) OVER (PARTITION BY series_id ORDER BY ts) AS prev
				FROM metric_samples
				WHERE series_id = ANY($1) AND ts >= $5 AND ts <= $4
			) w
			WHERE prev IS NOT NULL
		) d
		WHERE bucket >= floor(extract(epoch FROM $3::timestamptz) / $2)::bigint * $2
		GROUP BY series_id, bucket
		ORDER BY series_id, bucket
	`, pq.Array(ids), step, spec.From, spec.To, spec.From.Add(-lookback))
	if err != nil {
		return fmt.Errorf("raw metric rate query: %w", err)
	}
	return collectPoints(rows, byID)
}

var rollupAggExpr = map[string]string{
	"avg":  "SUM(sum_value) / NULLIF(SUM(sample_count), 0)",
	"min":  "MIN(min_value)",
	"max":  "MAX(max_value)",
	"sum":  "SUM(sum_value)",
	"last": "(array_agg(last_value ORDER BY bucket DESC))[1]",
}

func queryRollup(ctx context.Context, q DBTX, spec QuerySpec, ids []int64, step int, byID map[int64]*SeriesData) error {
	expr := rollupAggExpr[spec.Agg]
	if spec.Rate {
		// increase is the reset-aware in-bucket growth; dividing the grouped
		// sum by the group width yields the per-second rate.
		expr = "COALESCE(SUM(increase), 0) / $2"
	}
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT series_id,
		       (floor(extract(epoch FROM bucket) / $2)::bigint * $2) AS b,
		       %s
		FROM metric_rollups_hourly
		WHERE series_id = ANY($1) AND bucket >= $3 AND bucket <= $4
		GROUP BY series_id, b
		ORDER BY series_id, b
	`, expr), pq.Array(ids), step, spec.From, spec.To)
	if err != nil {
		return fmt.Errorf("rollup metric query: %w", err)
	}
	return collectPoints(rows, byID)
}

func collectPoints(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Close() error
	Err() error
}, byID map[int64]*SeriesData) error {
	defer rows.Close()
	for rows.Next() {
		var seriesID int64
		var bucketEpoch int64
		var value *float64
		if err := rows.Scan(&seriesID, &bucketEpoch, &value); err != nil {
			return fmt.Errorf("scan metric point: %w", err)
		}
		sd, ok := byID[seriesID]
		if !ok || value == nil {
			continue
		}
		sd.Points = append(sd.Points, Point{TSMillis: bucketEpoch * 1000, Value: *value})
	}
	return rows.Err()
}
