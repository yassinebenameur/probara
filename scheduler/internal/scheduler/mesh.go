package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/models"
)

// meshSuspectRecheckInterval mirrors suspectRecheckInterval for mesh edges:
// a suspect edge is re-probed fast to confirm or clear a potential path
// outage before the failure threshold trips.
const meshSuspectRecheckInterval = 20 * time.Second

// meshEdge is one due directed edge to probe.
type meshEdge struct {
	TenantID     uuid.UUID
	SourceID     uuid.UUID
	TargetID     uuid.UUID
	Endpoint     string
	CurrentState string
}

// runMeshBatch drives the inter-location mesh: sync the edge set from
// mesh-participating locations, then publish probe jobs for due edges. Both
// steps run in one transaction; due-edge rows are claimed FOR UPDATE SKIP
// LOCKED so concurrent scheduler replicas never double-publish (same contract
// as scheduleBatch).
func (s *Scheduler) runMeshBatch(ctx context.Context) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Mesh: failed to begin transaction")
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.syncMeshEdges(ctx, tx); err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Mesh: failed to sync edges")
		return
	}

	edges, err := s.fetchDueMeshEdges(ctx, tx)
	if err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Mesh: failed to fetch due edges")
		return
	}

	for _, edge := range edges {
		if err := s.publishMeshJob(ctx, edge); err != nil {
			// Leave next_run_at untouched: the edge stays due and retries
			// next tick, same contract as monitor publishes.
			s.meshPublishErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).
				WithField("source_location_id", edge.SourceID).
				WithField("target_location_id", edge.TargetID).
				Error("Mesh: failed to publish probe job")
			continue
		}

		delay := time.Duration(s.config.MeshProbeIntervalSeconds) * time.Second
		if edge.CurrentState == "suspect" && meshSuspectRecheckInterval < delay {
			delay = meshSuspectRecheckInterval
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE location_mesh_state
			SET next_run_at = NOW() + $1 * INTERVAL '1 second', updated_at = NOW()
			WHERE source_location_id = $2 AND target_location_id = $3
		`, int(delay.Seconds()), edge.SourceID, edge.TargetID); err != nil {
			s.dbErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).Error("Mesh: failed to update edge next_run_at")
			return
		}
		s.meshEdgesScheduled.With(prometheus.Labels{}).Inc()
	}

	if err := tx.Commit(); err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Mesh: failed to commit transaction")
	}
}

// syncMeshEdges reconciles location_mesh_state with the current set of
// mesh-participating locations: every directed pair of same-tenant, enabled,
// non-deleted locations with a mesh_endpoint gets a row; rows whose source or
// target no longer qualifies are deleted (which also lets the alerter
// orphan-resolve any open edge alert).
func (s *Scheduler) syncMeshEdges(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO location_mesh_state (tenant_id, source_location_id, target_location_id)
		SELECT a.tenant_id, a.id, b.id
		FROM locations a
		JOIN locations b ON b.tenant_id = a.tenant_id AND b.id <> a.id
		WHERE a.deleted_at IS NULL AND a.enabled = TRUE
		  AND COALESCE(a.mesh_endpoint, '') <> ''
		  AND b.deleted_at IS NULL AND b.enabled = TRUE
		  AND COALESCE(b.mesh_endpoint, '') <> ''
		ON CONFLICT (source_location_id, target_location_id) DO NOTHING
	`); err != nil {
		return fmt.Errorf("insert mesh edges: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM location_mesh_state ms
		WHERE NOT EXISTS (
			SELECT 1 FROM locations l
			WHERE l.id = ms.source_location_id
			  AND l.deleted_at IS NULL AND l.enabled = TRUE
			  AND COALESCE(l.mesh_endpoint, '') <> ''
		) OR NOT EXISTS (
			SELECT 1 FROM locations l
			WHERE l.id = ms.target_location_id
			  AND l.deleted_at IS NULL AND l.enabled = TRUE
			  AND COALESCE(l.mesh_endpoint, '') <> ''
		)
	`); err != nil {
		return fmt.Errorf("prune mesh edges: %w", err)
	}
	return nil
}

func (s *Scheduler) fetchDueMeshEdges(ctx context.Context, tx *sql.Tx) ([]meshEdge, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ms.tenant_id, ms.source_location_id, ms.target_location_id,
			l.mesh_endpoint, ms.current_state
		FROM location_mesh_state ms
		JOIN locations l ON l.id = ms.target_location_id
		WHERE ms.next_run_at <= NOW()
		ORDER BY ms.next_run_at
		LIMIT $1
		FOR UPDATE OF ms SKIP LOCKED
	`, s.config.MeshScheduleBatchSize)
	if err != nil {
		return nil, fmt.Errorf("query due mesh edges: %w", err)
	}
	defer rows.Close()

	var edges []meshEdge
	for rows.Next() {
		var e meshEdge
		if err := rows.Scan(&e.TenantID, &e.SourceID, &e.TargetID, &e.Endpoint, &e.CurrentState); err != nil {
			return nil, fmt.Errorf("scan mesh edge: %w", err)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mesh edges: %w", err)
	}
	return edges, nil
}

// publishMeshJob publishes one probe job to the source location's job subject;
// its workers dial the target's echo endpoint directly, so latency measures
// the real source→target network path.
func (s *Scheduler) publishMeshJob(ctx context.Context, edge meshEdge) error {
	if err := s.ensureLocationConsumer(ctx, edge.SourceID.String()); err != nil {
		return err
	}
	configJSON, err := json.Marshal(models.MeshProbeConfig{
		TargetLocationID: edge.TargetID.String(),
		Endpoint:         edge.Endpoint,
	})
	if err != nil {
		return fmt.Errorf("marshal mesh probe config: %w", err)
	}

	payload := models.CheckJobPayload{
		Type:           models.MonitorTypeMeshProbe,
		Config:         configJSON,
		TimeoutSeconds: s.config.MeshProbeTimeoutSeconds,
		LocationID:     edge.SourceID.String(),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mesh job payload: %w", err)
	}

	deadline := time.Now().Add(time.Duration(2*s.config.MeshProbeTimeoutSeconds) * time.Second)
	job := models.NewJob(uuid.New().String(), edge.TenantID.String(), models.JobTypeCheck, "v1", payloadJSON)
	job = job.WithDeadline(deadline)

	return s.publishJob(ctx, s.jobSubject(edge.SourceID.String()), job)
}

// pruneTenantMeshResults applies the tenant's retention window to
// mesh_probe_results, mirroring pruneTenantCheckResults.
func (s *Scheduler) pruneTenantMeshResults(ctx context.Context, tenantID uuid.UUID, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	deleteQuery := `
		DELETE FROM mesh_probe_results
		WHERE id IN (
			SELECT id
			FROM mesh_probe_results
			WHERE tenant_id = $1
			  AND created_at < $2
			ORDER BY created_at ASC
			LIMIT $3
		)
	`

	var totalDeleted int64
	maxRows := s.config.RetentionCleanupMaxRowsPerRun
	batchSize := s.config.RetentionCleanupBatchSize
	if batchSize <= 0 {
		batchSize = 5000
	}
	if maxRows <= 0 {
		maxRows = 200000
	}

	for int(totalDeleted) < maxRows {
		remaining := maxRows - int(totalDeleted)
		limit := batchSize
		if remaining < limit {
			limit = remaining
		}

		result, err := s.db.ExecContext(ctx, deleteQuery, tenantID, cutoff, limit)
		if err != nil {
			return totalDeleted, err
		}
		rowsDeleted, err := result.RowsAffected()
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to read rows affected: %w", err)
		}
		if rowsDeleted == 0 {
			break
		}

		totalDeleted += rowsDeleted
		if rowsDeleted < int64(limit) {
			break
		}
	}

	return totalDeleted, nil
}
