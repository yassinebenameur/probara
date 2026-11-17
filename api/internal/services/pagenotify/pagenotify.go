// Package pagenotify writes the status-page notification outbox
// (status_page_notification_outbox, migration 000122): one row per page and
// event that the page's email subscribers and push subscribers hear about.
//
// The outbox is the single trigger for subscriber mail and incident push.
// Every writer calls these functions inside the transaction of the change
// it announces, so an event is never announced without the change, nor the
// change committed without its announcement:
//
//   - IncidentPublishedTx: an incident published to a page, or published
//     again after it was taken down (the incidents service and automatic
//     publishing);
//   - IncidentUpdatedTx: a public update posted on an incident;
//   - IncidentResolvedTx: an incident resolved;
//   - MaintenanceScheduledTx: a maintenance window created, or its schedule
//     or targets changed, while it still has an occurrence ahead.
//
// maintenance_started rows come from the API runner's sweep
// (pagesubscribers), which reads only the announced one-off windows. The
// runner plans every row for mail; the status-page push reconciler reads the
// incident rows.
package pagenotify

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/statuspagesql"
)

// Event kinds (the outbox's event_kind CHECK).
const (
	KindIncidentPublished    = "incident_published"
	KindIncidentUpdated      = "incident_updated"
	KindIncidentResolved     = "incident_resolved"
	KindMaintenanceScheduled = "maintenance_scheduled"
	KindMaintenanceStarted   = "maintenance_started"
)

// scheduledLead is how far ahead an occurrence must start to be announced
// as scheduled (MaintenanceScheduledTx).
const scheduledLead = "1 minute"

// Execer is the part of *sql.Tx the writers need.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// IncidentPublishedTx records that incidentID was just published on
// pageID. Each publication is its own event: taking an incident down and
// publishing it again announces it again.
func IncidentPublishedTx(ctx context.Context, tx Execer, tenantID, incidentID, pageID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, incident_id, dedupe_key)
		SELECT $1, sp.id, $4, $3, $5
		FROM status_pages sp
		WHERE sp.id = $2 AND sp.tenant_id = $1
		ON CONFLICT DO NOTHING
	`, tenantID, pageID, incidentID, KindIncidentPublished, uuid.New().String()); err != nil {
		return fmt.Errorf("enqueue incident publication notice: %w", err)
	}
	return nil
}

// IncidentUpdatedTx records the incident's newest public update on every
// page it is published on. Call it after inserting the update, in the same
// transaction.
func IncidentUpdatedTx(ctx context.Context, tx Execer, tenantID, incidentID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_notification_outbox
			(tenant_id, status_page_id, event_kind, incident_id, timeline_entry_id, dedupe_key)
		SELECT isp.tenant_id, isp.status_page_id, $3, isp.incident_id, te.id, te.id::text
		FROM incident_status_page_publications isp
		CROSS JOIN LATERAL (
			SELECT ite.id
			FROM incident_timeline_entries ite
			WHERE ite.incident_id = isp.incident_id AND ite.tenant_id = isp.tenant_id
			  AND ite.entry_type = 'public_update'
			ORDER BY ite.created_at DESC, ite.id DESC
			LIMIT 1
		) te
		WHERE isp.tenant_id = $1 AND isp.incident_id = $2 AND isp.unpublished_at IS NULL
		ON CONFLICT DO NOTHING
	`, tenantID, incidentID, KindIncidentUpdated); err != nil {
		return fmt.Errorf("enqueue incident update notice: %w", err)
	}
	return nil
}

// IncidentResolvedTx records the incident's resolution on every page it is
// published on. A resolved incident cannot reopen, so the incident is the
// event's identity.
func IncidentResolvedTx(ctx context.Context, tx Execer, tenantID, incidentID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, incident_id, dedupe_key)
		SELECT isp.tenant_id, isp.status_page_id, $3, isp.incident_id, isp.incident_id::text
		FROM incident_status_page_publications isp
		WHERE isp.tenant_id = $1 AND isp.incident_id = $2 AND isp.unpublished_at IS NULL
		ON CONFLICT DO NOTHING
	`, tenantID, incidentID, KindIncidentResolved); err != nil {
		return fmt.Errorf("enqueue incident resolution notice: %w", err)
	}
	return nil
}

// MaintenanceScheduledTx records the window as scheduled on every page that
// renders a monitor one of its occurrences still ahead covers (directly or
// through a targeted group, per the shared occurrence fragment). Call it
// after the window's occurrences and targets are written, in the same
// transaction. A window that only starts now (a snooze) has no occurrence
// ahead and announces nothing: "ahead" means starting at least
// scheduledLead from now, which also absorbs clock skew between the API
// (which stamps a snooze's start) and the database. A recurring window is
// announced here once, never per occurrence.
func MaintenanceScheduledTx(ctx context.Context, tx Execer, tenantID, windowID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, maintenance_window_id, dedupe_key)
		SELECT DISTINCT sp.tenant_id, sp.id, $3, $2::uuid, $4
		FROM `+statuspagesql.RenderedMonitorsSQL+` rm
		JOIN status_pages sp ON sp.id = rm.status_page_id AND sp.tenant_id = $1
		JOIN monitors m ON m.id = rm.monitor_id AND m.tenant_id = $1 AND m.deleted_at IS NULL
		WHERE EXISTS (
			SELECT 1 FROM `+maintenance.OccurrenceRangesSQL("$1::uuid", "m.id", "NOW()", "'infinity'::timestamptz")+` mo
			WHERE mo.window_id = $2::uuid AND mo.occurrence_starts_at > NOW() + INTERVAL '`+scheduledLead+`'
		)
		ON CONFLICT DO NOTHING
	`, tenantID, windowID, KindMaintenanceScheduled, uuid.New().String()); err != nil {
		return fmt.Errorf("enqueue maintenance notice: %w", err)
	}
	return nil
}
