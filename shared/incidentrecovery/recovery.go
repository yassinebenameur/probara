// Package incidentrecovery records, on an incident's timeline, the moment all
// of its linked alerts have recovered.
//
// Two services resolve alerts: the alerter (a monitor recovered) and the API
// (an operator resolved one, singly or in bulk). Both must write the same
// "All linked alerts recovered" entry exactly once per recovery, and they can
// race on the same incident, so the rule lives here once. Serialization rests
// on a transaction-scoped advisory lock keyed by the incident; a caller that
// took the lock under any other key would stop serializing with the other
// service.
package incidentrecovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	// AlertsRecoveredMessage is the timeline entry text. The operator UI and
	// the duplicate check below both match on it.
	AlertsRecoveredMessage = "All linked alerts recovered"
	// SystemEntryType is the incident_timeline_entries.entry_type for entries
	// written by the platform rather than an operator.
	SystemEntryType = "system"
)

// Execer is the part of *sql.Tx (or *sql.DB) a timeline insert needs.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RecordForAlertTx appends the recovery entry to every incident alertID is
// linked to whose linked alerts are now all resolved. Call it in the
// transaction that resolved the alert, after the status change.
func RecordForAlertTx(ctx context.Context, tx *sql.Tx, alertID uuid.UUID) error {
	return record(ctx, tx, uuid.Nil, alertID)
}

// RecordForTenantAlertTx is RecordForAlertTx restricted to tenantID's
// incidents, for request paths where the tenant comes from the caller.
func RecordForTenantAlertTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("record incident recovery: tenant is required")
	}
	return record(ctx, tx, tenantID, alertID)
}

type target struct {
	incidentID uuid.UUID
	tenantID   uuid.UUID
}

func record(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	targets, err := loadTargets(ctx, tx, tenantID, alertID)
	if err != nil {
		return err
	}
	// A fixed lock order keeps two transactions resolving alerts that share
	// several incidents from deadlocking.
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].incidentID.String() < targets[j].incidentID.String()
	})

	for _, t := range targets {
		if err := lockIncident(ctx, tx, t.incidentID); err != nil {
			return err
		}
		allResolved, resolvedAfter, err := linkedAlertsResolved(ctx, tx, t.incidentID)
		if err != nil {
			return err
		}
		if !allResolved {
			continue
		}
		// The marker identifies this recovery: an incident whose alerts fire
		// and recover again gets a second entry, a retry of the same recovery
		// does not.
		marker := resolvedAfter.UTC().Format(time.RFC3339Nano)
		recorded, err := alreadyRecorded(ctx, tx, t.incidentID, marker)
		if err != nil {
			return err
		}
		if recorded {
			continue
		}
		if err := InsertTimelineEntryTx(ctx, tx, t.tenantID, t.incidentID, SystemEntryType, AlertsRecoveredMessage, map[string]any{
			"alert_id":       alertID.String(),
			"resolved_after": marker,
		}); err != nil {
			return err
		}
	}
	return nil
}

func loadTargets(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) ([]target, error) {
	var tenantParam any
	if tenantID != uuid.Nil {
		tenantParam = tenantID
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT i.id, i.tenant_id
		FROM incidents i
		JOIN incident_alerts ia ON ia.incident_id = i.id
		JOIN alerts a ON a.id = ia.alert_id AND a.tenant_id = i.tenant_id
		WHERE ia.alert_id = $1 AND ($2::uuid IS NULL OR i.tenant_id = $2::uuid)
	`, alertID, tenantParam)
	if err != nil {
		return nil, fmt.Errorf("load incidents for alert recovery: %w", err)
	}
	defer rows.Close()

	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.incidentID, &t.tenantID); err != nil {
			return nil, fmt.Errorf("scan incident recovery target: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident recovery targets: %w", err)
	}
	return targets, nil
}

func lockIncident(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) error {
	lockKey := fmt.Sprintf("incident-recovery:%s", incidentID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock incident recovery key: %w", err)
	}
	return nil
}

// linkedAlertsResolved reports whether the incident has linked alerts and none
// is open, and when the last of them resolved.
func linkedAlertsResolved(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) (bool, time.Time, error) {
	var total, unresolved int
	var resolvedAfter sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE a.status IN ('active', 'acknowledged')),
			MAX(COALESCE(a.resolved_at, a.updated_at))
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		WHERE ia.incident_id = $1
	`, incidentID).Scan(&total, &unresolved, &resolvedAfter); err != nil {
		return false, time.Time{}, fmt.Errorf("load linked alert statuses: %w", err)
	}
	if total == 0 || unresolved != 0 || !resolvedAfter.Valid {
		return false, time.Time{}, nil
	}
	return true, resolvedAfter.Time.UTC(), nil
}

func alreadyRecorded(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID, marker string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM incident_timeline_entries
			WHERE incident_id = $1
				AND entry_type = $2
				AND message = $3
				AND metadata->>'resolved_after' = $4
		)
	`, incidentID, SystemEntryType, AlertsRecoveredMessage, marker).Scan(&exists); err != nil {
		return false, fmt.Errorf("check incident recovery timeline entry: %w", err)
	}
	return exists, nil
}

// InsertTimelineEntryTx appends one entry to an incident's timeline and bumps
// the incident's updated_at. It is the single writer of
// incident_timeline_entries for both the API and the alerter.
func InsertTimelineEntryTx(ctx context.Context, exec Execer, tenantID, incidentID uuid.UUID, entryType, message string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal incident timeline metadata: %w", err)
	}

	if _, err := exec.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (
			id, tenant_id, incident_id, entry_type, message, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
	`, uuid.New(), tenantID, incidentID, entryType, message, metadataJSON); err != nil {
		return fmt.Errorf("insert incident timeline entry: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE incidents
		SET updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, incidentID, tenantID); err != nil {
		return fmt.Errorf("touch incident updated_at: %w", err)
	}
	return nil
}
