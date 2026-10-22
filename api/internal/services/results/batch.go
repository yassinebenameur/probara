package results

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// Batch results: the monitors list ranks every row by its last few results,
// so it reads them for many monitors in one request instead of one
// GET /monitors/{id}/results per monitor (plus one /members per group).
//
// MaxBatchMonitorIDs keeps the GET URL (comma-separated ids, ~37 bytes each)
// well under the 8 KB request-line limit common proxies enforce; callers
// chunk larger lists.
const (
	MaxBatchMonitorIDs       = 100
	DefaultBatchResultsLimit = 20
	MaxBatchResultsLimit     = 100
)

// ErrInvalidBatchRequest marks a batch request the caller must fix (no ids,
// too many ids, limit out of range); handlers map it to 400.
var ErrInvalidBatchRequest = errors.New("invalid batch results request")

// MonitorResultsBatchResponse is GET /monitors/results/batch. Results holds,
// per requested monitor, the rows GET /monitors/{id}/results?limit=N returns
// for it; Members holds each requested group's direct member ids (the
// monitor_groups junction table, by member name). Ids that do not name a live
// monitor of the tenant are absent from both maps.
type MonitorResultsBatchResponse struct {
	Limit   int                                `json:"limit"`
	Results map[uuid.UUID][]models.CheckResult `json:"results"`
	Members map[uuid.UUID][]uuid.UUID          `json:"members"`
}

// GetMonitorResultsBatch returns the newest `limit` results for each of
// monitorIDs (deduplicated), matching GetMonitorResults without `since`: a
// regular monitor gets its own rows (with metrics_data and location), a group
// gets its leaf members' rows merged newest first, topped by the derived
// group-status row. Like that endpoint it is a newest-N read, not a time
// window, so it carries no retention bound of its own: raw rows older than
// monitorstate.CheckResultsRawRetentionDays are pruned by cleanup, and a
// bound here would only make the list and the detail page disagree until
// that prune runs. Everything stays inside the tenant.
func (s *Service) GetMonitorResultsBatch(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, limit int) (*MonitorResultsBatchResponse, error) {
	ids := dedupeIDs(monitorIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: at least one monitor id is required", ErrInvalidBatchRequest)
	}
	if len(ids) > MaxBatchMonitorIDs {
		return nil, fmt.Errorf("%w: at most %d monitor ids per request", ErrInvalidBatchRequest, MaxBatchMonitorIDs)
	}
	if limit == 0 {
		limit = DefaultBatchResultsLimit
	}
	if limit < 1 || limit > MaxBatchResultsLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidBatchRequest, MaxBatchResultsLimit)
	}

	response := &MonitorResultsBatchResponse{
		Limit:   limit,
		Results: map[uuid.UUID][]models.CheckResult{},
		Members: map[uuid.UUID][]uuid.UUID{},
	}

	// Scope: which ids are this tenant's live monitors, and which are groups.
	var regular, groups []uuid.UUID
	if err := s.queryRows(ctx, func(rows *sql.Rows) error {
		var id uuid.UUID
		var monitorType string
		if err := rows.Scan(&id, &monitorType); err != nil {
			return err
		}
		if models.MonitorType(monitorType) == models.MonitorTypeGroup {
			groups = append(groups, id)
		} else {
			regular = append(regular, id)
		}
		return nil
	}, `
		SELECT id, type FROM monitors
		WHERE id = ANY($1::uuid[]) AND tenant_id = $2 AND deleted_at IS NULL
	`, pq.Array(ids), tenantID); err != nil {
		return nil, fmt.Errorf("failed to resolve monitors: %w", err)
	}

	// Groups: direct members (for the list's tree) and leaf members (whose
	// results a group's row is built from).
	leavesByGroup := map[uuid.UUID][]uuid.UUID{}
	if len(groups) > 0 {
		for _, groupID := range groups {
			response.Members[groupID] = []uuid.UUID{}
		}
		if err := s.queryRows(ctx, func(rows *sql.Rows) error {
			var groupID, memberID uuid.UUID
			if err := rows.Scan(&groupID, &memberID); err != nil {
				return err
			}
			response.Members[groupID] = append(response.Members[groupID], memberID)
			return nil
		}, `
			SELECT mg.group_id, mg.monitor_id
			FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id AND m.tenant_id = $2 AND m.deleted_at IS NULL
			WHERE mg.group_id = ANY($1::uuid[])
			ORDER BY mg.group_id, m.name, m.id
		`, pq.Array(groups), tenantID); err != nil {
			return nil, fmt.Errorf("failed to get group members: %w", err)
		}

		// The same tree walk as groups.GetGroupLeafMembers, rooted at every
		// requested group at once. UNION (not ALL) stops nesting cycles.
		if err := s.queryRows(ctx, func(rows *sql.Rows) error {
			var groupID, leafID uuid.UUID
			if err := rows.Scan(&groupID, &leafID); err != nil {
				return err
			}
			leavesByGroup[groupID] = append(leavesByGroup[groupID], leafID)
			return nil
		}, `
			WITH RECURSIVE member_tree AS (
				SELECT mg.group_id AS root_id, mg.monitor_id
				FROM monitor_groups mg
				JOIN monitors m ON m.id = mg.monitor_id
				WHERE mg.group_id = ANY($1::uuid[]) AND m.tenant_id = $2 AND m.deleted_at IS NULL
				UNION
				SELECT mt.root_id, mg.monitor_id
				FROM member_tree mt
				JOIN monitors parent ON parent.id = mt.monitor_id AND parent.tenant_id = $2
				JOIN monitor_groups mg ON mg.group_id = parent.id
				JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = $2
				WHERE parent.type = 'group'
				  AND parent.deleted_at IS NULL
				  AND child.deleted_at IS NULL
			)
			SELECT mt.root_id, m.id
			FROM member_tree mt
			JOIN monitors m ON m.id = mt.monitor_id
			WHERE m.tenant_id = $2 AND m.type <> 'group' AND m.deleted_at IS NULL
		`, pq.Array(groups), tenantID); err != nil {
			return nil, fmt.Errorf("failed to get group leaf members: %w", err)
		}
	}

	// One indexed top-N per monitor (idx_check_results_monitor_id_created_at):
	// a group's newest N rows are always among its leaves' newest N each.
	// Requested monitors get the full row; group leaves only the light
	// columns a group's rows carry (getGroupResults' shape), so a group never
	// detoasts its leaves' metrics_data. A leaf that is also requested reuses
	// its full rows. Leaves are read once however many groups share them.
	rowsByMonitor := map[uuid.UUID][]models.CheckResult{}
	if len(regular) > 0 {
		if err := s.queryRows(ctx, func(rows *sql.Rows) error {
			var monitorID uuid.UUID
			result, err := scanBatchResult(rows, &monitorID)
			if err != nil {
				return err
			}
			rowsByMonitor[monitorID] = append(rowsByMonitor[monitorID], result)
			return nil
		}, `
			SELECT req.monitor_id, cr.id, cr.status, cr.result_source, cr.http_status, cr.latency_ms,
				cr.error_message, cr.created_at, COALESCE(cr.metrics_data::text, ''), cr.location_id, l.name
			FROM unnest($1::uuid[]) AS req(monitor_id)
			CROSS JOIN LATERAL (
				SELECT c.id, c.status, c.result_source, c.http_status, c.latency_ms, c.error_message,
					c.created_at, c.metrics_data, c.location_id
				FROM check_results c
				WHERE c.monitor_id = req.monitor_id AND c.tenant_id = $2
				ORDER BY c.created_at DESC
				LIMIT $3
			) cr
			LEFT JOIN locations l ON l.id = cr.location_id
			ORDER BY req.monitor_id, cr.created_at DESC
		`, pq.Array(regular), tenantID, limit); err != nil {
			return nil, fmt.Errorf("failed to query check results: %w", err)
		}
	}

	requested := make(map[uuid.UUID]struct{}, len(regular))
	for _, id := range regular {
		requested[id] = struct{}{}
	}
	var leafIDs, leafOnlyIDs []uuid.UUID
	seenLeaf := map[uuid.UUID]struct{}{}
	for _, leaves := range leavesByGroup {
		for _, leafID := range leaves {
			if _, ok := seenLeaf[leafID]; ok {
				continue
			}
			seenLeaf[leafID] = struct{}{}
			leafIDs = append(leafIDs, leafID)
			if _, ok := requested[leafID]; !ok {
				leafOnlyIDs = append(leafOnlyIDs, leafID)
			}
		}
	}
	if len(leafOnlyIDs) > 0 {
		if err := s.queryRows(ctx, func(rows *sql.Rows) error {
			var monitorID uuid.UUID
			result, err := scanLightBatchResult(rows, &monitorID)
			if err != nil {
				return err
			}
			rowsByMonitor[monitorID] = append(rowsByMonitor[monitorID], result)
			return nil
		}, `
			SELECT req.monitor_id, cr.id, cr.status, cr.result_source, cr.http_status, cr.latency_ms,
				cr.error_message, cr.created_at
			FROM unnest($1::uuid[]) AS req(monitor_id)
			CROSS JOIN LATERAL (
				SELECT c.id, c.status, c.result_source, c.http_status, c.latency_ms, c.error_message, c.created_at
				FROM check_results c
				WHERE c.monitor_id = req.monitor_id AND c.tenant_id = $2
				ORDER BY c.created_at DESC
				LIMIT $3
			) cr
		`, pq.Array(leafOnlyIDs), tenantID, limit); err != nil {
			return nil, fmt.Errorf("failed to query group member results: %w", err)
		}
	}

	// A group's status row rolls up each leaf's latest non-platform result,
	// as groups.GetGroupStatus does (idx_check_results_monitor_non_platform_created_at).
	latestByLeaf := map[uuid.UUID]string{}
	if len(leafIDs) > 0 {
		if err := s.queryRows(ctx, func(rows *sql.Rows) error {
			var monitorID uuid.UUID
			var status string
			if err := rows.Scan(&monitorID, &status); err != nil {
				return err
			}
			latestByLeaf[monitorID] = status
			return nil
		}, `
			SELECT req.monitor_id, latest.status
			FROM unnest($1::uuid[]) AS req(monitor_id)
			CROSS JOIN LATERAL (
				SELECT c.status FROM check_results c
				WHERE c.monitor_id = req.monitor_id AND c.tenant_id = $2 AND c.result_source <> 'platform'
				ORDER BY c.created_at DESC
				LIMIT 1
			) latest
		`, pq.Array(leafIDs), tenantID); err != nil {
			return nil, fmt.Errorf("failed to query group member status: %w", err)
		}
	}

	for _, id := range regular {
		results := rowsByMonitor[id]
		if results == nil {
			results = []models.CheckResult{}
		}
		response.Results[id] = results
	}
	now := time.Now()
	for _, groupID := range groups {
		leaves := leavesByGroup[groupID]
		statuses := make([]string, 0, len(leaves))
		for _, leafID := range leaves {
			if status, ok := latestByLeaf[leafID]; ok {
				statuses = append(statuses, status)
			}
		}
		response.Results[groupID] = groupBatchResults(leaves, rowsByMonitor, limit, rollUpLeafStatuses(len(leaves), statuses), now)
	}
	return response, nil
}

// groupBatchResults merges the leaves' rows newest first, keeps `limit`, and
// prepends the derived status row, the shape getGroupResults returns: no
// metrics_data or location on a group's rows.
func groupBatchResults(leaves []uuid.UUID, rowsByMonitor map[uuid.UUID][]models.CheckResult, limit int, groupStatus string, now time.Time) []models.CheckResult {
	merged := []models.CheckResult{}
	for _, leafID := range leaves {
		for _, row := range rowsByMonitor[leafID] {
			row.MetricsData = nil
			row.LocationID = nil
			row.LocationName = nil
			merged = append(merged, row)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if !merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].CreatedAt.After(merged[j].CreatedAt)
		}
		return merged[i].ID.String() < merged[j].ID.String()
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	if groupStatus != "unknown" && len(merged) > 0 {
		merged = append([]models.CheckResult{{
			ID:           uuid.New(),
			Status:       groupStatus,
			ResultSource: string(sharedmodels.ResultSourceDerived),
			CreatedAt:    now,
		}}, merged...)
	}
	return merged
}

// rollUpLeafStatuses is groups.GetGroupStatus's rule over already-fetched
// latest statuses: no leaves or none checked → unknown, all success →
// success, all failure/error → failure, otherwise degraded.
func rollUpLeafStatuses(leafCount int, statuses []string) string {
	if leafCount == 0 {
		return "unknown"
	}
	var success, failed int
	for _, status := range statuses {
		switch status {
		case "success":
			success++
		case "failure", "error":
			failed++
		}
	}
	switch total := success + failed; {
	case total == 0:
		return "unknown"
	case success == total:
		return "success"
	case failed == total:
		return "failure"
	default:
		return "degraded"
	}
}

func scanLightBatchResult(rows *sql.Rows, monitorID *uuid.UUID) (models.CheckResult, error) {
	var result models.CheckResult
	var httpStatus, latencyMS sql.NullInt64
	var errorMessage sql.NullString
	if err := rows.Scan(monitorID, &result.ID, &result.Status, &result.ResultSource, &httpStatus, &latencyMS,
		&errorMessage, &result.CreatedAt); err != nil {
		return result, err
	}
	setNullableResultFields(&result, httpStatus, latencyMS, errorMessage)
	return result, nil
}

func setNullableResultFields(result *models.CheckResult, httpStatus, latencyMS sql.NullInt64, errorMessage sql.NullString) {
	if httpStatus.Valid {
		v := int(httpStatus.Int64)
		result.HTTPStatus = &v
	}
	if latencyMS.Valid {
		v := int(latencyMS.Int64)
		result.LatencyMS = &v
	}
	if errorMessage.Valid {
		result.ErrorMessage = &errorMessage.String
	}
}

func scanBatchResult(rows *sql.Rows, monitorID *uuid.UUID) (models.CheckResult, error) {
	var result models.CheckResult
	var httpStatus, latencyMS sql.NullInt64
	var errorMessage, locationName sql.NullString
	var metricsData string
	if err := rows.Scan(monitorID, &result.ID, &result.Status, &result.ResultSource, &httpStatus, &latencyMS,
		&errorMessage, &result.CreatedAt, &metricsData, &result.LocationID, &locationName); err != nil {
		return result, err
	}
	setNullableResultFields(&result, httpStatus, latencyMS, errorMessage)
	if metricsData != "" {
		result.MetricsData = json.RawMessage(metricsData)
	}
	if locationName.Valid {
		result.LocationName = &locationName.String
	}
	return result, nil
}

func (s *Service) queryRows(ctx context.Context, scan func(*sql.Rows) error, query string, args ...interface{}) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func dedupeIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
