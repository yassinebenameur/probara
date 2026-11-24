//go:build !unit

package statuspage

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
)

// insertOutbox writes what the API writes when an incident is published,
// updated or resolved on the fixture's page.
func (f *pushFixture) insertOutbox(ctx context.Context, t *testing.T, kind string, incidentID uuid.UUID, published bool) {
	t.Helper()
	if published {
		if _, err := f.store.db.ExecContext(ctx, `
			INSERT INTO incident_status_page_publications (incident_id, status_page_id, tenant_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, incidentID, f.pageID, f.tenantID); err != nil {
			t.Fatalf("publish incident: %v", err)
		}
	}
	if _, err := f.store.db.ExecContext(ctx, `
		INSERT INTO status_page_notification_outbox (tenant_id, status_page_id, event_kind, incident_id, dedupe_key)
		VALUES ($1, $2, $3, $4, $5)`, f.tenantID, f.pageID, kind, incidentID, uuid.NewString()); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}
}

func (f *pushFixture) newIncident(ctx context.Context, t *testing.T, title string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.store.db.ExecContext(ctx, `INSERT INTO incidents (id, tenant_id, title, summary, state) VALUES ($1, $2, $3, '', 'investigating')`,
		id, f.tenantID, title); err != nil {
		t.Fatalf("insert incident: %v", err)
	}
	return id
}

// Incident publications, public updates and resolutions push to the page's
// subscribers from the notification outbox, once each across replicas and
// passes, carrying the incident's title and one notification slot per
// incident; monitor transitions are untouched.
func TestReconcileOutboxNotifications(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	incident := f.newIncident(ctx, t, "Checkout is failing")
	f.insertOutbox(ctx, t, "incident_published", incident, true)
	f.insertOutbox(ctx, t, "incident_updated", incident, true)
	// An incident taken down from the page in the meantime pushes nothing.
	gone := f.newIncident(ctx, t, "Withdrawn")
	f.insertOutbox(ctx, t, "incident_published", gone, false)

	for i := 0; i < 3; i++ {
		if _, err := f.store.reconcileOutboxNotifications(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.deliveries(ctx, t); len(got) != 2 || got[0] != "incident_published" || got[1] != "incident_updated" {
		t.Fatalf("deliveries = %v", got)
	}

	claimed, err := f.store.ClaimDeliveries(ctx, 10, pushClaimStaleAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d", len(claimed))
	}
	p := &pushSender{cfg: &config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com"}}
	for _, d := range claimed {
		if d.IncidentID != incident || d.MonitorID != uuid.Nil || d.PageTitle != "Acme Status" {
			t.Fatalf("claimed %+v", d)
		}
		payload := p.payloadFor(d)
		if payload.Tag != "i:"+incident.String() || payload.Title != "Acme Status" || payload.URL != "https://status.example.com/public/status/status" {
			t.Fatalf("payload = %+v", payload)
		}
		if d.Kind == "incident_published" && payload.Body != "Incident: Checkout is failing" {
			t.Fatalf("body = %q", payload.Body)
		}
	}

	// Push turned off, or the page gated: nothing new.
	if _, err := f.store.db.ExecContext(ctx, `UPDATE status_pages SET visibility = 'private' WHERE id = $1`, f.pageID); err != nil {
		t.Fatal(err)
	}
	f.insertOutbox(ctx, t, "incident_resolved", incident, true)
	if _, err := f.store.reconcileOutboxNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.deliveries(ctx, t); len(got) != 2 {
		t.Fatalf("a private page got an incident push: %v", got)
	}
}
