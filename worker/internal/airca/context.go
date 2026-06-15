// Package airca implements the worker-side consumer that produces AI root
// cause analyses for incidents. It gathers the incident's probe evidence from
// the database, hands it to a provider-agnostic ai.RootCauseAnalyzer, and
// writes the structured result back to incident_ai_analyses.
package airca

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/db"
)

// recentCheckLimit bounds how many recent check_results per monitor are fed to
// the model — enough to show a failure timeline and timing/cert deltas without
// bloating the prompt.
const recentCheckLimit = 15

// buildAnalysisInput assembles the evidence bundle for one incident, reusing
// the same tables the API reads. Tenant scoping is enforced on the incident
// lookup; all linked rows descend from that incident.
func buildAnalysisInput(ctx context.Context, dbc *db.Client, tenantID, incidentID uuid.UUID) (ai.AnalysisInput, error) {
	var in ai.AnalysisInput

	row := dbc.QueryRowContext(ctx, `
		SELECT title, summary, severity, state, created_at
		FROM incidents
		WHERE id = $1 AND tenant_id = $2`, incidentID, tenantID)
	if err := row.Scan(&in.Incident.Title, &in.Incident.Summary, &in.Incident.Severity, &in.Incident.State, &in.Incident.CreatedAt); err != nil {
		return ai.AnalysisInput{}, err
	}

	alerts, err := loadAlerts(ctx, dbc, incidentID)
	if err != nil {
		return ai.AnalysisInput{}, err
	}
	in.Alerts = alerts

	monitors, err := loadMonitors(ctx, dbc, incidentID)
	if err != nil {
		return ai.AnalysisInput{}, err
	}
	in.Monitors = monitors

	maintenance, err := loadMaintenance(ctx, dbc, incidentID)
	if err != nil {
		return ai.AnalysisInput{}, err
	}
	in.Maintenance = maintenance

	return in, nil
}

func loadAlerts(ctx context.Context, dbc *db.Client, incidentID uuid.UUID) ([]ai.AlertContext, error) {
	rows, err := dbc.QueryContext(ctx, `
		SELECT m.name, a.status, a.failure_count, COALESCE(a.last_error, ''),
		       a.triggered_at, COALESCE(rcm.name, '')
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		JOIN monitors m ON m.id = a.monitor_id
		LEFT JOIN monitors rcm ON rcm.id = a.root_cause_monitor_id
		WHERE ia.incident_id = $1
		ORDER BY a.triggered_at`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []ai.AlertContext
	for rows.Next() {
		var a ai.AlertContext
		if err := rows.Scan(&a.MonitorName, &a.Status, &a.FailureCount, &a.LastError, &a.TriggeredAt, &a.DependencyRootCauseMonitor); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

func loadMonitors(ctx context.Context, dbc *db.Client, incidentID uuid.UUID) ([]ai.MonitorContext, error) {
	rows, err := dbc.QueryContext(ctx, `
		SELECT m.id, m.name, m.type, COALESCE(m.config, '{}'::jsonb), m.current_state
		FROM incident_monitors im
		JOIN monitors m ON m.id = im.monitor_id
		WHERE im.incident_id = $1
		ORDER BY m.name`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type monitorRow struct {
		id  uuid.UUID
		ctx ai.MonitorContext
	}
	var collected []monitorRow
	for rows.Next() {
		var id uuid.UUID
		var mc ai.MonitorContext
		var config []byte
		if err := rows.Scan(&id, &mc.Name, &mc.Type, &config, &mc.CurrentState); err != nil {
			return nil, err
		}
		mc.Config = json.RawMessage(config)
		collected = append(collected, monitorRow{id: id, ctx: mc})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	monitors := make([]ai.MonitorContext, 0, len(collected))
	for _, mr := range collected {
		dependsOn, err := loadDependencyNames(ctx, dbc, mr.id)
		if err != nil {
			return nil, err
		}
		mr.ctx.DependsOn = dependsOn

		checks, err := loadRecentChecks(ctx, dbc, mr.id)
		if err != nil {
			return nil, err
		}
		mr.ctx.RecentChecks = checks
		monitors = append(monitors, mr.ctx)
	}
	return monitors, nil
}

func loadDependencyNames(ctx context.Context, dbc *db.Client, monitorID uuid.UUID) ([]string, error) {
	rows, err := dbc.QueryContext(ctx, `
		SELECT dm.name
		FROM monitor_dependencies md
		JOIN monitors dm ON dm.id = md.depends_on_id
		WHERE md.monitor_id = $1
		ORDER BY dm.name`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func loadRecentChecks(ctx context.Context, dbc *db.Client, monitorID uuid.UUID) ([]ai.CheckContext, error) {
	rows, err := dbc.QueryContext(ctx, `
		SELECT status, http_status, latency_ms, COALESCE(error_message, ''), metrics_data, created_at
		FROM check_results
		WHERE monitor_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, monitorID, recentCheckLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checks []ai.CheckContext
	for rows.Next() {
		var c ai.CheckContext
		var httpStatus, latency sql.NullInt64
		var metrics []byte
		if err := rows.Scan(&c.Status, &httpStatus, &latency, &c.ErrorMessage, &metrics, &c.CheckedAt); err != nil {
			return nil, err
		}
		if httpStatus.Valid {
			v := int(httpStatus.Int64)
			c.HTTPStatus = &v
		}
		if latency.Valid {
			v := int(latency.Int64)
			c.LatencyMS = &v
		}
		if len(metrics) > 0 {
			c.Metrics = json.RawMessage(metrics)
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func loadMaintenance(ctx context.Context, dbc *db.Client, incidentID uuid.UUID) ([]ai.MaintenanceContext, error) {
	rows, err := dbc.QueryContext(ctx, `
		SELECT mw.title, mw.starts_at, mw.ends_at, array_agg(DISTINCT m.name)
		FROM maintenance_windows mw
		JOIN maintenance_window_monitors mwm ON mwm.maintenance_window_id = mw.id
		JOIN incident_monitors im ON im.monitor_id = mwm.monitor_id
		JOIN monitors m ON m.id = mwm.monitor_id
		WHERE im.incident_id = $1
		  AND NOW() BETWEEN mw.starts_at AND mw.ends_at
		GROUP BY mw.id, mw.title, mw.starts_at, mw.ends_at`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []ai.MaintenanceContext
	for rows.Next() {
		var w ai.MaintenanceContext
		var monitorNames pq.StringArray
		if err := rows.Scan(&w.Title, &w.StartsAt, &w.EndsAt, &monitorNames); err != nil {
			return nil, err
		}
		w.Monitors = []string(monitorNames)
		windows = append(windows, w)
	}
	return windows, rows.Err()
}
