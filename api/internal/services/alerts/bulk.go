package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// MaxBulkAlerts caps one bulk acknowledge/resolve request.
const MaxBulkAlerts = 500

// Reasons a bulk action left an alert unchanged (BulkFailure.Reason).
const (
	// BulkReasonNotFound: no such alert in this workspace (or its monitor
	// was deleted, which hides the alert from every read).
	BulkReasonNotFound = "not_found"
	// BulkReasonAlreadyAcknowledged: acknowledge on an acknowledged alert.
	BulkReasonAlreadyAcknowledged = "already_acknowledged"
	// BulkReasonAlreadyResolved: the alert had already closed.
	BulkReasonAlreadyResolved = "already_resolved"
)

// BulkFailure is one requested alert a bulk action did not change.
type BulkFailure struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason"`
}

// BulkResult is a bulk action's outcome, per alert: the batch never fails as
// a whole because some alerts changed state meanwhile.
type BulkResult struct {
	// Items are the alerts this request changed, in request order, in their
	// new state.
	Items []models.AlertWithDetails `json:"items"`
	// Failed are the requested alerts left unchanged, in request order.
	Failed []BulkFailure `json:"failed"`
}

// bulkTargetCTE locks the requested alerts that are still in one of the
// given states, in id order (a deterministic lock order, so two overlapping
// bulk requests cannot deadlock). The status test is re-evaluated after any
// wait for a row lock, so an alert the alerter resolved meanwhile drops out
// instead of being overwritten. Alerts of soft-deleted monitors are hidden
// from every read and are left alone here too.
const bulkTargetCTE = `
	WITH target AS (
		SELECT al.id
		FROM alerts al
		LEFT JOIN monitors m ON m.id = al.monitor_id
		WHERE al.id = ANY($1::uuid[]) AND al.tenant_id = $2 AND al.status::text = ANY($3::text[])
		  AND (al.monitor_id IS NULL OR m.deleted_at IS NULL)
		ORDER BY al.id
		FOR UPDATE OF al
	)`

// BulkAcknowledge acknowledges every listed alert that is still active. The
// others are reported in Failed (not found, already acknowledged or already
// resolved); they never fail the request.
func (s *Service) BulkAcknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error) {
	ids := dedupeIDs(alertIDs)
	now := time.Now()
	changed, err := collectIDs(s.db.QueryContext(ctx, bulkTargetCTE+`
		UPDATE alerts a
		SET status = 'acknowledged', acknowledged_at = $4, updated_at = $4
		FROM target
		WHERE a.id = target.id
		RETURNING a.id
	`, pq.Array(uuidStrings(ids)), tenantID, pq.Array([]string{"active"}), now))
	if err != nil {
		return nil, fmt.Errorf("failed to bulk acknowledge alerts: %w", err)
	}
	return s.bulkResult(ctx, tenantID, ids, changed)
}

// BulkResolve resolves every listed alert that is still open (active or
// acknowledged), recording incident recovery for each in the same
// transaction as ResolveAlert does. The others are reported in Failed.
func (s *Service) BulkResolve(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID) (*BulkResult, error) {
	ids := dedupeIDs(alertIDs)
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	changed, err := collectIDs(tx.QueryContext(ctx, bulkTargetCTE+`
		UPDATE alerts a
		SET status = 'resolved', resolved_at = $4, updated_at = $4
		FROM target
		WHERE a.id = target.id
		RETURNING a.id
	`, pq.Array(uuidStrings(ids)), tenantID, pq.Array([]string{"active", "acknowledged"}), now))
	if err != nil {
		return nil, fmt.Errorf("failed to bulk resolve alerts: %w", err)
	}

	if s.incidents != nil {
		for _, id := range changed {
			if err := s.incidents.RecordAlertRecoveryIfNeededTx(ctx, tx, tenantID, id); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit alert transaction: %w", err)
	}

	return s.bulkResult(ctx, tenantID, ids, changed)
}

// bulkResult reads the changed alerts back in full and explains every
// requested alert that did not change, both in request order.
func (s *Service) bulkResult(ctx context.Context, tenantID uuid.UUID, requested, changed []uuid.UUID) (*BulkResult, error) {
	result := &BulkResult{Items: []models.AlertWithDetails{}, Failed: []BulkFailure{}}

	details := make(map[uuid.UUID]models.AlertWithDetails, len(changed))
	if len(changed) > 0 {
		rows, err := s.db.QueryContext(ctx, alertDetailSelect+`
			WHERE a.id = ANY($1::uuid[]) AND a.tenant_id = $2 AND `+alertVisibleClause,
			pq.Array(uuidStrings(changed)), tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to read changed alerts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			alert, err := scanAlertWithDetails(rows.Scan)
			if err != nil {
				return nil, fmt.Errorf("failed to scan alert: %w", err)
			}
			details[alert.ID] = *alert
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating alerts: %w", err)
		}
	}

	changedSet := make(map[uuid.UUID]bool, len(changed))
	for _, id := range changed {
		changedSet[id] = true
	}
	var unchanged []uuid.UUID
	for _, id := range requested {
		if !changedSet[id] {
			unchanged = append(unchanged, id)
		}
	}
	statuses := map[uuid.UUID]models.AlertStatus{}
	if len(unchanged) > 0 {
		rows, err := s.db.QueryContext(ctx, `
			SELECT a.id, a.status
			FROM alerts a
			LEFT JOIN monitors m ON m.id = a.monitor_id
			WHERE a.id = ANY($1::uuid[]) AND a.tenant_id = $2 AND `+alertVisibleClause,
			pq.Array(uuidStrings(unchanged)), tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to read unchanged alerts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var status models.AlertStatus
			if err := rows.Scan(&id, &status); err != nil {
				return nil, fmt.Errorf("failed to scan alert status: %w", err)
			}
			statuses[id] = status
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating alert statuses: %w", err)
		}
	}

	for _, id := range requested {
		if changedSet[id] {
			// Changed but no longer readable (its monitor was deleted in
			// between): it did change, there is just no row to show.
			if alert, ok := details[id]; ok {
				result.Items = append(result.Items, alert)
			}
			continue
		}
		reason := BulkReasonNotFound
		switch statuses[id] {
		case models.AlertStatusAcknowledged:
			reason = BulkReasonAlreadyAcknowledged
		case models.AlertStatusResolved:
			reason = BulkReasonAlreadyResolved
		}
		result.Failed = append(result.Failed, BulkFailure{ID: id, Reason: reason})
	}
	return result, nil
}

func collectIDs(rows *sql.Rows, err error) ([]uuid.UUID, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func dedupeIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
