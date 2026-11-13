// Incident timeline: operator entries, automatic alert-recovery entries and the timeline read.

package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/incidentrecovery"
)

// CreateIncidentTimelineEntry appends a timeline entry to an incident.
func (s *Service) CreateIncidentTimelineEntry(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
	if err := validation.ValidateCreateIncidentTimelineEntry(req); err != nil {
		return nil, err
	}

	if _, err := s.GetIncident(ctx, tenantID, incidentID); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, req.EntryType, strings.TrimSpace(req.Message), req.Metadata); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	if req.EntryType == models.IncidentTimelineEntryTypePublicUpdate {
		s.publishIncidentStatusPageRefreshes(ctx, tenantID, incidentID)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// RecordAlertRecoveryIfNeeded appends a timeline entry when all alerts linked to an incident are resolved.
func (s *Service) RecordAlertRecoveryIfNeeded(ctx context.Context, tenantID, alertID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.RecordAlertRecoveryIfNeededTx(ctx, tx, tenantID, alertID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident transaction: %w", err)
	}

	return nil
}

// RecordAlertRecoveryIfNeededTx appends a timeline entry when all linked alerts are resolved inside an existing transaction.
func (s *Service) RecordAlertRecoveryIfNeededTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	return incidentrecovery.RecordForTenantAlertTx(ctx, tx, tenantID, alertID)
}

func (s *Service) insertTimelineEntryTx(ctx context.Context, exec incidentExecutor, tenantID, incidentID uuid.UUID, entryType models.IncidentTimelineEntryType, message string, metadata map[string]interface{}) error {
	return incidentrecovery.InsertTimelineEntryTx(ctx, exec, tenantID, incidentID, string(entryType), message, metadata)
}

func (s *Service) loadIncidentTimeline(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentTimelineEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, incident_id, entry_type, message, metadata, created_at
		FROM incident_timeline_entries
		WHERE tenant_id = $1 AND incident_id = $2
		ORDER BY created_at DESC, id DESC
	`, tenantID, incidentID)
	if err != nil {
		return nil, fmt.Errorf("load incident timeline: %w", err)
	}
	defer rows.Close()

	var timeline []models.IncidentTimelineEntry
	for rows.Next() {
		var entry models.IncidentTimelineEntry
		var metadata []byte
		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.IncidentID, &entry.EntryType,
			&entry.Message, &metadata, &entry.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan incident timeline entry: %w", err)
		}
		if len(metadata) > 0 {
			entry.Metadata = append(json.RawMessage(nil), metadata...)
		} else {
			entry.Metadata = json.RawMessage(`{}`)
		}
		timeline = append(timeline, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident timeline entries: %w", err)
	}

	return timeline, nil
}
