// Package airca gathers tenant-scoped monitoring evidence for local decisions.
package airca

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"time"
)

const recentCheckLimit = 24
const maxWindowChecks = 20000

// maxMeshEdgeNotes bounds the per-alert mesh-edge lines in incomplete_evidence;
// any remainder is summarized by count.
const maxMeshEdgeNotes = 10

func buildAnalysisInput(ctx context.Context, dbc *db.Client, tenantID, incidentID uuid.UUID, requestedAt time.Time) (ai.AnalysisInput, error) {
	var in ai.AnalysisInput
	// Free-form title and summary are deliberately not fetched for inference.
	err := dbc.QueryRowContext(ctx, `SELECT created_at FROM incidents WHERE id=$1 AND tenant_id=$2`, incidentID, tenantID).Scan(&in.Incident.CreatedAt)
	if err != nil {
		return in, err
	}
	// The window bounds monitor checks, so only monitor alerts anchor it. The
	// inner join on monitors drops mesh_edge alerts (monitor_id IS NULL) on
	// purpose: they reach incidents only by manual attach, stay open per edge
	// for days, and anchoring on one would pull the window off the attached
	// monitors' failures. They are listed by noteMeshEdgeAlerts instead.
	var earliest sql.NullTime
	err = dbc.QueryRowContext(ctx, `SELECT MIN(a.triggered_at) FROM incident_alerts ia
 JOIN alerts a ON a.id=ia.alert_id AND a.tenant_id=$2
 JOIN monitors m ON m.id=a.monitor_id AND m.tenant_id=$2
 JOIN incidents i ON i.id=ia.incident_id AND i.tenant_id=$2
 WHERE ia.incident_id=$1`, incidentID, tenantID).Scan(&earliest)
	if err != nil {
		return in, err
	}
	anchor := in.Incident.CreatedAt
	if earliest.Valid {
		anchor = earliest.Time
	}
	if anchor.After(requestedAt) {
		anchor = requestedAt
		in.Incomplete = append(in.Incomplete, "Incident anchor lies after analysis request; capped to request time")
	}
	in.WindowStart = anchor.Add(-15 * time.Minute)
	in.WindowEnd = anchor.Add(60 * time.Minute)
	if in.WindowEnd.After(requestedAt) {
		in.WindowEnd = requestedAt
	}
	if in.WindowStart.Before(time.Now().UTC().AddDate(0, 0, -monitorstate.CheckResultsRawRetentionDays)) {
		in.Incomplete = append(in.Incomplete, "Raw check evidence may be expired under the 30-day retention policy")
	}
	// Resolve affected monitors, bounded group leaves, configured upstreams and
	// stored dependency roots, with explicit tenant constraints at every join.
	rows, err := dbc.QueryContext(ctx, `WITH RECURSIVE affected(id,path,depth) AS (
 SELECT m.id,ARRAY[m.id],0 FROM monitors m WHERE m.tenant_id=$2 AND m.deleted_at IS NULL AND (
 EXISTS(SELECT 1 FROM incident_monitors im JOIN incidents i ON i.id=im.incident_id AND i.tenant_id=$2 WHERE im.incident_id=$1 AND im.monitor_id=m.id)
 OR EXISTS(SELECT 1 FROM incident_alerts ia JOIN alerts a ON a.id=ia.alert_id AND a.tenant_id=$2 JOIN incidents i ON i.id=ia.incident_id AND i.tenant_id=$2 WHERE ia.incident_id=$1 AND a.monitor_id=m.id))
 UNION ALL
 SELECT child.id,af.path||child.id,af.depth+1 FROM affected af JOIN monitor_groups mg ON mg.group_id=af.id
 JOIN monitors child ON child.id=mg.monitor_id AND child.tenant_id=$2 AND child.deleted_at IS NULL
 WHERE af.depth<8 AND NOT child.id=ANY(af.path)
 ), selected AS (
 SELECT DISTINCT id,TRUE AS affected FROM affected
 UNION SELECT md.depends_on_id,FALSE FROM monitor_dependencies md JOIN affected af ON af.id=md.monitor_id
 UNION SELECT a.root_cause_monitor_id,FALSE FROM incident_alerts ia JOIN alerts a ON a.id=ia.alert_id AND a.tenant_id=$2
 JOIN incidents i ON i.id=ia.incident_id AND i.tenant_id=$2 WHERE ia.incident_id=$1 AND a.root_cause_monitor_id IS NOT NULL
 )
 SELECT m.id,m.name,m.type,COALESCE(m.current_state,'unknown'),BOOL_OR(s.affected),m.interval_seconds,
 EXISTS(SELECT 1 FROM affected af JOIN monitor_groups mg ON mg.group_id=af.id
 JOIN monitors child ON child.id=mg.monitor_id AND child.tenant_id=$2 AND child.deleted_at IS NULL
 WHERE af.depth=8 AND NOT child.id=ANY(af.path))
 FROM selected s JOIN monitors m ON m.id=s.id AND m.tenant_id=$2 AND m.deleted_at IS NULL
 WHERE m.type<>'group' GROUP BY m.id ORDER BY BOOL_OR(s.affected) DESC,m.id LIMIT 101`, incidentID, tenantID)
	if err != nil {
		return in, err
	}
	type row struct {
		m              ai.MonitorContext
		id             uuid.UUID
		interval       int
		groupTruncated bool
	}
	collected := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.m.Name, &r.m.Type, &r.m.CurrentState, &r.m.Affected, &r.interval, &r.groupTruncated); err != nil {
			rows.Close()
			return in, err
		}
		r.m.Ref = r.id.String()
		collected = append(collected, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return in, err
	}
	rows.Close()
	if len(collected) > 0 && collected[0].groupTruncated {
		in.Incomplete = append(in.Incomplete, "Incident group traversal truncated at depth 8")
	}
	if len(collected) > 100 {
		collected = collected[:100]
		in.Incomplete = append(in.Incomplete, "Incident monitor/upstream set truncated to 100")
	}
	firstFailures := map[string]time.Time{}
	for _, r := range collected {
		checks, total, failures, first, err := loadWindowChecks(ctx, dbc, tenantID, r.id, in.WindowStart, in.WindowEnd)
		if err != nil {
			return in, err
		}
		r.m.Summary = ai.SummarizeChecks(checks)
		for _, c := range checks {
			if c.Status == "error" && c.ErrorCategory == "unknown" {
				in.Incomplete = append(in.Incomplete, r.m.Ref+": checker errors have no supported failure category")
				break
			}
		}
		if total > len(checks) {
			in.Incomplete = append(in.Incomplete, fmt.Sprintf("%s: detailed checks truncated (%d of %d rows); full-window count retained", r.m.Ref, len(checks), total))
			r.m.Summary.Checks = total
			r.m.Summary.Failures = failures
			r.m.Summary.Conflict = true // Unseen detail must not produce a diagnosis.
		}
		if first.Valid {
			firstFailures[r.m.Ref] = first.Time
		}
		sampled, truncated := ai.SampleTransitions(checks, recentCheckLimit)
		r.m.RecentChecks = sampled
		if truncated {
			in.Incomplete = append(in.Incomplete, r.m.Ref+": check timeline sampled; summary uses full window")
		}
		dependencies, err := loadDependencies(ctx, dbc, tenantID, r.id)
		if err != nil {
			return in, err
		}
		r.m.DependsOn = dependencies
		for _, upstream := range dependencies {
			found := false
			for _, selected := range collected {
				if selected.m.Ref == upstream {
					found = true
					break
				}
			}
			if !found {
				in.Incomplete = append(in.Incomplete, r.m.Ref+": configured upstream evidence unavailable or excluded by monitor limit")
			}
		}
		if r.m.Type == "agent" {
			metrics, pressure, err := loadHostMetrics(ctx, dbc, tenantID, r.id, in.WindowStart, in.WindowEnd, r.interval)
			if err != nil {
				return in, err
			}
			r.m.Summary.HostMetricsAvailable = metrics
			r.m.Summary.ResourcePressure = pressure
			if !metrics {
				in.Incomplete = append(in.Incomplete, r.m.Ref+": host telemetry missing or expired")
			}
		}
		in.Monitors = append(in.Monitors, r.m)
	}
	if err := noteMeshEdgeAlerts(ctx, dbc, tenantID, incidentID, &in); err != nil {
		return in, err
	}
	// Dependency roots attach to monitor evidence only, so the inner joins on
	// monitors are intended here: a mesh_edge alert has no monitor to attach a
	// root to and is reported separately by noteMeshEdgeAlerts.
	rootRows, err := dbc.QueryContext(ctx, `SELECT DISTINCT a.monitor_id,a.root_cause_monitor_id FROM incident_alerts ia
 JOIN alerts a ON a.id=ia.alert_id AND a.tenant_id=$2
 JOIN monitors m ON m.id=a.monitor_id AND m.tenant_id=$2
 JOIN monitors u ON u.id=a.root_cause_monitor_id AND u.tenant_id=$2
 JOIN incidents i ON i.id=ia.incident_id AND i.tenant_id=$2 WHERE ia.incident_id=$1`, incidentID, tenantID)
	if err != nil {
		return in, err
	}
	for rootRows.Next() {
		var child, root string
		if err := rootRows.Scan(&child, &root); err != nil {
			rootRows.Close()
			return in, err
		}
		for i := range in.Monitors {
			if in.Monitors[i].Ref == child {
				in.Monitors[i].DependencyRoots = append(in.Monitors[i].DependencyRoots, root)
			}
		}
	}
	if err := rootRows.Err(); err != nil {
		rootRows.Close()
		return in, err
	}
	rootRows.Close()
	for i := range in.Monitors {
		m := &in.Monitors[i]
		failure, ok := firstFailures[m.Ref]
		if !ok {
			continue
		}
		for _, u := range append(append([]string{}, m.DependsOn...), m.DependencyRoots...) {
			if t, ok := firstFailures[u]; ok && t.Before(failure) {
				m.Summary.UpstreamFailedBefore = true
			}
		}
	}
	ids := []uuid.UUID{}
	for _, r := range collected {
		ids = append(ids, r.id)
	}
	// S-E1: platform rows (job expired before a worker observed the target)
	// are not evidence, so they cannot place a failure inside maintenance.
	rows, err = dbc.QueryContext(ctx, `SELECT m.id FROM monitors m
 WHERE m.tenant_id=$1 AND m.id=ANY($2)
 AND EXISTS(SELECT 1 FROM check_results cr WHERE cr.tenant_id=$1 AND cr.monitor_id=m.id AND cr.result_source='monitor'
 AND cr.created_at>=$3 AND cr.created_at<=$4
 AND (cr.status='failure' OR (cr.status='error' AND LEFT(cr.error_message,4096) ~* $5))
 AND `+maintenance.InMaintenanceAtPredicate("m", "cr.created_at")+`)`, tenantID, pq.Array(ids), in.WindowStart, in.WindowEnd, classifiedErrorPattern)
	if err != nil {
		return in, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return in, err
		}
		for i := range in.Monitors {
			if in.Monitors[i].Ref == id {
				in.Monitors[i].Summary.MaintenanceOverlap = true
			}
		}
	}

	return in, rows.Err()
}

func loadDependencies(ctx context.Context, dbc *db.Client, tenantID, monitorID uuid.UUID) ([]string, error) {
	rows, err := dbc.QueryContext(ctx, `SELECT upstream.id FROM monitor_dependencies md
 JOIN monitors m ON m.id=md.monitor_id AND m.tenant_id=$1
 JOIN monitors upstream ON upstream.id=md.depends_on_id AND upstream.tenant_id=$1
 WHERE md.monitor_id=$2 ORDER BY upstream.id`, tenantID, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

const classifiedErrorPattern = "no such host|dns lookup|dns resolution|dns error|certificate|tls handshake|x509:|connection refused|network is unreachable|no route to host|connection reset|unexpected status|status code|status_code:|assertion|expected status"

// loadWindowChecks reads only result_source='monitor' rows (S-E1,
// docs/state-semantics.md): platform rows record that a job expired before any
// worker observed the target, so counting them would report scheduler or
// queue trouble as target failures.
func loadWindowChecks(ctx context.Context, dbc *db.Client, tenantID, monitorID uuid.UUID, start, end time.Time) ([]ai.CheckContext, int, int, sql.NullTime, error) {
	rows, err := dbc.QueryContext(ctx, `SELECT cr.status,cr.http_status,cr.latency_ms,LEFT(COALESCE(cr.error_message,''),4096),
 CASE WHEN octet_length(cr.metrics_data::text)<=65536 THEN cr.metrics_data ELSE NULL END,cr.created_at,COUNT(*) OVER(),
 COUNT(*) FILTER(WHERE cr.status='failure' OR (cr.status='error' AND LEFT(cr.error_message,4096) ~* $6)) OVER(),
 MIN(cr.created_at) FILTER(WHERE cr.status='failure' OR (cr.status='error' AND LEFT(cr.error_message,4096) ~* $6)) OVER()
 FROM check_results cr JOIN monitors m ON m.id=cr.monitor_id AND m.tenant_id=$1
 WHERE cr.tenant_id=$1 AND cr.monitor_id=$2 AND cr.result_source='monitor' AND cr.created_at>=$3 AND cr.created_at<=$4
 ORDER BY cr.created_at,cr.id LIMIT $5`, tenantID, monitorID, start, end, maxWindowChecks, classifiedErrorPattern)
	if err != nil {
		return nil, 0, 0, sql.NullTime{}, err
	}
	defer rows.Close()
	checks := []ai.CheckContext{}
	total, failures := 0, 0
	var first sql.NullTime
	for rows.Next() {
		var c ai.CheckContext
		var status, latency sql.NullInt64
		var metrics []byte
		if err := rows.Scan(&c.Status, &status, &latency, &c.ErrorMessage, &metrics, &c.CheckedAt, &total, &failures, &first); err != nil {
			return nil, 0, 0, sql.NullTime{}, err
		}
		if status.Valid {
			v := int(status.Int64)
			c.HTTPStatus = &v
		}
		if latency.Valid {
			v := int(latency.Int64)
			c.LatencyMS = &v
		}
		c.Metrics = metrics
		c.ErrorCategory = ai.ErrorCategory(c.ErrorMessage)
		checks = append(checks, c)
	}
	return checks, total, failures, first, rows.Err()
}
func loadHostMetrics(ctx context.Context, dbc *db.Client, tenantID, monitorID uuid.UUID, start, end time.Time, interval int) (bool, bool, error) {
	var available, pressure bool
	// Only allowlisted native ratio gauges. The sample must overlap a failure,
	// within the monitor's existing freshness horizon; old high values abstain.
	// Only monitor-sourced failures count (S-E1): a platform row is not an
	// observation of the host.
	horizon := monitorstate.FreshnessHorizonSeconds(interval)
	err := dbc.QueryRowContext(ctx, `WITH samples AS (SELECT ms.value,EXISTS(
 SELECT 1 FROM check_results cr WHERE cr.tenant_id=$1 AND cr.monitor_id=$2 AND cr.result_source='monitor' AND cr.created_at>=$3 AND cr.created_at<=$4
 AND cr.created_at>=ms.ts AND cr.created_at<=ms.ts+($5*INTERVAL '1 second')
 AND (cr.status='failure' OR (cr.status='error' AND LEFT(cr.error_message,4096) ~* $6))) AS fresh
 FROM metric_series s JOIN metric_samples ms ON ms.series_id=s.id
 WHERE s.tenant_id=$1 AND s.monitor_id=$2 AND s.metric_name IN ('system.cpu.utilization','system.memory.utilization')
 AND s.metric_type='gauge' AND s.attributes->>'state'='used' AND ms.ts>=$3 AND ms.ts<=$4)
 SELECT COALESCE(BOOL_OR(fresh),FALSE),COALESCE(BOOL_OR(fresh AND value>0.9 AND value<=1),FALSE)
 FROM samples`, tenantID, monitorID, start, end, horizon, classifiedErrorPattern).Scan(&available, &pressure)
	return available, pressure, err
}

// noteMeshEdgeAlerts records linked mesh_edge alerts in the persisted
// incomplete_evidence list. Their subject is a directed location pair, not a
// monitor, so they have no MonitorEvidence and are deliberately never sent to
// the decision service (its request shape is a fixed training contract);
// listing them keeps the analysis honest that part of the incident was not
// modeled. Only opaque alert and location IDs are written, never names.
func noteMeshEdgeAlerts(ctx context.Context, dbc *db.Client, tenantID, incidentID uuid.UUID, in *ai.AnalysisInput) error {
	rows, err := dbc.QueryContext(ctx, `SELECT a.id,a.source_location_id,a.target_location_id,COUNT(*) OVER()
 FROM incident_alerts ia
 JOIN alerts a ON a.id=ia.alert_id AND a.tenant_id=$2
 JOIN incidents i ON i.id=ia.incident_id AND i.tenant_id=$2
 WHERE ia.incident_id=$1 AND a.kind='mesh_edge' AND a.monitor_id IS NULL
 ORDER BY a.triggered_at,a.id LIMIT $3`, incidentID, tenantID, maxMeshEdgeNotes)
	if err != nil {
		return err
	}
	defer rows.Close()
	total, listed := 0, 0
	for rows.Next() {
		var id uuid.UUID
		var source, target uuid.NullUUID
		if err := rows.Scan(&id, &source, &target, &total); err != nil {
			return err
		}
		in.Incomplete = append(in.Incomplete, fmt.Sprintf("%s: linked mesh-edge alert (location %s -> %s) not modeled; location connectivity is outside monitor evidence", id, nullableRef(source), nullableRef(target)))
		listed++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if total > listed {
		in.Incomplete = append(in.Incomplete, fmt.Sprintf("%d more linked mesh-edge alerts not modeled", total-listed))
	}
	return nil
}

func nullableRef(id uuid.NullUUID) string {
	if !id.Valid {
		return "unknown"
	}
	return id.UUID.String()
}
