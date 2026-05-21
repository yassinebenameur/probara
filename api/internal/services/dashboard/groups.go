package dashboard

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

const membersPreviewLimit = 10

// groupAggregationRow is one monitor's pre-aggregated stats joined with its tag set.
type groupAggregationRow struct {
	MonitorID      uuid.UUID
	Name           string
	Uptime         float64
	Tags           []string
	CurrentStatus  *string
	AttentionCount int // 1 if monitor needs attention (failure/error in range OR current_status in {failure,error}), else 0
}

// aggregateGroups builds per-tag groups from the per-monitor rows.
//
//   - A monitor with N matching group tags appears in N groups (Venn-style).
//   - Monitors with no matching tags are placed in a synthetic ungrouped row (tag == nil).
//   - The ungrouped row is included only if at least one monitor falls into it.
//   - Stale group tags (no matching monitors) are still rendered with monitor_count=0.
//   - If groupTags is empty, returns nil (the empty-state CTA case).
func aggregateGroups(rows []groupAggregationRow, groupTags []string) []models.DashboardGroup {
	if len(groupTags) == 0 {
		return nil
	}

	buckets := make(map[string]*models.DashboardGroup, len(groupTags))
	order := make([]string, 0, len(groupTags))
	for _, tag := range groupTags {
		t := tag
		if _, exists := buckets[t]; exists {
			continue
		}
		buckets[t] = &models.DashboardGroup{Tag: &t}
		order = append(order, t)
	}
	sort.Strings(order)

	var ungrouped *models.DashboardGroup
	sums := make(map[string]float64, len(groupTags))
	var ungroupedSum float64

	for _, row := range rows {
		matched := false
		for _, tag := range row.Tags {
			if g, ok := buckets[tag]; ok {
				matched = true
				g.MonitorCount++
				sums[tag] += row.Uptime
				g.AttentionCount += row.AttentionCount
				g.Members = appendGroupMember(g.Members, row)
			}
		}
		if !matched {
			if ungrouped == nil {
				ungrouped = &models.DashboardGroup{Tag: nil}
			}
			ungrouped.MonitorCount++
			ungroupedSum += row.Uptime
			ungrouped.AttentionCount += row.AttentionCount
			ungrouped.Members = appendGroupMember(ungrouped.Members, row)
		}
	}

	finalize := func(g *models.DashboardGroup, totalUptime float64) {
		if g.MonitorCount > 0 {
			g.Uptime = totalUptime / float64(g.MonitorCount)
		}
		sort.SliceStable(g.Members, func(i, j int) bool {
			return g.Members[i].Uptime < g.Members[j].Uptime
		})
		if len(g.Members) > membersPreviewLimit {
			g.Members = g.Members[:membersPreviewLimit]
		}
		if g.AttentionCount > 0 && len(g.Members) > 0 {
			worst := g.Members[0]
			g.WorstMember = &worst
		}
		if g.Members == nil {
			g.Members = []models.DashboardGroupMember{}
		}
	}

	out := make([]models.DashboardGroup, 0, len(order)+1)
	for _, tag := range order {
		g := buckets[tag]
		finalize(g, sums[tag])
		out = append(out, *g)
	}
	if ungrouped != nil {
		finalize(ungrouped, ungroupedSum)
		out = append(out, *ungrouped)
	}
	return out
}

func appendGroupMember(members []models.DashboardGroupMember, row groupAggregationRow) []models.DashboardGroupMember {
	return append(members, models.DashboardGroupMember{
		MonitorID:     row.MonitorID,
		MonitorName:   row.Name,
		Uptime:        row.Uptime,
		CurrentStatus: row.CurrentStatus,
	})
}

// queryMonitorsForGroups returns one row per enabled, non-group monitor with its uptime over
// the given range and tag set. The top-level dashboard tag filter (filterTags) restricts the
// monitor set, but the per-monitor `m.tags` field is always returned so the caller can bucket
// into groups (which may differ from the filter set).
func (s *Service) queryMonitorsForGroups(
	ctx context.Context,
	tenantID uuid.UUID,
	rangeStart, rangeEndExclusive time.Time,
	filterTags []string,
) ([]groupAggregationRow, error) {
	tagClause := ""
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive}
	if len(filterTags) > 0 {
		tagClause = "AND m.tags @> $4::text[]"
		args = append(args, pq.Array(filterTags))
	}

	query := fmt.Sprintf(`
		WITH per_monitor AS (
			SELECT
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
				COUNT(*) FILTER (WHERE cr.status IN ('failure', 'error')) AS bad_checks
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT
			m.id,
			m.name,
			COALESCE(m.tags, '{}'::text[]) AS tags,
			cs.current_status,
			COALESCE(
				CASE WHEN pm.total_checks > 0
					THEN (pm.success_checks::float / pm.total_checks::float) * 100.0
					ELSE 100.0
				END,
				100.0
			) AS uptime,
			COALESCE(pm.bad_checks, 0) > 0 OR COALESCE(cs.current_status IN ('failure','error'), FALSE) AS needs_attention
		FROM monitors m
		LEFT JOIN per_monitor pm ON pm.monitor_id = m.id
		LEFT JOIN LATERAL (
			SELECT cr.status AS current_status
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.tenant_id = $1
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			ORDER BY cr.created_at DESC
			LIMIT 1
		) cs ON TRUE
		WHERE m.tenant_id = $1
		  AND m.enabled = TRUE
		  AND m.type <> 'group'
		  %s
	`, tagClause, tagClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitors for groups: %w", err)
	}
	defer rows.Close()

	out := []groupAggregationRow{}
	for rows.Next() {
		var r groupAggregationRow
		var needsAttention bool
		if err := rows.Scan(
			&r.MonitorID,
			&r.Name,
			pq.Array(&r.Tags),
			&r.CurrentStatus,
			&r.Uptime,
			&needsAttention,
		); err != nil {
			return nil, fmt.Errorf("failed to scan group row: %w", err)
		}
		if needsAttention {
			r.AttentionCount = 1
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate group rows: %w", err)
	}
	return out, nil
}

// loadGroups runs the per-monitor aggregation query and shapes it into DashboardGroup rows.
func (s *Service) loadGroups(
	ctx context.Context,
	tenantID uuid.UUID,
	rangeStart, rangeEndExclusive time.Time,
	filterTags []string,
	groupTags []string,
) ([]models.DashboardGroup, error) {
	if len(groupTags) == 0 {
		return []models.DashboardGroup{}, nil
	}

	rows, err := s.queryMonitorsForGroups(ctx, tenantID, rangeStart, rangeEndExclusive, filterTags)
	if err != nil {
		return nil, err
	}
	out := aggregateGroups(rows, groupTags)
	if out == nil {
		return []models.DashboardGroup{}, nil
	}
	return out, nil
}

const sparklineBucketCount = 12

// GetGroupSparkline returns a 12-bucket uptime series for a single group over the range.
// params.Tag == nil means the ungrouped sentinel (monitors with no curated group tag).
func (s *Service) GetGroupSparkline(
	ctx context.Context,
	tenantID uuid.UUID,
	params *models.DashboardGroupSparklineQuery,
) (*models.DashboardGroupSparklineResponse, error) {
	if params == nil {
		return nil, fmt.Errorf("missing params")
	}

	rng := normalizeDashboardRange(params.Range)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(rng)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	monitorIDs, err := s.monitorIDsForGroup(ctx, tenantID, params.Tag, params.Tags)
	if err != nil {
		return nil, err
	}

	buckets := make([]float64, 0, sparklineBucketCount)
	if len(monitorIDs) == 0 {
		return &models.DashboardGroupSparklineResponse{
			Tag:     params.Tag,
			Range:   rng,
			Buckets: buckets,
		}, nil
	}

	series, err := s.groupUptimeSeries(ctx, tenantID, monitorIDs, rng, rangeStart, rangeEnd, rangeEndExclusive)
	if err != nil {
		return nil, err
	}

	// Down-/up-sample to 12 buckets so the frontend always renders consistently.
	buckets = resampleTo(series, sparklineBucketCount)

	return &models.DashboardGroupSparklineResponse{
		Tag:     params.Tag,
		Range:   rng,
		Buckets: buckets,
	}, nil
}

// monitorIDsForGroup returns the IDs of enabled non-group monitors that:
//   - have `tag` in m.tags (if tag != nil), OR
//   - have none of the tenant's curated dashboard_group_tags (if tag == nil — ungrouped),
//   - AND match the top-level filterTags filter (m.tags @> filterTags).
func (s *Service) monitorIDsForGroup(
	ctx context.Context,
	tenantID uuid.UUID,
	tag *string,
	filterTags []string,
) ([]uuid.UUID, error) {
	var query string
	args := []interface{}{tenantID}

	if tag != nil {
		// monitors that have THIS tag, plus optional filter
		query = `
            SELECT id FROM monitors
            WHERE tenant_id = $1
              AND enabled = TRUE
              AND type <> 'group'
              AND $2 = ANY(tags)
        `
		args = append(args, *tag)
		if len(filterTags) > 0 {
			query += ` AND tags @> $3::text[]`
			args = append(args, pq.Array(filterTags))
		}
	} else {
		// ungrouped: monitors with none of the curated group tags
		settings, err := s.tenants.GetTenantSettings(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to load tenant settings for ungrouped sparkline: %w", err)
		}
		curated := settings.DashboardGroupTags
		if curated == nil {
			curated = []string{}
		}
		query = `
            SELECT id FROM monitors
            WHERE tenant_id = $1
              AND enabled = TRUE
              AND type <> 'group'
              AND NOT (tags && $2::text[])
        `
		args = append(args, pq.Array(curated))
		if len(filterTags) > 0 {
			query += ` AND tags @> $3::text[]`
			args = append(args, pq.Array(filterTags))
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve group monitors: %w", err)
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan monitor id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate monitor ids: %w", err)
	}
	return out, nil
}

// groupUptimeSeries returns a per-bucket uptime % series for the given monitor set,
// using the analytics rollup for long ranges and live SQL for 24h.
func (s *Service) groupUptimeSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	rng models.DashboardRange,
	rangeStart, rangeEnd, rangeEndExclusive time.Time,
) ([]float64, error) {
	if rng != models.DashboardRange24h {
		analyticsResult, err := s.analytics.GetScopeAnalytics(
			ctx,
			tenantID,
			monitorIDs,
			sharedanalytics.Range(rng),
			time.Now().UTC(),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query rollup-backed sparkline: %w", err)
		}
		out := make([]float64, 0, len(analyticsResult.Series))
		for _, p := range analyticsResult.Series {
			out = append(out, p.UptimePct)
		}
		return out, nil
	}

	// 24h: hourly buckets via generate_series, mirroring getTrend's live path.
	query := `
        WITH buckets AS (
            SELECT generate_series($2::timestamptz, $3::timestamptz, INTERVAL '1 hour') AS bucket_start
        ),
        per_bucket AS (
            SELECT
                date_trunc('hour', cr.created_at) AS bucket_start,
                COUNT(*) AS total_checks,
                COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks
            FROM check_results cr
            WHERE cr.tenant_id = $1
              AND cr.result_source <> 'platform'
              AND cr.monitor_id = ANY($4::uuid[])
              AND cr.created_at >= $2
              AND cr.created_at < $5
            GROUP BY 1
        )
        SELECT
            COALESCE(
                CASE WHEN pb.total_checks > 0
                    THEN (pb.success_checks::float / pb.total_checks::float) * 100.0
                    ELSE 100.0
                END,
                100.0
            ) AS uptime
        FROM buckets b
        LEFT JOIN per_bucket pb ON pb.bucket_start = b.bucket_start
        ORDER BY b.bucket_start
    `

	rows, err := s.db.QueryContext(ctx, query, tenantID, rangeStart, rangeEnd, pq.Array(monitorIDs), rangeEndExclusive)
	if err != nil {
		return nil, fmt.Errorf("failed to query 24h sparkline: %w", err)
	}
	defer rows.Close()

	out := []float64{}
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("failed to scan bucket: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate buckets: %w", err)
	}
	return out, nil
}

// resampleTo down-/up-samples a series to exactly n buckets by linear index mapping.
// For empty input, returns an empty slice.
func resampleTo(in []float64, n int) []float64 {
	if len(in) == 0 || n <= 0 {
		return []float64{}
	}
	if len(in) == n {
		out := make([]float64, n)
		copy(out, in)
		return out
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		// Take the bucket at the proportional position in the source.
		srcIdx := i * len(in) / n
		if srcIdx >= len(in) {
			srcIdx = len(in) - 1
		}
		out[i] = in[srcIdx]
	}
	return out
}
