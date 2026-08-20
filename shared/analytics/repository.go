package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
)

type Repository struct {
	db db.DB
}

func NewRepository(database db.DB) *Repository {
	return &Repository{db: database}
}

type rawRow struct {
	MonitorID uuid.UUID
	Status    string
	LatencyMS sql.NullInt64
	CreatedAt time.Time
}

type rollupRow struct {
	MonitorID           uuid.UUID
	BucketDay           time.Time
	TotalChecks         int
	SuccessChecks       int
	LatencySuccessSumMS float64
	LatencySuccessCount int
	LatestStatus        sql.NullString
	LatestCheckAt       sql.NullTime
}

type uptimeAccumulator struct {
	totalChecks   int
	successChecks int
	latencies     []float64
	latestStatus  *string
	latestCheckAt *time.Time
}

type dayAccumulator struct {
	totalChecks         int
	successChecks       int
	latencySuccessSumMS float64
	latencySuccessCount int
	latestStatus        *string
	latestCheckAt       *time.Time
}

// avgLatencyMS returns the day's average success latency, or nil when the day
// had no successful checks with latency data.
func (a dayAccumulator) avgLatencyMS() *float64 {
	if a.latencySuccessCount <= 0 {
		return nil
	}
	return PtrFloat64(a.latencySuccessSumMS / float64(a.latencySuccessCount))
}

func (r *Repository) GetScopeAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, rangeValue Range, now time.Time) (*Result, error) {
	monitorIDs = dedupeUUIDs(monitorIDs)
	window, err := ResolveWindow(rangeValue, now)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return &Result{
			Range:       rangeValue,
			GeneratedAt: now.UTC(),
			Source:      sourceForRange(rangeValue),
			IsPartial:   true,
			Summary:     Summary{Method: "sampled"},
			Series:      emptySeries(window),
		}, nil
	}

	var res *Result
	if window.UseRollup {
		res, err = r.getRollupAnalytics(ctx, tenantID, monitorIDs, window, now.UTC())
	} else {
		res, err = r.getRawAnalytics(ctx, tenantID, monitorIDs, window, now.UTC())
	}
	if err != nil {
		return nil, err
	}
	if err := r.applyIntervalAvailability(ctx, tenantID, monitorIDs, window.Start, now.UTC(), res); err != nil {
		return nil, err
	}
	return res, nil
}

// applyIntervalAvailability fills the time-based headline (S-U1). When the
// timeline does not cover the window, the sampled rate stands in, labeled
// method="sampled" (S-U5).
func (r *Repository) applyIntervalAvailability(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, start, end time.Time, res *Result) error {
	res.Summary.Method = "sampled"
	res.Summary.AvailabilityPct = res.Summary.SLAPct
	ia, covered, err := r.computeIntervalAvailability(ctx, tenantID, monitorIDs, start, end)
	if err != nil {
		return err
	}
	if !covered {
		return nil
	}
	res.Summary.Method = "interval"
	res.Summary.AvailabilityPct = ia.AvailabilityPct
	coverage := ia.CoveragePct
	res.Summary.CoveragePct = &coverage
	// The timeline is authoritative once it covers the window: an entirely
	// unknown window renders as no-data even when history-only sample rows
	// exist (S-D1).
	res.Summary.HasData = ia.HasData
	return nil
}

// ScopeAnalyticsBatchRequest identifies one analytics scope to compute within a batch.
// Key is an opaque identifier (e.g. a monitor or group id) used to key the returned map;
// MonitorIDs is the set of leaf monitors that make up the scope.
type ScopeAnalyticsBatchRequest struct {
	Key        uuid.UUID
	MonitorIDs []uuid.UUID
}

// GetScopeAnalyticsBatch computes analytics for many scopes across several ranges while
// amortizing database access: rollup rows and downtime are loaded once for the union of
// all monitors over the widest window, then every (scope, range) result is assembled in
// memory using the exact same builders as GetScopeAnalytics. This is the batched, N+1-free
// counterpart of GetScopeAnalytics for rollup-backed (long) ranges. Any non-rollup range is
// computed per scope to remain correct; callers that only request long ranges issue a
// constant number of queries regardless of how many scopes are involved.
func (r *Repository) GetScopeAnalyticsBatch(ctx context.Context, tenantID uuid.UUID, scopes []ScopeAnalyticsBatchRequest, ranges []Range, now time.Time) (map[uuid.UUID]map[Range]*Result, error) {
	now = now.UTC()
	out := make(map[uuid.UUID]map[Range]*Result, len(scopes))
	for _, scope := range scopes {
		if _, ok := out[scope.Key]; !ok {
			out[scope.Key] = make(map[Range]*Result, len(ranges))
		}
	}

	rollupRanges := make([]Range, 0, len(ranges))
	otherRanges := make([]Range, 0)
	for _, rng := range ranges {
		window, err := ResolveWindow(rng, now)
		if err != nil {
			return nil, err
		}
		if window.UseRollup {
			rollupRanges = append(rollupRanges, rng)
		} else {
			otherRanges = append(otherRanges, rng)
		}
	}

	if len(rollupRanges) > 0 {
		if err := r.fillRollupBatch(ctx, tenantID, scopes, rollupRanges, now, out); err != nil {
			return nil, err
		}
	}

	// Non-rollup ranges are not expected in batch usage (the status page only batches long
	// ranges). Compute them per scope so the method stays correct if ever called that way.
	for _, scope := range scopes {
		for _, rng := range otherRanges {
			res, err := r.GetScopeAnalytics(ctx, tenantID, scope.MonitorIDs, rng, now)
			if err != nil {
				return nil, err
			}
			out[scope.Key][rng] = res
		}
	}

	return out, nil
}

func (r *Repository) fillRollupBatch(ctx context.Context, tenantID uuid.UUID, scopes []ScopeAnalyticsBatchRequest, rollupRanges []Range, now time.Time, out map[uuid.UUID]map[Range]*Result) error {
	union := make([]uuid.UUID, 0)
	for _, scope := range scopes {
		union = append(union, scope.MonitorIDs...)
	}
	union = dedupeUUIDs(union)

	windows := make(map[Range]Window, len(rollupRanges))
	var widest Window
	for i, rng := range rollupRanges {
		window, err := ResolveWindow(rng, now)
		if err != nil {
			return err
		}
		windows[rng] = window
		if i == 0 || window.Start.Before(widest.Start) {
			widest = window
		}
	}

	if len(union) == 0 {
		for _, scope := range scopes {
			for _, rng := range rollupRanges {
				window := windows[rng]
				out[scope.Key][rng] = &Result{
					Range:       rng,
					GeneratedAt: now,
					Source:      SourceRollup,
					IsPartial:   true,
					Series:      emptySeries(window),
				}
			}
		}
		return nil
	}

	rollupsByMonitor, err := r.loadRollupRowsByMonitor(ctx, tenantID, union, widest.Start, now)
	if err != nil {
		return err
	}
	periodsByMonitor, openByMonitor, err := r.loadDowntimeByMonitor(ctx, tenantID, union, widest.Start, now)
	if err != nil {
		return err
	}

	for _, scope := range scopes {
		for _, rng := range rollupRanges {
			window := windows[rng]
			rows := make([]rollupRow, 0)
			for _, monitorID := range scope.MonitorIDs {
				for _, row := range rollupsByMonitor[monitorID] {
					if row.BucketDay.Before(window.Start) || row.BucketDay.After(window.End) {
						continue
					}
					rows = append(rows, row)
				}
			}
			res := buildRollupResult(window, now, rows)
			res.Range = rng
			res.Source = SourceRollup
			res.Downtime = buildScopeDowntime(scope.MonitorIDs, periodsByMonitor, openByMonitor, window.Start, now)
			out[scope.Key][rng] = res
		}
	}

	return nil
}

func (r *Repository) loadRollupRowsByMonitor(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, start, end time.Time) (map[uuid.UUID][]rollupRow, error) {
	query := `
		SELECT monitor_id, bucket_day, total_checks, success_checks,
		       latency_success_sum_ms, latency_success_count, latest_status, latest_check_at
		FROM monitor_daily_rollups
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND bucket_day >= $3::date
		  AND bucket_day <= $4::date
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch rollup rows: %w", err)
	}
	defer rows.Close()

	byMonitor := make(map[uuid.UUID][]rollupRow)
	for rows.Next() {
		var row rollupRow
		if err := rows.Scan(
			&row.MonitorID,
			&row.BucketDay,
			&row.TotalChecks,
			&row.SuccessChecks,
			&row.LatencySuccessSumMS,
			&row.LatencySuccessCount,
			&row.LatestStatus,
			&row.LatestCheckAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan batch rollup row: %w", err)
		}
		row.BucketDay = row.BucketDay.UTC()
		byMonitor[row.MonitorID] = append(byMonitor[row.MonitorID], row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch rollup rows: %w", err)
	}
	return byMonitor, nil
}

func (r *Repository) loadDowntimeByMonitor(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, start, end time.Time) (map[uuid.UUID][]DowntimePeriod, map[uuid.UUID][]time.Time, error) {
	periodsByMonitor := make(map[uuid.UUID][]DowntimePeriod)
	periodQuery := `
		SELECT monitor_id, start_time, end_time
		FROM monitor_downtime_periods
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND end_time >= $3
		  AND start_time <= $4
		ORDER BY start_time ASC
	`
	rows, err := r.db.QueryContext(ctx, periodQuery, tenantID, pq.Array(monitorIDs), start, end)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query batch downtime periods: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var monitorID uuid.UUID
		var period DowntimePeriod
		if err := rows.Scan(&monitorID, &period.Start, &period.End); err != nil {
			return nil, nil, fmt.Errorf("failed to scan batch downtime period: %w", err)
		}
		period.Start = period.Start.UTC()
		period.End = period.End.UTC()
		periodsByMonitor[monitorID] = append(periodsByMonitor[monitorID], period)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating batch downtime periods: %w", err)
	}

	openByMonitor := make(map[uuid.UUID][]time.Time)
	openQuery := `
		SELECT monitor_id, started_at
		FROM monitor_downtime_open
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND started_at <= $3
	`
	openRows, err := r.db.QueryContext(ctx, openQuery, tenantID, pq.Array(monitorIDs), end)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query batch open downtime periods: %w", err)
	}
	defer openRows.Close()
	for openRows.Next() {
		var monitorID uuid.UUID
		var startedAt time.Time
		if err := openRows.Scan(&monitorID, &startedAt); err != nil {
			return nil, nil, fmt.Errorf("failed to scan batch open downtime period: %w", err)
		}
		openByMonitor[monitorID] = append(openByMonitor[monitorID], startedAt.UTC())
	}
	if err := openRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating batch open downtime periods: %w", err)
	}

	return periodsByMonitor, openByMonitor, nil
}

// buildScopeDowntime reproduces loadRollupDowntime for a single scope/window from
// pre-loaded, monitor-keyed downtime data.
func buildScopeDowntime(monitorIDs []uuid.UUID, periodsByMonitor map[uuid.UUID][]DowntimePeriod, openByMonitor map[uuid.UUID][]time.Time, start, end time.Time) []DowntimePeriod {
	periods := make([]DowntimePeriod, 0)
	for _, monitorID := range monitorIDs {
		for _, period := range periodsByMonitor[monitorID] {
			if period.End.Before(start) || period.Start.After(end) {
				continue
			}
			periods = append(periods, DowntimePeriod{
				Start: clampTime(period.Start, start, end),
				End:   clampTime(period.End, start, end),
			})
		}
		for _, startedAt := range openByMonitor[monitorID] {
			if startedAt.After(end) {
				continue
			}
			periods = append(periods, DowntimePeriod{
				Start:  clampTime(startedAt, start, end),
				End:    end,
				IsOpen: true,
			})
		}
	}
	return mergeDowntimePeriods(periods)
}

func sourceForRange(rangeValue Range) Source {
	if rangeValue.IsLongRange() {
		return SourceRollup
	}
	return SourceRaw
}

func emptySeries(window Window) []SeriesPoint {
	buckets := BucketStarts(window)
	series := make([]SeriesPoint, 0, len(buckets))
	for _, bucket := range buckets {
		series = append(series, SeriesPoint{BucketStart: bucket})
	}
	return series
}

func (r *Repository) getRawAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, window Window, generatedAt time.Time) (*Result, error) {
	query := `
		SELECT monitor_id, status, latency_ms, created_at
		FROM check_results
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND result_source <> 'platform'
		  AND created_at >= $3
		  AND created_at <= $4
		ORDER BY created_at ASC, id ASC
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), window.Start, window.End)
	if err != nil {
		return nil, fmt.Errorf("failed to query raw analytics rows: %w", err)
	}
	defer rows.Close()

	var rawRows []rawRow
	for rows.Next() {
		var row rawRow
		if err := rows.Scan(&row.MonitorID, &row.Status, &row.LatencyMS, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan raw analytics row: %w", err)
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating raw analytics rows: %w", err)
	}

	result := buildRawResult(window, generatedAt, rawRows)
	result.Range = window.Range
	result.Source = SourceRaw
	return result, nil
}

func buildRawResult(window Window, generatedAt time.Time, rows []rawRow) *Result {
	res := &Result{
		Range:       window.Range,
		GeneratedAt: generatedAt,
		Source:      SourceRaw,
		IsPartial:   true,
	}
	if len(rows) == 0 {
		res.Series = emptySeries(window)
		return res
	}

	coverageStart := rows[0].CreatedAt.UTC()
	res.CoverageStart = &coverageStart
	res.IsPartial = coverageStart.After(window.Start)

	perMonitor := make(map[uuid.UUID]*uptimeAccumulator)
	perBucket := make(map[time.Time]map[uuid.UUID]*uptimeAccumulator)
	monitorDowntimeRows := make(map[uuid.UUID][]rawRow)
	allLatencies := make([]float64, 0)

	for _, row := range rows {
		bucket := bucketForRaw(window, row.CreatedAt.UTC())
		acc := ensureRawAcc(perMonitor, row.MonitorID)
		consumeRawRow(acc, row, &allLatencies)
		if _, ok := perBucket[bucket]; !ok {
			perBucket[bucket] = make(map[uuid.UUID]*uptimeAccumulator)
		}
		bucketAcc := ensureRawAcc(perBucket[bucket], row.MonitorID)
		consumeRawRow(bucketAcc, row, nil)
		monitorDowntimeRows[row.MonitorID] = append(monitorDowntimeRows[row.MonitorID], row)
	}

	latestStatuses := make([]string, 0, len(perMonitor))
	var latestCheckAt *time.Time
	perMonitorUptimes := make([]float64, 0, len(perMonitor))
	perMonitorLatencies := make([]float64, 0, len(perMonitor))
	for _, acc := range perMonitor {
		if acc.totalChecks > 0 {
			perMonitorUptimes = append(perMonitorUptimes, (float64(acc.successChecks)/float64(acc.totalChecks))*100)
		}
		if len(acc.latencies) > 0 {
			perMonitorLatencies = append(perMonitorLatencies, average(acc.latencies))
		}
		if acc.latestStatus != nil {
			latestStatuses = append(latestStatuses, *acc.latestStatus)
		}
		if acc.latestCheckAt != nil && (latestCheckAt == nil || acc.latestCheckAt.After(*latestCheckAt)) {
			latestCheckAt = cloneTimePtr(acc.latestCheckAt)
		}
	}

	uptime := average(perMonitorUptimes)
	res.Summary.HasData = len(perMonitorUptimes) > 0
	res.Summary.UptimePct = uptime
	res.Summary.SLAPct = uptime
	res.Summary.DowntimePct = maxFloat(0, 100-uptime)
	if len(perMonitorLatencies) > 0 {
		res.Summary.AvgLatencyMS = PtrFloat64(average(perMonitorLatencies))
	}
	sort.Float64s(allLatencies)
	res.Summary.MedianLatencyMS = percentile(allLatencies, 0.5)
	res.Summary.P95LatencyMS = percentile(allLatencies, 0.95)
	res.Summary.LatestStatus = aggregateStatus(latestStatuses)
	res.Summary.LatestCheckAt = latestCheckAt
	res.Series = buildRawSeries(window, perBucket)
	res.Downtime = mergeDowntimePeriods(buildRawDowntimePeriods(monitorDowntimeRows, window.End))
	return res
}

func bucketForRaw(window Window, ts time.Time) time.Time {
	start := window.Start
	delta := ts.Sub(start)
	if delta < 0 {
		return start
	}
	bucket := (delta / window.Bucket) * window.Bucket
	return start.Add(bucket)
}

func ensureRawAcc(target map[uuid.UUID]*uptimeAccumulator, monitorID uuid.UUID) *uptimeAccumulator {
	acc, ok := target[monitorID]
	if !ok {
		acc = &uptimeAccumulator{}
		target[monitorID] = acc
	}
	return acc
}

func consumeRawRow(acc *uptimeAccumulator, row rawRow, allLatencies *[]float64) {
	acc.totalChecks++
	if row.Status == "success" {
		acc.successChecks++
		if row.LatencyMS.Valid {
			latency := float64(row.LatencyMS.Int64)
			acc.latencies = append(acc.latencies, latency)
			if allLatencies != nil {
				*allLatencies = append(*allLatencies, latency)
			}
		}
	}
	status := row.Status
	acc.latestStatus = &status
	ts := row.CreatedAt.UTC()
	acc.latestCheckAt = &ts
}

func buildRawSeries(window Window, perBucket map[time.Time]map[uuid.UUID]*uptimeAccumulator) []SeriesPoint {
	bucketStarts := BucketStarts(window)
	series := make([]SeriesPoint, 0, len(bucketStarts))
	for _, bucket := range bucketStarts {
		monitorAccs := perBucket[bucket]
		point := SeriesPoint{BucketStart: bucket}
		if len(monitorAccs) == 0 {
			series = append(series, point)
			continue
		}
		point.HasData = true
		uptimes := make([]float64, 0, len(monitorAccs))
		latencies := make([]float64, 0, len(monitorAccs))
		for _, acc := range monitorAccs {
			if acc.totalChecks > 0 {
				uptimes = append(uptimes, (float64(acc.successChecks)/float64(acc.totalChecks))*100)
				point.TotalChecks += acc.totalChecks
			}
			if len(acc.latencies) > 0 {
				latencies = append(latencies, average(acc.latencies))
			}
		}
		point.UptimePct = average(uptimes)
		if len(latencies) > 0 {
			point.AvgLatencyMS = PtrFloat64(average(latencies))
		}
		series = append(series, point)
	}
	return series
}

func buildRawDowntimePeriods(perMonitor map[uuid.UUID][]rawRow, end time.Time) []DowntimePeriod {
	periods := make([]DowntimePeriod, 0)
	for _, rows := range perMonitor {
		var openStart *time.Time
		for _, row := range rows {
			ts := row.CreatedAt.UTC()
			if row.Status != "success" {
				if openStart == nil {
					openStart = &ts
				}
				continue
			}
			if openStart != nil {
				periods = append(periods, DowntimePeriod{Start: *openStart, End: ts})
				openStart = nil
			}
		}
		if openStart != nil {
			periods = append(periods, DowntimePeriod{Start: *openStart, End: end, IsOpen: true})
		}
	}
	return periods
}

func (r *Repository) getRollupAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, window Window, generatedAt time.Time) (*Result, error) {
	query := `
		SELECT monitor_id, bucket_day, total_checks, success_checks,
		       latency_success_sum_ms, latency_success_count, latest_status, latest_check_at
		FROM monitor_daily_rollups
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND bucket_day >= $3::date
		  AND bucket_day <= $4::date
		ORDER BY bucket_day ASC, monitor_id ASC
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), window.Start, window.End)
	if err != nil {
		return nil, fmt.Errorf("failed to query rollup rows: %w", err)
	}
	defer rows.Close()

	rollups := make([]rollupRow, 0)
	for rows.Next() {
		var row rollupRow
		if err := rows.Scan(
			&row.MonitorID,
			&row.BucketDay,
			&row.TotalChecks,
			&row.SuccessChecks,
			&row.LatencySuccessSumMS,
			&row.LatencySuccessCount,
			&row.LatestStatus,
			&row.LatestCheckAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan rollup row: %w", err)
		}
		row.BucketDay = row.BucketDay.UTC()
		rollups = append(rollups, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rollup rows: %w", err)
	}

	downtime, err := r.loadRollupDowntime(ctx, tenantID, monitorIDs, window.Start, generatedAt)
	if err != nil {
		return nil, err
	}

	res := buildRollupResult(window, generatedAt, rollups)
	res.Range = window.Range
	res.Source = SourceRollup
	res.Downtime = downtime
	return res, nil
}

func buildRollupResult(window Window, generatedAt time.Time, rows []rollupRow) *Result {
	res := &Result{
		Range:       window.Range,
		GeneratedAt: generatedAt,
		Source:      SourceRollup,
		IsPartial:   true,
		Series:      emptySeries(window),
	}
	if len(rows) == 0 {
		return res
	}

	dayBuckets := make(map[time.Time]map[uuid.UUID]dayAccumulator)
	perMonitor := make(map[uuid.UUID][]dayAccumulator)
	var coverageStart *time.Time
	var latestCheckAt *time.Time
	var latestStatus *string

	for _, row := range rows {
		day := time.Date(row.BucketDay.Year(), row.BucketDay.Month(), row.BucketDay.Day(), 0, 0, 0, 0, time.UTC)
		if coverageStart == nil || day.Before(*coverageStart) {
			coverageStart = &day
		}
		acc := dayAccumulator{
			totalChecks:         row.TotalChecks,
			successChecks:       row.SuccessChecks,
			latencySuccessSumMS: row.LatencySuccessSumMS,
			latencySuccessCount: row.LatencySuccessCount,
		}
		if row.LatestStatus.Valid {
			status := row.LatestStatus.String
			acc.latestStatus = &status
		}
		if row.LatestCheckAt.Valid {
			ts := row.LatestCheckAt.Time.UTC()
			acc.latestCheckAt = &ts
			if latestCheckAt == nil || ts.After(*latestCheckAt) {
				latestCheckAt = cloneTimePtr(&ts)
				latestStatus = cloneStringPtr(acc.latestStatus)
			}
		}
		if _, ok := dayBuckets[day]; !ok {
			dayBuckets[day] = make(map[uuid.UUID]dayAccumulator)
		}
		dayBuckets[day][row.MonitorID] = acc
		if row.TotalChecks > 0 {
			perMonitor[row.MonitorID] = append(perMonitor[row.MonitorID], acc)
		}
	}

	if coverageStart != nil {
		res.CoverageStart = coverageStart
		res.IsPartial = coverageStart.After(window.Start)
	}

	bucketStarts := BucketStarts(window)
	series := make([]SeriesPoint, 0, len(bucketStarts))
	for _, bucket := range bucketStarts {
		point := SeriesPoint{BucketStart: bucket}
		monitorStats := dayBuckets[bucket]
		if len(monitorStats) == 0 {
			series = append(series, point)
			continue
		}
		uptimes := make([]float64, 0, len(monitorStats))
		latencies := make([]float64, 0, len(monitorStats))
		for _, stat := range monitorStats {
			if stat.totalChecks > 0 {
				uptimes = append(uptimes, (float64(stat.successChecks)/float64(stat.totalChecks))*100)
				point.TotalChecks += stat.totalChecks
			}
			if avg := stat.avgLatencyMS(); avg != nil {
				latencies = append(latencies, *avg)
			}
		}
		// A rollup row can exist with zero checks; a bucket only has data if
		// something was actually checked, else it must render as no-data,
		// not as a 0% day (S-D1).
		point.HasData = point.TotalChecks > 0
		point.UptimePct = average(uptimes)
		if len(latencies) > 0 {
			point.AvgLatencyMS = PtrFloat64(average(latencies))
		}
		series = append(series, point)
	}
	res.Series = series

	// Summary semantics (D2): each monitor's SLA/latency is computed over the
	// WHOLE window — sum(success_checks)/sum(total_checks) across all of the
	// monitor's daily rollup rows — so every check weighs equally regardless of
	// which day it landed on. This matches the 24h dashboard math
	// (computeMonitorWeightedUptime in api/internal/services/dashboard). The
	// per-day series above intentionally keeps day-scoped rates: averaging
	// per-day rates here would let a 1-check day weigh as much as a 1000-check
	// day in the headline number.
	perMonitorSLA := make([]float64, 0, len(perMonitor))
	perMonitorLatency := make([]float64, 0, len(perMonitor))
	for _, stats := range perMonitor {
		var totalChecks, successChecks, latencyCount int
		var latencySumMS float64
		for _, stat := range stats {
			totalChecks += stat.totalChecks
			successChecks += stat.successChecks
			latencySumMS += stat.latencySuccessSumMS
			latencyCount += stat.latencySuccessCount
		}
		if totalChecks > 0 {
			perMonitorSLA = append(perMonitorSLA, (float64(successChecks)/float64(totalChecks))*100)
		}
		if latencyCount > 0 {
			perMonitorLatency = append(perMonitorLatency, latencySumMS/float64(latencyCount))
		}
	}
	sla := average(perMonitorSLA)
	res.Summary.HasData = len(perMonitorSLA) > 0
	res.Summary.UptimePct = sla
	res.Summary.SLAPct = sla
	res.Summary.DowntimePct = maxFloat(0, 100-sla)
	if len(perMonitorLatency) > 0 {
		res.Summary.AvgLatencyMS = PtrFloat64(average(perMonitorLatency))
	}
	res.Summary.LatestStatus = latestStatus
	res.Summary.LatestCheckAt = latestCheckAt
	return res
}

func (r *Repository) loadRollupDowntime(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, start, end time.Time) ([]DowntimePeriod, error) {
	periodQuery := `
		SELECT start_time, end_time
		FROM monitor_downtime_periods
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND end_time >= $3
		  AND start_time <= $4
		ORDER BY start_time ASC
	`
	rows, err := r.db.QueryContext(ctx, periodQuery, tenantID, pq.Array(monitorIDs), start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query downtime periods: %w", err)
	}
	defer rows.Close()

	periods := make([]DowntimePeriod, 0)
	for rows.Next() {
		var period DowntimePeriod
		if err := rows.Scan(&period.Start, &period.End); err != nil {
			return nil, fmt.Errorf("failed to scan downtime period: %w", err)
		}
		period.Start = clampTime(period.Start.UTC(), start, end)
		period.End = clampTime(period.End.UTC(), start, end)
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating downtime periods: %w", err)
	}

	openQuery := `
		SELECT started_at
		FROM monitor_downtime_open
		WHERE tenant_id = $1
		  AND monitor_id = ANY($2)
		  AND started_at <= $3
	`
	openRows, err := r.db.QueryContext(ctx, openQuery, tenantID, pq.Array(monitorIDs), end)
	if err != nil {
		return nil, fmt.Errorf("failed to query open downtime periods: %w", err)
	}
	defer openRows.Close()
	for openRows.Next() {
		var startedAt time.Time
		if err := openRows.Scan(&startedAt); err != nil {
			return nil, fmt.Errorf("failed to scan open downtime period: %w", err)
		}
		periods = append(periods, DowntimePeriod{
			Start:  clampTime(startedAt.UTC(), start, end),
			End:    end,
			IsOpen: true,
		})
	}
	if err := openRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating open downtime periods: %w", err)
	}

	return mergeDowntimePeriods(periods), nil
}

func clampTime(value, start, end time.Time) time.Time {
	if value.Before(start) {
		return start
	}
	if value.After(end) {
		return end
	}
	return value
}

func mergeDowntimePeriods(periods []DowntimePeriod) []DowntimePeriod {
	if len(periods) == 0 {
		return nil
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].Start.Equal(periods[j].Start) {
			return periods[i].End.Before(periods[j].End)
		}
		return periods[i].Start.Before(periods[j].Start)
	})
	merged := []DowntimePeriod{periods[0]}
	for _, period := range periods[1:] {
		last := &merged[len(merged)-1]
		if !period.Start.After(last.End) {
			if period.End.After(last.End) {
				last.End = period.End
			}
			last.IsOpen = last.IsOpen || period.IsOpen
			continue
		}
		merged = append(merged, period)
	}
	return merged
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
