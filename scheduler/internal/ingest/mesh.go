package ingest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// handleMeshResult persists one inter-location mesh probe. Mesh results carry
// no monitor: LocationID is the probing (source) location and Mesh holds the
// target, so they bypass the monitor pipeline entirely — raw sample into
// mesh_probe_results (whose UNIQUE(job_id) is the durable dedupe) and the
// per-edge temporal state machine on location_mesh_state.
func (i *Ingest) handleMeshResult(ctx context.Context, m models.CheckResultMessage) error {
	logEntry := i.logger.WithFields(logrus.Fields{
		"job_id":    m.JobID,
		"tenant_id": m.TenantID,
		"source":    m.LocationID,
		"target":    m.Mesh.TargetLocationID,
	})

	tenantID, err := uuid.Parse(m.TenantID)
	if err != nil {
		logEntry.WithError(err).Error("Mesh result: invalid tenant_id")
		return nil
	}
	jobID, err := uuid.Parse(m.JobID)
	if err != nil {
		logEntry.WithError(err).Error("Mesh result: invalid job_id")
		return nil
	}
	sourceID, err := uuid.Parse(m.LocationID)
	if err != nil {
		logEntry.WithError(err).Error("Mesh result: invalid source location_id")
		return nil
	}
	targetID, err := uuid.Parse(m.Mesh.TargetLocationID)
	if err != nil {
		logEntry.WithError(err).Error("Mesh result: invalid target location_id")
		return nil
	}

	outcome, err := i.persistMeshResult(ctx, m, tenantID, jobID, sourceID, targetID)
	if err != nil {
		// Same shape as isMonitorGone: a location hard-deleted while the
		// probe was in flight surfaces as a locations FK violation.
		if isMonitorGone(err) {
			logEntry.Debug("Dropping mesh result for missing location")
			return nil
		}
		logEntry.WithError(err).Error("Failed to persist mesh result")
		i.ingestErrors.With(prometheus.Labels{}).Inc()
		return err
	}
	if outcome.duplicate {
		i.duplicates.Inc()
		return nil
	}
	i.meshIngested.Inc()
	return nil
}

func (i *Ingest) persistMeshResult(ctx context.Context, m models.CheckResultMessage, tenantID, jobID, sourceID, targetID uuid.UUID) (outcome, error) {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return outcome{}, fmt.Errorf("begin mesh transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Lock the edge row first; every writer for the same edge serializes here.
	// No row means the edge was removed (endpoint cleared, location disabled)
	// while the job was in flight — drop the result, don't resurrect state.
	var snap monitorstate.Snapshot
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures
		FROM location_mesh_state
		WHERE source_location_id = $1 AND target_location_id = $2
		FOR UPDATE
	`, sourceID, targetID).Scan(&snap.State, &snap.ConsecutiveFailures)
	if err == sql.ErrNoRows {
		if err := tx.Commit(); err != nil {
			return outcome{}, fmt.Errorf("commit mesh transaction: %w", err)
		}
		return outcome{}, nil
	}
	if err != nil {
		return outcome{}, fmt.Errorf("lock mesh edge state: %w", err)
	}

	// Durable idempotency: at-least-once redelivery of the same job must not
	// double-advance consecutive_failures.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO mesh_probe_results (
			id, tenant_id, source_location_id, target_location_id,
			job_id, status, latency_ms, error_message, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (job_id) DO NOTHING
	`, uuid.New(), tenantID, sourceID, targetID, jobID, m.Status, m.LatencyMs, m.ErrorMessage, m.CompletedAt)
	if err != nil {
		return outcome{}, fmt.Errorf("insert mesh probe result: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		if err := tx.Commit(); err != nil {
			return outcome{}, fmt.Errorf("commit mesh transaction: %w", err)
		}
		return outcome{duplicate: true}, nil
	}

	transition := monitorstate.Apply(snap, monitorstate.IsFailureStatus(m.Status), i.config.MeshFailureThreshold)
	if _, err := tx.ExecContext(ctx, `
		UPDATE location_mesh_state
		SET current_state = $1,
			consecutive_failures = $2,
			last_latency_ms = $3,
			last_error = $4,
			last_check_at = NOW(),
			last_state_change_at = CASE WHEN $5 THEN NOW() ELSE last_state_change_at END,
			updated_at = NOW()
		WHERE source_location_id = $6 AND target_location_id = $7
	`, string(transition.To), transition.ConsecutiveFailures, m.LatencyMs, m.ErrorMessage,
		transition.Changed, sourceID, targetID); err != nil {
		return outcome{}, fmt.Errorf("update mesh edge state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return outcome{}, fmt.Errorf("commit mesh transaction: %w", err)
	}
	return outcome{stateChanged: transition.Changed}, nil
}
