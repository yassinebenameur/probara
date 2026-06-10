package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// spyStatusPublisher records published events for assertions.
type spyStatusPublisher struct {
	events []statusupdates.Event
}

func (s *spyStatusPublisher) Publish(event statusupdates.Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestPersistResultPublishesOnlyOnStateTransitions(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "worker-publish")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	// monitor starts in 'unknown'; threshold = 2 (column default, migration 000044)

	spy := &spyStatusPublisher{}
	w := &Worker{db: dbClient, status: spy}

	persist := func(status string) {
		t.Helper()
		job := &models.Job{ID: uuid.New().String(), TenantID: tenantID.String()}
		payload := &models.CheckJobPayload{MonitorID: monitorID.String()}
		if err := w.persistResult(ctx, job, payload, &CheckResult{Status: status}, time.Now()); err != nil {
			t.Fatalf("persist %s: %v", status, err)
		}
	}

	assertEvents := func(wantCount int) {
		t.Helper()
		if len(spy.events) != wantCount {
			t.Fatalf("published events = %d, want %d (%+v)", len(spy.events), wantCount, spy.events)
		}
	}

	persist("success") // unknown -> up: transition
	assertEvents(1)
	persist("success") // steady up: no publish
	assertEvents(1)
	persist("failure") // up -> suspect (below threshold, still a transition)
	assertEvents(2)
	persist("failure") // suspect -> down: threshold crossed
	assertEvents(3)
	persist("failure") // steady down: no publish
	assertEvents(3)
	persist("success") // down -> up: recovery
	assertEvents(4)

	for i, event := range spy.events {
		if event.Type != "state_change" {
			t.Fatalf("event[%d].Type = %q, want %q", i, event.Type, "state_change")
		}
		if event.MonitorID != monitorID.String() || event.TenantID != tenantID.String() {
			t.Fatalf("event[%d] ids = %s/%s, want %s/%s",
				i, event.MonitorID, event.TenantID, monitorID, tenantID)
		}
	}
}

func TestPersistExpiredJobDoesNotPublish(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "worker-expired")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")

	spy := &spyStatusPublisher{}
	w := &Worker{db: dbClient, status: spy}

	payload, err := json.Marshal(models.CheckJobPayload{MonitorID: monitorID.String()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	job := &models.Job{ID: uuid.New().String(), TenantID: tenantID.String(), Payload: payload}

	if err := w.persistExpiredJob(ctx, job); err != nil {
		t.Fatalf("persistExpiredJob: %v", err)
	}

	var count int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND result_source = 'platform'`,
		monitorID).Scan(&count); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if count != 1 {
		t.Fatalf("platform check_results = %d, want 1", count)
	}
	if len(spy.events) != 0 {
		t.Fatalf("published events = %d, want 0 (%+v)", len(spy.events), spy.events)
	}
}
