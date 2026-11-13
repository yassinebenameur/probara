// Tenant-ownership checks for the rows an incident references, plus row scanners and UUID-set helpers.

package incidents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/apperr"
	"github.com/yassinebenameur/probara/api/internal/models"
)

type incidentRowScanner interface {
	Scan(dest ...interface{}) error
}

func (s *Service) ensureIncidentBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, incidentID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, incidentID, tenantID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncidentNotFound
		}
		return fmt.Errorf("load incident: %w", err)
	}

	return nil
}

func (s *Service) ensureAlertBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM alerts
		WHERE id = $1 AND tenant_id = $2
	`, alertID, tenantID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.ErrAlertNotFound
		}
		return fmt.Errorf("load alert: %w", err)
	}

	return nil
}

func (s *Service) ensureMonitorBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, monitorID, tenantID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.ErrMonitorNotFound
		}
		return fmt.Errorf("load monitor: %w", err)
	}

	return nil
}

func (s *Service) ensureStatusPageBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, statusPageID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM status_pages
		WHERE id = $1 AND tenant_id = $2
	`, statusPageID, tenantID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.ErrStatusPageNotFound
		}
		return fmt.Errorf("load status page: %w", err)
	}

	return nil
}

func loadUUIDSet(ctx context.Context, tx *sql.Tx, query string, args ...interface{}) (map[uuid.UUID]struct{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

func uuidStrings(ids []uuid.UUID) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id.String())
	}
	return values
}

func sameUUIDSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}

	leftSet := make(map[uuid.UUID]struct{}, len(left))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	for _, id := range right {
		if _, ok := leftSet[id]; !ok {
			return false
		}
	}

	return true
}

func scanIncidentListItem(scanner incidentRowScanner) (models.IncidentListItem, error) {
	var item models.IncidentListItem
	var severity string
	var ownerUserID uuid.NullUUID
	var ownerUsername sql.NullString
	var resolvedAt sql.NullTime

	if err := scanner.Scan(
		&item.ID, &item.State, &item.Source, &item.Title, &severity, &ownerUserID, &ownerUsername, &item.UpdatedAt, &resolvedAt,
		&item.LinkedAlertCount, &item.LinkedMonitorCount, &item.PublicationCount,
	); err != nil {
		return models.IncidentListItem{}, err
	}
	item.Severity = models.IncidentSeverity(severity)
	if ownerUserID.Valid {
		ownerID := ownerUserID.UUID
		item.OwnerUserID = &ownerID
	}
	if ownerUsername.Valid {
		item.OwnerUsername = ownerUsername.String
	}
	if resolvedAt.Valid {
		resolvedAtValue := resolvedAt.Time
		item.ResolvedAt = &resolvedAtValue
	}

	return item, nil
}
