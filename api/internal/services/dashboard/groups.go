package dashboard

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
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
			COALESCE(pm.bad_checks, 0) > 0 OR cs.current_status IN ('failure','error') AS needs_attention
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
