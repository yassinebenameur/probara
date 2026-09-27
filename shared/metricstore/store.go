package metricstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DBTX is satisfied by *sql.DB and *sql.Tx; ingest runs the whole write path
// in one transaction, maintenance passes use the pool directly.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// SeriesKey identifies and describes one series for ResolveSeries.
type SeriesKey struct {
	TenantID    uuid.UUID
	MonitorID   uuid.UUID
	MetricName  string
	Unit        string
	MetricType  string // "gauge" | "sum"
	IsMonotonic bool
	Temporality string // "cumulative" | "delta" | "unspecified"
	Attributes  map[string]string
}

// Sample is one raw data point for InsertSamples.
type Sample struct {
	SeriesID int64
	TS       time.Time
	Value    float64
}

// ResolveSeries upserts the registry row for a series and returns its id.
// The upsert refreshes last_seen_at, but steady-state ingest should hit its
// (monitor_id, attr_hash) → id cache instead and refresh freshness with one
// batched TouchSeries per report.
func ResolveSeries(ctx context.Context, q DBTX, key SeriesKey) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		INSERT INTO metric_series (
			tenant_id, monitor_id, metric_name, unit, metric_type,
			is_monotonic, temporality, attributes, attr_hash
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (monitor_id, attr_hash) DO UPDATE SET last_seen_at = NOW()
		RETURNING id
	`, key.TenantID, key.MonitorID, key.MetricName, key.Unit, key.MetricType,
		key.IsMonotonic, key.Temporality, CanonicalAttributes(key.Attributes),
		AttrHash(key.MetricName, key.Attributes)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("resolve metric series: %w", err)
	}
	return id, nil
}

// TouchSeries refreshes last_seen_at for a batch of series in one statement.
// Freshness bounds (alert evaluation, status pages) read last_seen_at, so
// this must run for every accepted report — cache hits included.
func TouchSeries(ctx context.Context, q DBTX, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]string, len(ids))
	params := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = fmt.Sprintf("$%d", i+1)
		params[i] = id
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE metric_series SET last_seen_at = NOW() WHERE id IN (`+strings.Join(args, ",")+`)`,
		params...); err != nil {
		return fmt.Errorf("touch metric series: %w", err)
	}
	return nil
}

const insertSamplesChunk = 1000

// InsertSamples writes raw samples in chunked multi-row inserts, idempotent
// on (series_id, ts) so an OTLP retry after a dropped response cannot
// duplicate points. Returns the number of rows actually inserted.
func InsertSamples(ctx context.Context, q DBTX, rows []Sample) (int, error) {
	total := 0
	for start := 0; start < len(rows); start += insertSamplesChunk {
		end := start + insertSamplesChunk
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]
		values := make([]string, len(chunk))
		params := make([]interface{}, 0, len(chunk)*3)
		for i, r := range chunk {
			values[i] = fmt.Sprintf("($%d,$%d,$%d)", i*3+1, i*3+2, i*3+3)
			params = append(params, r.SeriesID, r.TS, r.Value)
		}
		res, err := q.ExecContext(ctx,
			`INSERT INTO metric_samples (series_id, ts, value) VALUES `+
				strings.Join(values, ",")+` ON CONFLICT (series_id, ts) DO NOTHING`,
			params...)
		if err != nil {
			return total, fmt.Errorf("insert metric samples: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("insert metric samples rows affected: %w", err)
		}
		total += int(n)
	}
	return total, nil
}

// MarkDirty marks (monitor, hour) buckets in metric_rollup_dirty, same
// contract as the check-result rollup ledger: DO UPDATE refreshes marked_at
// so a consumer mid-rebuild leaves a re-marked bucket for the next run.
func MarkDirty(ctx context.Context, q DBTX, monitorID uuid.UUID, hours []time.Time) error {
	for _, h := range hours {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO metric_rollup_dirty (monitor_id, bucket_hour)
			VALUES ($1, date_trunc('hour', $2::timestamptz))
			ON CONFLICT (monitor_id, bucket_hour) DO UPDATE SET marked_at = clock_timestamp()
		`, monitorID, h); err != nil {
			return fmt.Errorf("mark metric rollup bucket dirty: %w", err)
		}
	}
	return nil
}

// CountSeries returns the registry cardinality for a monitor (the ingest
// guardrail input).
func CountSeries(ctx context.Context, q DBTX, monitorID uuid.UUID) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metric_series WHERE monitor_id = $1`, monitorID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count metric series: %w", err)
	}
	return n, nil
}

// EnsurePartitions creates the daily metric_samples partitions covering
// [from, to] (UTC days, inclusive). Idempotent; used by the scheduler's
// partition maintenance and as the ingest fallback when an insert hits a
// missing partition.
func EnsurePartitions(ctx context.Context, q DBTX, from, to time.Time) error {
	day := from.UTC().Truncate(24 * time.Hour)
	last := to.UTC().Truncate(24 * time.Hour)
	for !day.After(last) {
		next := day.Add(24 * time.Hour)
		stmt := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS metric_samples_%s PARTITION OF metric_samples FOR VALUES FROM ('%s') TO ('%s')`,
			day.Format("20060102"),
			day.Format("2006-01-02 15:04:05+00"),
			next.Format("2006-01-02 15:04:05+00"),
		)
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create metric_samples partition for %s: %w", day.Format("2006-01-02"), err)
		}
		day = next
	}
	return nil
}

// IsMissingPartitionErr reports whether an insert failed because no
// partition covers the row's timestamp (the ingest fallback trigger).
func IsMissingPartitionErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no partition of relation")
}
