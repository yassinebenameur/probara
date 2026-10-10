package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// meshEdgeSubject holds the identity of one mesh alert's directed edge.
type meshEdgeSubject struct {
	alertID     uuid.UUID
	tenantID    uuid.UUID
	sourceID    uuid.UUID
	targetID    uuid.UUID
	sourceName  string
	targetName  string
	triggeredAt time.Time
	failCount   int
	lastError   sql.NullString
}

// meshLabel is the readable subject baked into MonitorName so every existing
// channel plugin/template renders a mesh alert meaningfully.
func (s meshEdgeSubject) meshLabel() string {
	return fmt.Sprintf("mesh: %s → %s", s.sourceName, s.targetName)
}

func (s meshEdgeSubject) record(kindTriggeredAt time.Time) alertRecord {
	sourceID, targetID := s.sourceID, s.targetID
	sourceName, targetName := s.sourceName, s.targetName
	record := alertRecord{
		ID: s.alertID, TenantID: s.tenantID,
		Kind: notifications.KindMeshEdge, TriggeredAt: kindTriggeredAt,
		FailureCount:       s.failCount,
		SourceLocationID:   &sourceID,
		SourceLocationName: &sourceName,
		TargetLocationID:   &targetID,
		TargetLocationName: &targetName,
	}
	if s.lastError.Valid {
		record.LastError = &s.lastError.String
	}
	return record
}

func (s meshEdgeSubject) binding() policyBinding {
	return policyBinding{TenantID: s.tenantID, MonitorName: s.meshLabel()}
}

// evaluateMeshEdges opens alerts for down mesh edges, resolves alerts whose
// edge recovered or vanished, and dispatches notifications. Mesh alerts have
// no monitor, so they run their own lifecycle here (the monitor-joined paths
// in lifecycle.go never see them) and route to tenant default channels only.
func (a *Alerter) evaluateMeshEdges(ctx context.Context) error {
	if err := a.openMeshEdgeAlerts(ctx); err != nil {
		return err
	}
	if err := a.resolveMeshEdgeAlerts(ctx); err != nil {
		return err
	}
	return a.dispatchOpenMeshAlerts(ctx)
}

// openMeshEdgeAlerts creates one alert per down edge without an open alert.
// Race-safe across replicas via idx_alerts_one_open_mesh_edge.
func (a *Alerter) openMeshEdgeAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ms.tenant_id, ms.source_location_id, ms.target_location_id,
			sl.name, tl.name, ms.consecutive_failures, ms.last_error
		FROM location_mesh_state ms
		JOIN locations sl ON sl.id = ms.source_location_id
		JOIN locations tl ON tl.id = ms.target_location_id
		WHERE ms.current_state = 'down'
		  AND NOT EXISTS (
			SELECT 1 FROM alerts al
			WHERE al.kind = 'mesh_edge'
			  AND al.status IN ('active', 'acknowledged')
			  AND al.source_location_id = ms.source_location_id
			  AND al.target_location_id = ms.target_location_id)
	`)
	if err != nil {
		return fmt.Errorf("query down mesh edges: %w", err)
	}
	defer rows.Close()

	var edges []meshEdgeSubject
	for rows.Next() {
		var e meshEdgeSubject
		if err := rows.Scan(&e.tenantID, &e.sourceID, &e.targetID,
			&e.sourceName, &e.targetName, &e.failCount, &e.lastError); err != nil {
			return fmt.Errorf("scan down mesh edge: %w", err)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range edges {
		if err := a.openMeshEdgeAlert(ctx, e); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"source_location_id": e.sourceID, "target_location_id": e.targetID,
			}).Error("Failed to open mesh edge alert")
		}
	}
	return nil
}

func (a *Alerter) openMeshEdgeAlert(ctx context.Context, e meshEdgeSubject) error {
	var lastError *string
	if e.lastError.Valid {
		lastError = &e.lastError.String
	}

	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, last_error, source_location_id, target_location_id,
			created_at, updated_at)
		VALUES ($1, $2, NULL, NULL, 'mesh_edge', 'active', NOW(), $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (source_location_id, target_location_id)
			WHERE kind = 'mesh_edge' AND status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id
	`, uuid.New(), e.tenantID, e.failCount, lastError, e.sourceID, e.targetID).Scan(&e.alertID)
	if err == sql.ErrNoRows {
		return nil // another replica won the race
	}
	if err != nil {
		return fmt.Errorf("insert mesh edge alert: %w", err)
	}

	record := e.record(time.Now())
	a.publishAlertEvent(ctx, "created", e.binding(), &record, nil)
	return nil
}

// resolveMeshEdgeAlerts resolves open mesh alerts whose edge is up again or no
// longer exists (endpoint cleared / location disabled or soft-deleted — the
// scheduler's edge sync removes the row, and soft-deletes never fire the FK
// cascade, so orphan-resolution here is load-bearing).
func (a *Alerter) resolveMeshEdgeAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, al.target_location_id,
			COALESCE(sl.name, 'unknown'), COALESCE(tl.name, 'unknown'),
			al.triggered_at, al.failure_count, al.last_error
		FROM alerts al
		LEFT JOIN locations sl ON sl.id = al.source_location_id
		LEFT JOIN locations tl ON tl.id = al.target_location_id
		LEFT JOIN location_mesh_state ms
			ON ms.source_location_id = al.source_location_id
			AND ms.target_location_id = al.target_location_id
		WHERE al.kind = 'mesh_edge'
		  AND al.status IN ('active', 'acknowledged')
		  AND (ms.source_location_id IS NULL OR ms.current_state = 'up')
	`)
	if err != nil {
		return fmt.Errorf("query recovered mesh alerts: %w", err)
	}
	defer rows.Close()

	var toResolve []meshEdgeSubject
	for rows.Next() {
		var e meshEdgeSubject
		if err := rows.Scan(&e.alertID, &e.tenantID, &e.sourceID, &e.targetID,
			&e.sourceName, &e.targetName, &e.triggeredAt, &e.failCount, &e.lastError); err != nil {
			return fmt.Errorf("scan recovered mesh alert: %w", err)
		}
		toResolve = append(toResolve, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range toResolve {
		resolvedAt, err := a.resolveAlert(ctx, e.alertID, time.Now())
		if errors.Is(err, errAlertAlreadyResolved) {
			continue
		}
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": e.alertID}).Error("Failed to resolve mesh edge alert")
			continue
		}
		record := e.record(e.triggeredAt)
		a.publishAlertEvent(ctx, "resolved", e.binding(), &record, &resolvedAt)
		a.notifyFiredChannels(ctx, "resolved", e.binding(), &record)
	}
	return nil
}

// resolveTenantDefaultChannels returns the tenant's default channel routing —
// the only routing a monitor-less mesh alert can use.
func (a *Alerter) resolveTenantDefaultChannels(ctx context.Context, tenantID uuid.UUID) ([]channelTarget, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ac.id, ac.name, ac.type, ac.config, ac.is_active, tdc.delay_seconds
		FROM tenant_default_channels tdc
		JOIN alert_channels ac ON ac.id = tdc.channel_id
		WHERE tdc.tenant_id = $1
		ORDER BY tdc.position, ac.id
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant default channels: %w", err)
	}
	defer rows.Close()

	var targets []channelTarget
	for rows.Next() {
		var t channelTarget
		var configBytes []byte
		var delaySeconds int
		if err := rows.Scan(&t.channel.ID, &t.channel.Name, &t.channel.Type, &configBytes, &t.channel.IsActive, &delaySeconds); err != nil {
			return nil, fmt.Errorf("scan tenant default channel: %w", err)
		}
		t.channel.Config = configBytes
		t.delay = time.Duration(delaySeconds) * time.Second
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// dispatchOpenMeshAlerts fires due escalation tiers and reminders for open
// mesh alerts, mirroring dispatchOpenAlerts' delay/reminder contract.
func (a *Alerter) dispatchOpenMeshAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, al.target_location_id,
			COALESCE(sl.name, 'unknown'), COALESCE(tl.name, 'unknown'),
			al.triggered_at, al.failure_count, al.last_error,
			te.alert_reminder_seconds
		FROM alerts al
		JOIN tenants te ON te.id = al.tenant_id
		LEFT JOIN locations sl ON sl.id = al.source_location_id
		LEFT JOIN locations tl ON tl.id = al.target_location_id
		JOIN location_mesh_state ms
			ON ms.source_location_id = al.source_location_id
			AND ms.target_location_id = al.target_location_id
		WHERE al.kind = 'mesh_edge'
		  AND al.status IN ('active', 'acknowledged')
		  AND ms.current_state = 'down'
	`)
	if err != nil {
		return fmt.Errorf("query open mesh alerts: %w", err)
	}
	defer rows.Close()

	type openMeshAlert struct {
		edge            meshEdgeSubject
		reminderSeconds int
	}
	var open []openMeshAlert
	for rows.Next() {
		var oa openMeshAlert
		if err := rows.Scan(&oa.edge.alertID, &oa.edge.tenantID, &oa.edge.sourceID, &oa.edge.targetID,
			&oa.edge.sourceName, &oa.edge.targetName, &oa.edge.triggeredAt, &oa.edge.failCount,
			&oa.edge.lastError, &oa.reminderSeconds); err != nil {
			return fmt.Errorf("scan open mesh alert: %w", err)
		}
		open = append(open, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, oa := range open {
		targets, err := a.resolveTenantDefaultChannels(ctx, oa.edge.tenantID)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.edge.alertID}).Error("Failed to resolve mesh alert channels")
			continue
		}
		states, err := a.loadNotificationStates(ctx, []uuid.UUID{oa.edge.alertID})
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.edge.alertID}).Error("Failed to load mesh notification states")
			continue
		}

		record := oa.edge.record(oa.edge.triggeredAt)
		binding := oa.edge.binding()
		alertAge := now.Sub(oa.edge.triggeredAt)
		reminderInterval := time.Duration(oa.reminderSeconds) * time.Second

		for _, target := range targets {
			if !target.channel.IsActive {
				continue
			}
			state := getNotificationState(states, oa.edge.alertID, target.channel.ID)
			switch {
			case state == nil && alertAge >= target.delay:
				a.fireChannel(ctx, "created", binding, &record, target.channel, now, reminderInterval)
			case state != nil && state.LastEventType != "resolved" && reminderInterval > 0 &&
				now.Sub(state.LastSentAt) >= reminderInterval:
				a.fireChannel(ctx, "reminder", binding, &record, target.channel, now, reminderInterval)
			}
		}
	}
	return nil
}
