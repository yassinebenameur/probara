package pagesubscribers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/services/incidents"
	"github.com/yassinebenameur/probara/api/internal/services/pagenotify"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/autoincident"
	"github.com/yassinebenameur/probara/shared/incidentrecovery"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/statuspagesql"
)

// Automatic publication of monitor outages (the page setting
// auto_publish_incidents, off by default).
//
// An outage is a monitor's open availability alert: the alerter opens it
// only for a monitor that is down outside maintenance, and resolves it only
// once a recovery is confirmed (S-T7). A page that opted in gets the outage
// published once it has lasted AutoPublishDelay, counted from the alert's
// trigger or its last relapse reopen, if the monitor is one the page
// renders and, at that moment, enabled, still down, not in maintenance, not
// already passing again (recovering) and not explained by a dependency
// (alertrouting.SuppressedByDependencyPredicate). It is published at most
// once per outage and page: not again after an operator took it down or
// resolved it, and not while an operator's own unresolved incident for that
// monitor is on the page.
//
// The incident is the monitor's open automatic incident
// (shared/autoincident, the one the alerter's "create incident on fire"
// opens too), created with neutral public wording when there is none. It is
// resolved automatically once every alert linked to it is resolved, while
// an automatic publication of it is still on a page.

// AutoPublishDelay is how long an outage lasts before it is published.
const AutoPublishDelay = 5 * time.Minute

// The wording of an incident automatic publishing creates. It is public:
// it names no monitor (the page lists the affected components under it).
const (
	autoIncidentTitle   = "Service disruption"
	autoIncidentSummary = "We are seeing a disruption on the components listed below and are looking into it. This notice was published automatically."
	maxOutagesPerTick   = 50
)

// outagePagesSQL selects (alert, tenant, monitor, page) for every outage due
// on a page; $1 is the delay in seconds, $2 an alert ID or NULL, $3 a limit.
var outagePagesSQL = `
	SELECT a.id, a.tenant_id, a.monitor_id, sp.id
	FROM alerts a
	JOIN monitors m ON m.id = a.monitor_id AND m.tenant_id = a.tenant_id
	JOIN tenants t ON t.id = a.tenant_id
	JOIN ` + statuspagesql.RenderedMonitorsSQL + ` rm ON rm.monitor_id = a.monitor_id
	JOIN status_pages sp ON sp.id = rm.status_page_id AND sp.tenant_id = a.tenant_id
	WHERE a.kind = 'availability' AND a.status IN ('active', 'acknowledged')
	  AND COALESCE(a.reopened_at, a.triggered_at) <= NOW() - make_interval(secs => $1)
	  AND COALESCE(sp.settings->>'auto_publish_incidents', 'false') = 'true'
	  AND m.enabled AND m.deleted_at IS NULL AND m.current_state = 'down'
	  AND NOT ` + maintenance.InMaintenancePredicate("m") + `
	  AND NOT ` + monitorstate.RecoveringPredicate("m") + `
	  AND NOT ` + alertrouting.SuppressedByDependencyPredicate("a", "m", "t") + `
	  -- Once per outage and page: whatever became of that publication since.
	  AND NOT EXISTS (
		SELECT 1 FROM incident_alerts ia
		JOIN incident_status_page_publications isp ON isp.incident_id = ia.incident_id AND isp.status_page_id = sp.id
		WHERE ia.alert_id = a.id AND isp.published_at >= COALESCE(a.reopened_at, a.triggered_at))
	  -- An unresolved incident about this monitor is already on the page.
	  AND NOT EXISTS (
		SELECT 1 FROM incident_status_page_publications isp
		JOIN incidents i ON i.id = isp.incident_id AND i.state <> 'resolved'
		WHERE isp.status_page_id = sp.id AND isp.unpublished_at IS NULL
		  AND (EXISTS (SELECT 1 FROM incident_monitors im WHERE im.incident_id = i.id AND im.monitor_id = a.monitor_id)
			OR EXISTS (SELECT 1 FROM incident_status_page_monitors ispm
				WHERE ispm.incident_id = i.id AND ispm.status_page_id = sp.id AND ispm.monitor_id = a.monitor_id)))
	  AND ($2::uuid IS NULL OR a.id = $2::uuid)
	ORDER BY COALESCE(a.reopened_at, a.triggered_at), a.id, sp.id
	LIMIT $3`

type outage struct {
	alertID, tenantID, monitorID uuid.UUID
}

// autoPublish publishes every due outage (up to maxOutagesPerTick alerts).
func (s *Service) autoPublish(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, outagePagesSQL, AutoPublishDelay.Seconds(), nil, maxOutagesPerTick*4)
	if err != nil {
		return fmt.Errorf("find outages to publish: %w", err)
	}
	var due []outage
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		var o outage
		var pageID uuid.UUID
		if err := rows.Scan(&o.alertID, &o.tenantID, &o.monitorID, &pageID); err != nil {
			rows.Close()
			return fmt.Errorf("scan outage: %w", err)
		}
		if !seen[o.alertID] && len(due) < maxOutagesPerTick {
			seen[o.alertID] = true
			due = append(due, o)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("find outages to publish: %w", err)
	}
	for _, o := range due {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.publishOutage(ctx, o); err != nil {
			if s.log != nil {
				s.log.WithError(err).WithField("alert_id", o.alertID.String()).Error("Failed to publish an outage to its status pages")
			}
		}
	}
	return nil
}

// publishOutage publishes one outage on every page it is due on, under the
// monitor's automatic-incident lock: the pages are selected again once the
// lock is held, so two replicas (or the alerter opening the same incident)
// never publish it twice.
func (s *Service) publishOutage(ctx context.Context, o outage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := autoincident.LockTx(ctx, tx, o.tenantID, o.monitorID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, outagePagesSQL, AutoPublishDelay.Seconds(), o.alertID, 1000)
	if err != nil {
		return fmt.Errorf("recheck outage: %w", err)
	}
	var pages []uuid.UUID
	for rows.Next() {
		var alertID, tenantID, monitorID, pageID uuid.UUID
		if err := rows.Scan(&alertID, &tenantID, &monitorID, &pageID); err != nil {
			rows.Close()
			return err
		}
		pages = append(pages, pageID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(pages) == 0 {
		return tx.Commit()
	}

	incidentID, created, err := autoincident.FindOrCreateTx(ctx, tx, o.tenantID, o.monitorID, autoIncidentTitle, autoIncidentSummary)
	if err != nil {
		return err
	}
	if _, err := autoincident.LinkAlertTx(ctx, tx, incidentID, o.alertID); err != nil {
		return err
	}
	if _, err := autoincident.LinkMonitorTx(ctx, tx, incidentID, o.monitorID); err != nil {
		return err
	}
	if created {
		if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, o.tenantID, incidentID, incidentrecovery.SystemEntryType,
			"Incident auto-created to publish a monitor outage", map[string]any{
				"alert_id": o.alertID.String(), "monitor_id": o.monitorID.String(),
			}); err != nil {
			return err
		}
	}
	for _, pageID := range pages {
		if _, err := incidents.PublishTx(ctx, tx, o.tenantID, incidentID, pageID, []uuid.UUID{o.monitorID}, true); err != nil {
			return err
		}
		if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, o.tenantID, incidentID, incidentrecovery.SystemEntryType,
			"Incident published to status page automatically", map[string]any{
				"status_page_id": pageID.String(), "monitor_ids": []string{o.monitorID.String()},
			}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outage publication: %w", err)
	}
	s.refreshPages(o.tenantID, pages)
	return nil
}

// autoResolveCandidatesSQL selects unresolved incidents that an automatic
// publication still shows and whose linked alerts are all resolved.
const autoResolveCandidatesSQL = `
	SELECT DISTINCT i.id, i.tenant_id
	FROM incidents i
	JOIN incident_status_page_publications isp
		ON isp.incident_id = i.id AND isp.tenant_id = i.tenant_id AND isp.auto_published AND isp.unpublished_at IS NULL
	WHERE i.state <> 'resolved'
	  AND EXISTS (SELECT 1 FROM incident_alerts ia WHERE ia.incident_id = i.id)
	  AND NOT EXISTS (SELECT 1 FROM incident_alerts ia JOIN alerts al ON al.id = ia.alert_id
		WHERE ia.incident_id = i.id AND al.status IN ('active', 'acknowledged'))
	  AND ($1::uuid IS NULL OR i.id = $1::uuid)
	LIMIT $2`

// autoResolve resolves automatically published incidents whose outage
// ended. Alerts resolve only on a confirmed recovery, so this waits for it.
func (s *Service) autoResolve(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, autoResolveCandidatesSQL, nil, maxOutagesPerTick)
	if err != nil {
		return fmt.Errorf("find recovered published outages: %w", err)
	}
	type candidate struct{ id, tenantID uuid.UUID }
	var due []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.tenantID); err != nil {
			rows.Close()
			return err
		}
		due = append(due, c)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, c := range due {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.resolveRecovered(ctx, c.id, c.tenantID); err != nil && s.log != nil {
			s.log.WithError(err).WithField("incident_id", c.id.String()).Error("Failed to resolve a recovered published outage")
		}
	}
	return nil
}

func (s *Service) resolveRecovered(ctx context.Context, incidentID, tenantID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state::text FROM incidents WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, incidentID, tenantID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || state == "resolved" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock incident: %w", err)
	}
	var still uuid.UUID
	err = tx.QueryRowContext(ctx, autoResolveCandidatesSQL, incidentID, 1).Scan(&still, new(uuid.UUID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil // an alert reopened, or an operator took the publication over
	}
	if err != nil {
		return fmt.Errorf("recheck recovered outage: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE incidents SET state = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1 AND tenant_id = $2
	`, incidentID, tenantID); err != nil {
		return fmt.Errorf("resolve incident: %w", err)
	}
	if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, tenantID, incidentID, incidentrecovery.SystemEntryType,
		"Incident resolved automatically: its monitors recovered", nil); err != nil {
		return err
	}
	if err := pagenotify.IncidentResolvedTx(ctx, tx, tenantID, incidentID); err != nil {
		return err
	}
	var pages []uuid.UUID
	pageRows, err := tx.QueryContext(ctx, `SELECT status_page_id FROM incident_status_page_publications
		WHERE incident_id = $1 AND tenant_id = $2 AND unpublished_at IS NULL`, incidentID, tenantID)
	if err != nil {
		return fmt.Errorf("list incident pages: %w", err)
	}
	for pageRows.Next() {
		var id uuid.UUID
		if err := pageRows.Scan(&id); err != nil {
			pageRows.Close()
			return err
		}
		pages = append(pages, id)
	}
	if err := pageRows.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident resolution: %w", err)
	}
	s.refreshPages(tenantID, pages)
	return nil
}
