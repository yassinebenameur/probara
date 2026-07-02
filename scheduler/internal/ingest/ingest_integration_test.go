package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
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

func newTestIngest(t testing.TB, dbClient *db.Client, spy statusPublisher) *Ingest {
	t.Helper()
	cfg := &config.SchedulerConfig{
		CheckResultStream:        models.CheckResultStream,
		CheckResultSubject:       models.CheckResultSubject,
		ResultIngestConsumerName: "result-ingest-test",
		ResultIngestConcurrency:  1,
	}
	i := New(cfg, logger.New("ingest-test", "error"), metrics.NewRegistry("ingest_test"), dbClient, nil, nil)
	i.status = spy
	return i
}

func monitorMessage(tenantID, monitorID uuid.UUID, status string) models.CheckResultMessage {
	now := time.Now()
	return models.CheckResultMessage{
		Version:      "v1",
		JobID:        uuid.New().String(),
		MonitorID:    monitorID.String(),
		TenantID:     tenantID.String(),
		Status:       status,
		ResultSource: string(models.ResultSourceMonitor),
		StartedAt:    now,
		CompletedAt:  now,
	}
}

func ingestMessage(t *testing.T, i *Ingest, m models.CheckResultMessage) {
	t.Helper()
	ids, err := i.buildResult(m)
	if err != nil {
		t.Fatalf("buildResult: %v", err)
	}
	if _, err := i.persist(context.Background(), m, ids); err != nil {
		t.Fatalf("persist %s: %v", m.Status, err)
	}
}

func TestIngestAdvancesMonitorState(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ingest")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	// threshold = 2 is the column default from migration 000044

	i := newTestIngest(t, dbClient, nil)

	assertState := func(wantState string, wantFails int) {
		t.Helper()
		var state string
		var fails int
		if err := dbClient.QueryRowContext(ctx,
			`SELECT current_state, consecutive_failures FROM monitors WHERE id = $1`,
			monitorID).Scan(&state, &fails); err != nil {
			t.Fatalf("query state: %v", err)
		}
		if state != wantState || fails != wantFails {
			t.Fatalf("state = %s/%d, want %s/%d", state, fails, wantState, wantFails)
		}
	}

	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // 1st failure -> suspect
	assertState("suspect", 1)
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // 2nd consecutive -> down
	assertState("down", 2)
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // stays down
	assertState("down", 3)
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "success")) // recovery
	assertState("up", 0)
}

func TestIngestPublishesOnlyOnStateTransitions(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ingest-publish")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	// monitor starts in 'unknown'; threshold = 2 (column default, migration 000044)

	spy := &spyStatusPublisher{}
	i := newTestIngest(t, dbClient, spy)

	handle := func(status string) {
		t.Helper()
		m := monitorMessage(tenantID, monitorID, status)
		data := mustMarshal(t, m)
		if err := i.handleMessage(ctx, testMessage(data)); err != nil {
			t.Fatalf("handleMessage %s: %v", status, err)
		}
	}

	assertEvents := func(wantCount int) {
		t.Helper()
		if len(spy.events) != wantCount {
			t.Fatalf("published events = %d, want %d (%+v)", len(spy.events), wantCount, spy.events)
		}
	}

	handle("success") // unknown -> up: transition
	assertEvents(1)
	handle("success") // steady up: no publish
	assertEvents(1)
	handle("failure") // up -> suspect (below threshold, still a transition)
	assertEvents(2)
	handle("failure") // suspect -> down: threshold crossed
	assertEvents(3)
	handle("failure") // steady down: no publish
	assertEvents(3)
	handle("success") // down -> up: recovery
	assertEvents(4)

	for n, event := range spy.events {
		if event.Type != "state_change" {
			t.Fatalf("event[%d].Type = %q, want %q", n, event.Type, "state_change")
		}
		if event.MonitorID != monitorID.String() || event.TenantID != tenantID.String() {
			t.Fatalf("event[%d] ids = %s/%s, want %s/%s",
				n, event.MonitorID, event.TenantID, monitorID, tenantID)
		}
	}
}

func TestIngestIsIdempotentOnRedelivery(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ingest-dedupe")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")

	i := newTestIngest(t, dbClient, nil)

	m := monitorMessage(tenantID, monitorID, "failure")
	data := mustMarshal(t, m)

	// Same message delivered twice: one row, one state transition.
	for n := 0; n < 2; n++ {
		if err := i.handleMessage(ctx, testMessage(data)); err != nil {
			t.Fatalf("handleMessage delivery %d: %v", n+1, err)
		}
	}

	var count int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`,
		monitorID).Scan(&count); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if count != 1 {
		t.Fatalf("check_results = %d, want 1", count)
	}

	var fails int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT consecutive_failures FROM monitors WHERE id = $1`,
		monitorID).Scan(&fails); err != nil {
		t.Fatalf("query failures: %v", err)
	}
	if fails != 1 {
		t.Fatalf("consecutive_failures = %d, want 1 (redelivery double-applied)", fails)
	}
}

func TestIngestExpiredJobInsertsWithoutState(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ingest-expired")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")

	spy := &spyStatusPublisher{}
	i := newTestIngest(t, dbClient, spy)

	errMsg := "Job expired before processing"
	m := monitorMessage(tenantID, monitorID, string(models.ResultStatusError))
	m.ResultSource = string(models.ResultSourcePlatform)
	m.ErrorMessage = &errMsg

	if err := i.handleMessage(ctx, testMessage(mustMarshal(t, m))); err != nil {
		t.Fatalf("handleMessage: %v", err)
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

	var state string
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state FROM monitors WHERE id = $1`,
		monitorID).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("current_state = %q, want unknown (platform results must not run the state machine)", state)
	}
	if len(spy.events) != 0 {
		t.Fatalf("published events = %d, want 0 (%+v)", len(spy.events), spy.events)
	}
}

func TestIngestDropsResultForMissingMonitor(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ingest-gone")
	i := newTestIngest(t, dbClient, nil)

	// Monitor never existed (purged while the job was in flight): ack, no error.
	m := monitorMessage(tenantID, uuid.New(), "failure")
	if err := i.handleMessage(ctx, testMessage(mustMarshal(t, m))); err != nil {
		t.Fatalf("handleMessage should drop results for missing monitors, got: %v", err)
	}
}
