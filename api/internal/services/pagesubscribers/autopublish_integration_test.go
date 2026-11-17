//go:build !unit

package pagesubscribers

// Automatic publication of monitor outages against Postgres: the delay, the
// page opt-in, maintenance, dependency suppression and recovery
// confirmation, resolution on recovery, and no duplicates with manual
// incidents, with an earlier operator decision or across replicas.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	"github.com/yassinebenameur/probara/api/internal/services/pagenotify"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// outageFor marks the fixture's monitor down with an availability alert
// that opened ago.
func (f *fixture) outageFor(t *testing.T, ago time.Duration) uuid.UUID {
	t.Helper()
	alertID := uuid.New()
	_, err := f.db.ExecContext(f.ctx, `UPDATE monitors SET current_state = 'down', consecutive_successes = 0 WHERE id = $1`, f.monitorID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count)
		VALUES ($1, $2, $3, 'availability', 'active', NOW() - make_interval(secs => $4), 3)
	`, alertID, f.tenantID, f.monitorID, ago.Seconds())
	require.NoError(t, err)
	return alertID
}

func (f *fixture) recover(t *testing.T, alertID uuid.UUID) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `UPDATE monitors SET current_state = 'up' WHERE id = $1`, f.monitorID)
	require.NoError(t, err)
}

type publication struct {
	incidentID uuid.UUID
	auto, live bool
	state      string
}

func (f *fixture) publications(t *testing.T) []publication {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx, `
		SELECT isp.incident_id, isp.auto_published, isp.unpublished_at IS NULL, i.state::text
		FROM incident_status_page_publications isp JOIN incidents i ON i.id = isp.incident_id
		WHERE isp.status_page_id = $1 ORDER BY isp.created_at`, f.pageID)
	require.NoError(t, err)
	defer rows.Close()
	var out []publication
	for rows.Next() {
		var p publication
		require.NoError(t, rows.Scan(&p.incidentID, &p.auto, &p.live, &p.state))
		out = append(out, p)
	}
	return out
}

func TestAutoPublish_DelayOptInAndResolve(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "ops@example.com")
	alertID := f.outageFor(t, 4*time.Minute)

	f.setting(t, "auto_publish_incidents", true)
	run(t, s)
	require.Empty(t, f.publications(t), "published before the delay")

	_, err := f.db.ExecContext(f.ctx, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, alertID)
	require.NoError(t, err)
	f.setting(t, "auto_publish_incidents", false)
	run(t, s)
	require.Empty(t, f.publications(t), "published without the page's opt-in")

	f.setting(t, "auto_publish_incidents", true)
	run(t, s)
	run(t, s)
	pubs := f.publications(t)
	require.Len(t, pubs, 1)
	require.True(t, pubs[0].auto && pubs[0].live)
	require.Equal(t, "investigating", pubs[0].state)
	var title string
	var linked bool
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT title, EXISTS (SELECT 1 FROM incident_alerts WHERE incident_id = $1 AND alert_id = $2)
		FROM incidents WHERE id = $1`, pubs[0].incidentID, alertID).Scan(&title, &linked))
	require.Equal(t, autoIncidentTitle, title, "public wording, no internal monitor name")
	require.True(t, linked)
	notices := mailer.to("ops@example.com", "Incident: Service disruption")
	require.Len(t, notices, 1)
	require.Contains(t, notices[0].Text, "Affected: Checkout")

	// Still down: nothing more. Recovered: resolved and announced once.
	run(t, s)
	require.Len(t, f.publications(t), 1)
	f.recover(t, alertID)
	run(t, s)
	run(t, s)
	pubs = f.publications(t)
	require.Equal(t, "resolved", pubs[0].state)
	require.Equal(t, 1, f.outbox(t, pagenotify.KindIncidentResolved))
	require.Len(t, mailer.to("ops@example.com", "Resolved: Service disruption"), 1)
}

// Maintenance, a dependency that explains the outage, and a monitor
// already passing again (recovery confirmation) all hold publication.
func TestAutoPublish_RespectsMaintenanceSuppressionAndRecovery(t *testing.T) {
	f := setup(t)
	s := f.service(&fakeMailer{}, nil)
	f.setting(t, "auto_publish_incidents", true)
	alertID := f.outageFor(t, 10*time.Minute)

	// Recovering: passing checks count toward confirming a recovery.
	_, err := f.db.ExecContext(f.ctx, `UPDATE monitors SET consecutive_successes = 1 WHERE id = $1`, f.monitorID)
	require.NoError(t, err)
	run(t, s)
	require.Empty(t, f.publications(t), "published while recovering")
	_, err = f.db.ExecContext(f.ctx, `UPDATE monitors SET consecutive_successes = 0 WHERE id = $1`, f.monitorID)
	require.NoError(t, err)

	// Explained by a dependency.
	root := testutil.InsertHTTPMonitor(f.ctx, t, f.db, f.tenantID, "core-network")
	_, err = f.db.ExecContext(f.ctx, `UPDATE tenants SET dependency_suppression_enabled = TRUE WHERE id = $1`, f.tenantID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `UPDATE alerts SET root_cause_monitor_id = $2 WHERE id = $1`, alertID, root)
	require.NoError(t, err)
	run(t, s)
	require.Empty(t, f.publications(t), "published an outage a dependency explains")
	_, err = f.db.ExecContext(f.ctx, `UPDATE alerts SET root_cause_monitor_id = NULL WHERE id = $1`, alertID)
	require.NoError(t, err)

	// In maintenance.
	_, err = maintenancewindows.NewService(f.db).Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Planned work", StartsAt: time.Now().Add(-time.Minute), EndsAt: time.Now().Add(time.Hour),
		MonitorIDs: []string{f.monitorID.String()},
	})
	require.NoError(t, err)
	run(t, s)
	require.Empty(t, f.publications(t), "published during maintenance")
	_, err = f.db.ExecContext(f.ctx, `DELETE FROM maintenance_window_occurrences`)
	require.NoError(t, err)

	run(t, s)
	require.Len(t, f.publications(t), 1)
}

// An operator's own unresolved incident on the page already says it; an
// automatic publication the operator took down or resolved is not put back
// for the same outage; two replicas publish once.
func TestAutoPublish_NoDuplicates(t *testing.T) {
	f := setup(t)
	s := f.service(&fakeMailer{}, nil)
	f.setting(t, "auto_publish_incidents", true)
	manual := f.publish(t, "We know")
	alertID := f.outageFor(t, 10*time.Minute)
	run(t, s)
	require.Len(t, f.publications(t), 1, "a manual incident about the monitor is already on the page")

	_, err := f.incidents.TransitionIncidentState(f.ctx, f.tenantID, manual, &models.TransitionIncidentStateRequest{State: models.IncidentStateResolved})
	require.NoError(t, err)
	a, b := f.service(&fakeMailer{}, nil), f.service(&fakeMailer{}, nil)
	var wg sync.WaitGroup
	for _, r := range []*Service{a, b, a, b} {
		wg.Add(1)
		go func(r *Service) {
			defer wg.Done()
			require.NoError(t, r.RunOnce(context.Background()))
		}(r)
	}
	wg.Wait()
	var autos []publication
	for _, p := range f.publications(t) {
		if p.auto {
			autos = append(autos, p)
		}
	}
	require.Len(t, autos, 1, "two replicas published twice")
	var created int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM incidents WHERE is_auto_created`).Scan(&created))
	require.Equal(t, 1, created)

	// The operator takes it down: not put back while the outage lasts.
	_, err = f.incidents.UnpublishIncidentFromStatusPage(f.ctx, f.tenantID, autos[0].incidentID, f.pageID)
	require.NoError(t, err)
	run(t, s)
	for _, p := range f.publications(t) {
		require.False(t, p.auto && p.live, "republished after an operator took it down")
	}

	// A new outage (a relapse reopens the alert after that publication) is
	// published again.
	_, err = f.db.ExecContext(f.ctx, `UPDATE incident_status_page_publications SET published_at = NOW() - INTERVAL '20 minutes' WHERE incident_id = $1`, autos[0].incidentID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `UPDATE alerts SET reopened_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, alertID)
	require.NoError(t, err)
	_, err = f.incidents.TransitionIncidentState(f.ctx, f.tenantID, autos[0].incidentID, &models.TransitionIncidentStateRequest{State: models.IncidentStateResolved})
	require.NoError(t, err)
	run(t, s)
	live := 0
	for _, p := range f.publications(t) {
		if p.auto && p.live {
			live++
		}
	}
	require.Equal(t, 1, live)
}

// The alerter's automatic incident for the monitor (create incident on
// fire) is the one published: never a second incident for one monitor.
func TestAutoPublish_ReusesTheAlertersIncident(t *testing.T) {
	f := setup(t)
	s := f.service(&fakeMailer{}, nil)
	f.setting(t, "auto_publish_incidents", true)
	alertID := f.outageFor(t, 10*time.Minute)
	existing := uuid.New()
	_, err := f.db.ExecContext(f.ctx, `
		INSERT INTO incidents (id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, created_at, updated_at)
		VALUES ($1, $2, 'checkout-internal incident', 'Automatically created from a firing alert.', 'investigating', TRUE, $3, NOW(), NOW())
	`, existing, f.tenantID, f.monitorID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `INSERT INTO incident_alerts (incident_id, alert_id) VALUES ($1, $2)`, existing, alertID)
	require.NoError(t, err)
	run(t, s)
	pubs := f.publications(t)
	require.Len(t, pubs, 1)
	require.Equal(t, existing, pubs[0].incidentID)
}
