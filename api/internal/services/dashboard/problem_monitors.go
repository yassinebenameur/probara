// Problem monitors: the per-range variants, their candidate list and failure stats.

package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

type problemMonitorCandidate struct {
	MonitorID     uuid.UUID
	MonitorName   string
	CurrentState  string
	TotalChecks   int
	SuccessChecks int
	ProblemChecks int
	LatestCheckAt *time.Time
}

type problemMonitorFailureStats struct {
	FailureCount    int
	ErrorCount      int
	LatestFailureAt *time.Time
}

func (s *Service) getProblemMonitors(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	if limit < 1 {
		limit = problemMonitorLimit
	}

	var monitors []models.DashboardProblemMonitor
	var err error
	switch {
	case isDashboardRollupRange(dashboardRange):
		monitors, err = s.getProblemMonitorsLongRange(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	case dashboardRange == models.DashboardRange24h:
		monitors, err = s.getProblemMonitors24h(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	default:
		monitors, err = s.getProblemMonitors1h(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	}
	if err != nil {
		return nil, err
	}

	// Rollups carry no error messages, so the latest failure reason is fetched
	// from raw check_results in one batched probe over the <= limit winners,
	// keeping the three range variants untouched.
	if err := s.attachLatestProblemErrors(ctx, tenantID, rangeStart, rangeEndExclusive, monitors); err != nil {
		return nil, err
	}
	return monitors, nil
}

// attachLatestProblemErrors populates LatestErrorMessage on each problem
// monitor from its most recent failing check inside the range. Monitors whose
// failures only exist in rollups (raw rows already purged) keep a nil message.
func (s *Service) attachLatestProblemErrors(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, monitors []models.DashboardProblemMonitor) error {
	if len(monitors) == 0 {
		return nil
	}
	monitorIDs := make([]uuid.UUID, 0, len(monitors))
	for _, m := range monitors {
		monitorIDs = append(monitorIDs, m.MonitorID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (cr.monitor_id) cr.monitor_id, cr.error_message
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.monitor_id = ANY($2)
		  AND cr.status IN ('failure', 'error')
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $3
		  AND cr.created_at < $4
		ORDER BY cr.monitor_id, cr.created_at DESC
	`, tenantID, pq.Array(monitorIDs), rangeStart, rangeEndExclusive)
	if err != nil {
		return fmt.Errorf("failed to query latest problem errors: %w", err)
	}
	defer rows.Close()

	latest := make(map[uuid.UUID]*string, len(monitors))
	for rows.Next() {
		var monitorID uuid.UUID
		var message sql.NullString
		if err := rows.Scan(&monitorID, &message); err != nil {
			return fmt.Errorf("failed to scan latest problem error: %w", err)
		}
		if message.Valid && message.String != "" {
			v := message.String
			latest[monitorID] = &v
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating latest problem errors: %w", err)
	}

	for i := range monitors {
		monitors[i].LatestErrorMessage = latest[monitors[i].MonitorID]
	}
	return nil
}

// Problem monitors are ordered by current state first (currentStateRank) on
// every range, BEFORE the limit cuts the list: the dashboard re-sorts Needs
// attention by state, and ordering by failure count alone dropped a monitor
// that is down now but failed few checks in the range (it just went down, or
// runs on a long interval) once a larger fleet filled the limit.
func (s *Service) getProblemMonitors1h(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	tagClause := ""
	limitPlaceholder := 4
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags && $5::text[]"
		args = []interface{}{tenantID, rangeStart, rangeEndExclusive, limit, pq.Array(tags)}
	}

	query := fmt.Sprintf(`
		WITH problem_stats AS (
			SELECT
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
				COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
				COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
				MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND m.deleted_at IS NULL
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT
			m.id,
			m.name,
			m.current_state,
			ps.failure_count,
			ps.error_count,
			CASE
				WHEN ps.total_checks > 0 THEN (ps.success_checks::float / ps.total_checks::float) * 100.0
				ELSE 0
			END AS uptime,
			ps.latest_failure_at
		FROM problem_stats ps
		JOIN monitors m ON m.id = ps.monitor_id AND m.tenant_id = $1 AND m.deleted_at IS NULL
		WHERE (ps.failure_count + ps.error_count) > 0
		ORDER BY %s, (ps.failure_count + ps.error_count) DESC, ps.latest_failure_at DESC NULLS LAST, m.name ASC
		LIMIT $%d
	`, tagClause, currentStateRankSQL("m.current_state"), limitPlaceholder)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query 1h problem monitors: %w", err)
	}
	defer rows.Close()

	return scanProblemMonitorRows(rows)
}

func (s *Service) getProblemMonitors24h(ctx context.Context, tenantID uuid.UUID, _rangeStart, _rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	// The monitor-set predicate (enabled, type<>'group', deleted_at IS NULL,
	// tags &&) and the totals computation are exactly the ones cached by
	// loadRolling24hData, so the parallel summary request's work is reused.
	data, monitorIDs, err := s.loadRolling24hData(ctx, tenantID, tags)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}
	totals := data.totals

	// Materialise candidates (any monitor with at least one bad check).
	type candidate struct {
		monitorID     uuid.UUID
		failureCount  int
		errorCount    int
		uptime        float64
		latestFailure *time.Time
	}
	cands := make([]candidate, 0, len(monitorIDs))
	for _, id := range monitorIDs {
		t := totals[id]
		bad := t.FailureChecks + t.ErrorChecks
		if bad == 0 {
			continue
		}
		uptime := 0.0
		if t.TotalChecks > 0 {
			uptime = (float64(t.SuccessChecks) / float64(t.TotalChecks)) * 100.0
		}
		var latest *time.Time
		if t.LatestCheckAt != nil && t.LatestStatus != nil && (*t.LatestStatus == "failure" || *t.LatestStatus == "error") {
			ts := *t.LatestCheckAt
			latest = &ts
		}
		cands = append(cands, candidate{
			monitorID:     id,
			failureCount:  t.FailureChecks,
			errorCount:    t.ErrorChecks,
			uptime:        uptime,
			latestFailure: latest,
		})
	}
	if len(cands) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}

	// Names + current_state for every candidate, before the cut, so the
	// current state can lead the order.
	candidateIDs := make([]uuid.UUID, 0, len(cands))
	for _, c := range cands {
		candidateIDs = append(candidateIDs, c.monitorID)
	}
	nameRows, err := s.db.QueryContext(ctx, `
		SELECT id, name, current_state
		FROM monitors
		WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, tenantID, pq.Array(candidateIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load problem-monitor names: %w", err)
	}
	defer nameRows.Close()
	type monitorMeta struct {
		name         string
		currentState string
	}
	meta := make(map[uuid.UUID]monitorMeta, len(candidateIDs))
	for nameRows.Next() {
		var id uuid.UUID
		var m monitorMeta
		if err := nameRows.Scan(&id, &m.name, &m.currentState); err != nil {
			return nil, fmt.Errorf("failed to scan problem-monitor name: %w", err)
		}
		meta[id] = m
	}
	if err := nameRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem-monitor names: %w", err)
	}

	sort.Slice(cands, func(i, j int) bool {
		ri := currentStateRank(meta[cands[i].monitorID].currentState)
		rj := currentStateRank(meta[cands[j].monitorID].currentState)
		if ri != rj {
			return ri < rj
		}
		li := cands[i].failureCount + cands[i].errorCount
		lj := cands[j].failureCount + cands[j].errorCount
		if li != lj {
			return li > lj
		}
		if cands[i].latestFailure == nil && cands[j].latestFailure != nil {
			return false
		}
		if cands[i].latestFailure != nil && cands[j].latestFailure == nil {
			return true
		}
		if cands[i].latestFailure != nil && cands[j].latestFailure != nil && !cands[i].latestFailure.Equal(*cands[j].latestFailure) {
			return cands[i].latestFailure.After(*cands[j].latestFailure)
		}
		return cands[i].monitorID.String() < cands[j].monitorID.String()
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}

	out := make([]models.DashboardProblemMonitor, 0, len(cands))
	for _, c := range cands {
		m := meta[c.monitorID]
		out = append(out, models.DashboardProblemMonitor{
			MonitorID:       c.monitorID,
			MonitorName:     m.name,
			CurrentState:    m.currentState,
			CurrentStatus:   monitorStateToStatus(m.currentState),
			FailureCount:    c.failureCount,
			ErrorCount:      c.errorCount,
			Uptime:          c.uptime,
			LatestFailureAt: c.latestFailure,
		})
	}
	return out, nil
}

func (s *Service) getProblemMonitorsLongRange(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	candidateLimit := limit * problemCandidateMult
	if candidateLimit < limit {
		candidateLimit = limit
	}

	candidates, err := s.listProblemMonitorCandidates(ctx, tenantID, rangeStart, rangeEndExclusive, candidateLimit, tags)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}

	monitorIDs := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		monitorIDs = append(monitorIDs, candidate.MonitorID)
	}

	rawStats, err := s.getProblemMonitorFailureStats(ctx, tenantID, rangeStart, rangeEndExclusive, monitorIDs)
	if err != nil {
		return nil, err
	}

	monitors := make([]models.DashboardProblemMonitor, 0, len(candidates))
	for _, candidate := range candidates {
		stats := rawStats[candidate.MonitorID]
		failureCount := stats.FailureCount
		errorCount := stats.ErrorCount
		latestFailureAt := stats.LatestFailureAt

		if failureCount+errorCount == 0 && candidate.ProblemChecks > 0 {
			failureCount = candidate.ProblemChecks
			latestFailureAt = candidate.LatestCheckAt
		}
		if failureCount+errorCount == 0 {
			continue
		}

		uptime := 0.0
		if candidate.TotalChecks > 0 {
			uptime = (float64(candidate.SuccessChecks) / float64(candidate.TotalChecks)) * 100.0
		}

		monitors = append(monitors, models.DashboardProblemMonitor{
			MonitorID:       candidate.MonitorID,
			MonitorName:     candidate.MonitorName,
			CurrentState:    candidate.CurrentState,
			CurrentStatus:   monitorStateToStatus(candidate.CurrentState),
			FailureCount:    failureCount,
			ErrorCount:      errorCount,
			Uptime:          uptime,
			LatestFailureAt: latestFailureAt,
		})
	}

	sort.Slice(monitors, func(i, j int) bool {
		if ri, rj := currentStateRank(monitors[i].CurrentState), currentStateRank(monitors[j].CurrentState); ri != rj {
			return ri < rj
		}
		leftCount := monitors[i].FailureCount + monitors[i].ErrorCount
		rightCount := monitors[j].FailureCount + monitors[j].ErrorCount
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		if monitors[i].LatestFailureAt == nil && monitors[j].LatestFailureAt != nil {
			return false
		}
		if monitors[i].LatestFailureAt != nil && monitors[j].LatestFailureAt == nil {
			return true
		}
		if monitors[i].LatestFailureAt != nil && monitors[j].LatestFailureAt != nil && !monitors[i].LatestFailureAt.Equal(*monitors[j].LatestFailureAt) {
			return monitors[i].LatestFailureAt.After(*monitors[j].LatestFailureAt)
		}
		return monitors[i].MonitorName < monitors[j].MonitorName
	})

	if len(monitors) > limit {
		monitors = monitors[:limit]
	}
	return monitors, nil
}

func (s *Service) listProblemMonitorCandidates(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]problemMonitorCandidate, error) {
	tagClause := ""
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags && $5::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		WITH rollup_candidates AS (
			SELECT
				m.id,
				m.name,
				m.current_state,
				COALESCE(SUM(mdr.total_checks), 0) AS total_checks,
				COALESCE(SUM(mdr.success_checks), 0) AS success_checks,
				COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) AS problem_checks,
				latest.latest_check_at
			FROM monitors m
			LEFT JOIN monitor_daily_rollups mdr
				ON mdr.monitor_id = m.id
				AND mdr.tenant_id = m.tenant_id
				AND mdr.bucket_day >= $2::date
				AND mdr.bucket_day < $3::date
			LEFT JOIN LATERAL (
				SELECT mdr_latest.latest_check_at
				FROM monitor_daily_rollups mdr_latest
				WHERE mdr_latest.monitor_id = m.id
				  AND mdr_latest.tenant_id = m.tenant_id
				  AND mdr_latest.bucket_day >= $2::date
				  AND mdr_latest.bucket_day < $3::date
				ORDER BY mdr_latest.bucket_day DESC
				LIMIT 1
			) latest ON TRUE
			WHERE m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND m.deleted_at IS NULL
			  %[1]s
			GROUP BY m.id, m.name, m.current_state, latest.latest_check_at
			HAVING COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) > 0
			    OR m.current_state IN ('down', 'degraded')
			ORDER BY %[2]s, problem_checks DESC, name ASC
			LIMIT $4
		)
		SELECT id, name, current_state, total_checks, success_checks, problem_checks, latest_check_at
		FROM rollup_candidates
		ORDER BY %[3]s, problem_checks DESC, name ASC
	`, tagClause, currentStateRankSQL("m.current_state"), currentStateRankSQL("current_state"))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query problem monitor rollup candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]problemMonitorCandidate, 0)
	for rows.Next() {
		var candidate problemMonitorCandidate
		var latestCheckAt sql.NullTime

		if err := rows.Scan(
			&candidate.MonitorID,
			&candidate.MonitorName,
			&candidate.CurrentState,
			&candidate.TotalChecks,
			&candidate.SuccessChecks,
			&candidate.ProblemChecks,
			&latestCheckAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor rollup candidate: %w", err)
		}
		if latestCheckAt.Valid {
			ts := latestCheckAt.Time
			candidate.LatestCheckAt = &ts
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor rollup candidates: %w", err)
	}

	return candidates, nil
}

func (s *Service) getProblemMonitorFailureStats(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, monitorIDs []uuid.UUID) (map[uuid.UUID]problemMonitorFailureStats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			cr.monitor_id,
			COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
			COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
			MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  AND cr.monitor_id = ANY($4)
		GROUP BY cr.monitor_id
	`, tenantID, rangeStart, rangeEndExclusive, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query problem monitor raw stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[uuid.UUID]problemMonitorFailureStats, len(monitorIDs))
	for rows.Next() {
		var monitorID uuid.UUID
		var row problemMonitorFailureStats
		var latestFailureAt sql.NullTime

		if err := rows.Scan(&monitorID, &row.FailureCount, &row.ErrorCount, &latestFailureAt); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor raw stats: %w", err)
		}
		if latestFailureAt.Valid {
			ts := latestFailureAt.Time
			row.LatestFailureAt = &ts
		}
		stats[monitorID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor raw stats: %w", err)
	}

	return stats, nil
}

func scanProblemMonitorRows(rows *sql.Rows) ([]models.DashboardProblemMonitor, error) {
	monitors := make([]models.DashboardProblemMonitor, 0)
	for rows.Next() {
		var row models.DashboardProblemMonitor
		var latestFailureAt sql.NullTime

		if err := rows.Scan(
			&row.MonitorID,
			&row.MonitorName,
			&row.CurrentState,
			&row.FailureCount,
			&row.ErrorCount,
			&row.Uptime,
			&latestFailureAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor row: %w", err)
		}

		row.CurrentStatus = monitorStateToStatus(row.CurrentState)
		if latestFailureAt.Valid {
			ts := latestFailureAt.Time
			row.LatestFailureAt = &ts
		}
		monitors = append(monitors, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor rows: %w", err)
	}

	return monitors, nil
}
