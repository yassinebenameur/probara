package airca

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type publishedJob struct {
	subject string
	job     TriageJob
	headers map[string][]string
}

// recordingPublisher stands in for queue.Client.
type recordingPublisher struct {
	mu    sync.Mutex
	jobs  []publishedJob
	fail  error
	calls int
}

func (p *recordingPublisher) Publish(_ context.Context, subject string, data []byte, headers map[string][]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail != nil {
		return p.fail
	}
	var job TriageJob
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	p.jobs = append(p.jobs, publishedJob{subject: subject, job: job, headers: headers})
	return nil
}

func (p *recordingPublisher) published() []publishedJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]publishedJob{}, p.jobs...)
}

type relayFixture struct {
	t   *testing.T
	ctx context.Context
	dbc *db.Client
	cfg *config.WorkerConfig
	pub *recordingPublisher
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	return &relayFixture{t: t, ctx: ctx, dbc: dbc, pub: &recordingPublisher{}, cfg: &config.WorkerConfig{
		AITriageEnabled: true, AITriageStream: "AI_TRIAGE", AITriageSubject: "ai.triage.jobs", AITriageConsumerName: "ai-triage-workers",
		LocalAI: ai.Config{Timeout: time.Second},
	}}
}

func (f *relayFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.dbc.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *relayFixture) relay() *Relay {
	r := NewRelay(f.cfg, logger.New("test", "error"), f.dbc, nil)
	r.pub = f.pub
	return r
}

func (f *relayFixture) tick(r *Relay) TickResult {
	f.t.Helper()
	res, err := r.Tick(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// tenant creates a workspace with the given AI settings.
func (f *relayFixture) tenant(enabled, auto bool, delay, maxPerHour int) uuid.UUID {
	f.t.Helper()
	id := testutil.InsertTenant(f.ctx, f.t, f.dbc, "triage "+uuid.NewString()[:8])
	f.exec(`INSERT INTO ai_settings(tenant_id,enabled,auto_triage_enabled,triage_delay_seconds,triage_max_per_hour) VALUES($1,$2,$3,$4,$5)`,
		id, enabled, auto, delay, maxPerHour)
	return id
}

// openAlert creates a monitor with the given check interval and an open
// availability alert on it that triggered agoSeconds ago (database clock).
func (f *relayFixture) openAlert(tenant uuid.UUID, interval int, agoSeconds float64) (alert, monitor uuid.UUID) {
	f.t.Helper()
	monitor = testutil.InsertHTTPMonitor(f.ctx, f.t, f.dbc, tenant, "m-"+uuid.NewString()[:8])
	f.exec(`UPDATE monitors SET interval_seconds=$2, timeout_seconds=LEAST(timeout_seconds, $2-1) WHERE id=$1`, monitor, interval)
	alert = uuid.New()
	f.exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,failure_count) VALUES($1,$2,$3,NOW()-make_interval(secs=>$4),1)`,
		alert, tenant, monitor, agoSeconds)
	return alert, monitor
}

func (f *relayFixture) triageCount(alert uuid.UUID) int {
	f.t.Helper()
	var n int
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM alert_triage WHERE alert_id=$1`, alert).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *relayFixture) triageID(alert uuid.UUID) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT id FROM alert_triage WHERE alert_id=$1`, alert).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// pendingRow inserts a queued (never delivered) triage row directly for
// republish/fail tests.
func (f *relayFixture) pendingRow(tenant uuid.UUID, createdAgo, enqueuedAgo time.Duration, attempts int) uuid.UUID {
	f.t.Helper()
	alert, monitor := f.openAlert(tenant, 30, 7200)
	id := uuid.New()
	f.exec(`INSERT INTO alert_triage(id,tenant_id,alert_id,monitor_id,status,created_at,enqueued_at,attempts)
		VALUES($1,$2,$3,$4,'pending',NOW()-make_interval(secs=>$5),NOW()-make_interval(secs=>$6),$7)`,
		id, tenant, alert, monitor, createdAgo.Seconds(), enqueuedAgo.Seconds(), attempts)
	return id
}

// startedRow is pendingRow whose first delivery happened startedAgo.
func (f *relayFixture) startedRow(tenant uuid.UUID, createdAgo, enqueuedAgo, startedAgo time.Duration, attempts int) uuid.UUID {
	f.t.Helper()
	id := f.pendingRow(tenant, createdAgo, enqueuedAgo, attempts)
	f.exec(`UPDATE alert_triage SET started_at=NOW()-make_interval(secs=>$2) WHERE id=$1`, id, startedAgo.Seconds())
	return id
}

func (f *relayFixture) attempts(id uuid.UUID) int {
	f.t.Helper()
	var n int
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT attempts FROM alert_triage WHERE id=$1`, id).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestRelayWaitsForDelayAndTwoCheckIntervals(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 60, 60)
	fresh, _ := f.openAlert(tenant, 30, 5)
	ready, _ := f.openAlert(tenant, 30, 61)
	slowMonitor, _ := f.openAlert(tenant, 60, 90) // waits 2x60s
	resolved, _ := f.openAlert(tenant, 30, 120)
	f.exec(`UPDATE alerts SET status='resolved',resolved_at=NOW() WHERE id=$1`, resolved)
	old, _ := f.openAlert(tenant, 30, (7 * time.Hour).Seconds()) // outside the lookback

	res := f.tick(f.relay())
	if res.Claimed != 1 || res.Published != 1 || res.LockHeld {
		t.Fatalf("tick %+v", res)
	}
	for alert, want := range map[uuid.UUID]int{fresh: 0, ready: 1, slowMonitor: 0, resolved: 0, old: 0} {
		if got := f.triageCount(alert); got != want {
			t.Fatalf("alert %s: %d rows, want %d", alert, got, want)
		}
	}
	jobs := f.pub.published()
	if jobs[0].subject != "ai.triage.jobs" || jobs[0].job.AlertID != ready || jobs[0].job.TenantID != tenant ||
		jobs[0].job.V != TriageJobVersion || jobs[0].job.TriageID != f.triageID(ready) {
		t.Fatalf("published %+v", jobs[0])
	}
	if h := jobs[0].headers[queue.MsgIDHeader]; len(h) != 1 || h[0] != jobs[0].job.TriageID.String() {
		t.Fatalf("Nats-Msg-Id header %v", jobs[0].headers)
	}
	var status string
	var attempts int
	var monitor uuid.NullUUID
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT status,attempts,monitor_id FROM alert_triage WHERE alert_id=$1`, ready).Scan(&status, &attempts, &monitor); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 1 || !monitor.Valid {
		t.Fatalf("claimed row %s %d %v", status, attempts, monitor)
	}

	// Once the wait passes for the slow monitor it is claimed; the claimed
	// alert is never claimed twice. Acknowledged alerts are still open.
	f.exec(`UPDATE alerts SET triggered_at=NOW()-INTERVAL '121 seconds',status='acknowledged',acknowledged_at=NOW() WHERE id=$1`, slowMonitor)
	if res := f.tick(f.relay()); res.Claimed != 1 {
		t.Fatalf("second tick %+v", res)
	}
	if f.triageCount(slowMonitor) != 1 || f.triageCount(ready) != 1 {
		t.Fatal("acknowledged alert not claimed or claimed alert duplicated")
	}
}

func TestRelayHourlyCapExcludesSkipsAndClaimsOldestFirst(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 0, 3)
	var alerts []uuid.UUID
	for i := 0; i < 5; i++ {
		a, _ := f.openAlert(tenant, 30, float64(600-i*60)) // alerts[0] is the oldest
		alerts = append(alerts, a)
	}
	// A skipped triage this hour does not count against the cap.
	skippedAlert, _ := f.openAlert(tenant, 30, 3000)
	f.exec(`INSERT INTO alert_triage(tenant_id,alert_id,status,skip_reason,completed_at) VALUES($1,$2,'skipped','cooldown',NOW())`, tenant, skippedAlert)

	if res := f.tick(f.relay()); res.Claimed != 3 {
		t.Fatalf("cap 3: %+v", res)
	}
	for i, a := range alerts {
		want := 0
		if i < 3 {
			want = 1
		}
		if got := f.triageCount(a); got != want {
			t.Fatalf("alert %d: %d rows, want %d (oldest first)", i, got, want)
		}
	}
	if res := f.tick(f.relay()); res.Claimed != 0 {
		t.Fatalf("cap exhausted, next tick claimed %+v", res)
	}
	// A claim older than an hour frees room again.
	f.exec(`UPDATE alert_triage SET created_at=NOW()-INTERVAL '61 minutes' WHERE alert_id=$1`, alerts[0])
	if res := f.tick(f.relay()); res.Claimed != 1 {
		t.Fatalf("freed room: %+v", res)
	}
}

func TestRelayTenantSettingsAndMeshAlerts(t *testing.T) {
	f := newRelayFixture(t)
	on := f.tenant(true, true, 0, 60)
	autoOff := f.tenant(true, false, 0, 60)
	aiOff := f.tenant(false, true, 0, 60)
	noRow := testutil.InsertTenant(f.ctx, t, f.dbc, "no settings row")
	onAlert, _ := f.openAlert(on, 30, 120)
	offAlert, _ := f.openAlert(autoOff, 30, 120)
	aiOffAlert, _ := f.openAlert(aiOff, 30, 120)
	noRowAlert, _ := f.openAlert(noRow, 30, 120)

	var src, dst uuid.UUID
	if err := f.dbc.QueryRowContext(f.ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'paris','paris') RETURNING id`, on).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if err := f.dbc.QueryRowContext(f.ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'berlin','berlin') RETURNING id`, on).Scan(&dst); err != nil {
		t.Fatal(err)
	}
	mesh := uuid.New()
	f.exec(`INSERT INTO alerts(id,tenant_id,kind,source_location_id,target_location_id,triggered_at,failure_count) VALUES($1,$2,'mesh_edge',$3,$4,NOW()-INTERVAL '120 seconds',1)`, mesh, on, src, dst)
	// An alert whose monitor was deleted is not claimed.
	deleted, deletedMonitor := f.openAlert(on, 30, 120)
	f.exec(`UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, deletedMonitor)

	if res := f.tick(f.relay()); res.Claimed != 2 {
		t.Fatalf("tick %+v", res)
	}
	for alert, want := range map[uuid.UUID]int{onAlert: 1, mesh: 1, offAlert: 0, aiOffAlert: 0, noRowAlert: 0, deleted: 0} {
		if got := f.triageCount(alert); got != want {
			t.Fatalf("alert %s: %d rows, want %d", alert, got, want)
		}
	}
	var meshMonitor uuid.NullUUID
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT monitor_id FROM alert_triage WHERE alert_id=$1`, mesh).Scan(&meshMonitor); err != nil || meshMonitor.Valid {
		t.Fatalf("mesh triage monitor_id %v %v", meshMonitor, err)
	}
	for _, j := range f.pub.published() {
		if j.job.TenantID != on {
			t.Fatalf("job for tenant %s published", j.job.TenantID)
		}
	}
}

func TestRelayRepublishesStalePendingRowsAndFailsTimedOutOnes(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 0, 60)
	stale := f.pendingRow(tenant, 4*time.Minute, 4*time.Minute, 1)
	recent := f.pendingRow(tenant, 4*time.Minute, time.Minute, 1)
	exhausted := f.pendingRow(tenant, 10*time.Minute, 4*time.Minute, maxTriageAttempts)
	// A delivered row is never republished: its job is in the stream.
	inDelivery := f.startedRow(tenant, 10*time.Minute, 4*time.Minute, time.Minute, 1)
	timedOut := f.startedRow(tenant, 40*time.Minute, 4*time.Minute, 31*time.Minute, 1)
	// Queue wait does not count toward the run timeout...
	queued := f.pendingRow(tenant, 31*time.Minute, 4*time.Minute, maxTriageAttempts)
	// ...until the job can no longer be delivered.
	notStarted := f.pendingRow(tenant, triageNotStartedAfter+time.Minute, 4*time.Minute, maxTriageAttempts)

	res := f.tick(f.relay())
	if res.Republished != 1 || res.Failed != 2 || res.Claimed != 0 || res.Published != 1 {
		t.Fatalf("tick %+v", res)
	}
	jobs := f.pub.published()
	if jobs[0].job.TriageID != stale {
		t.Fatalf("republished %+v", jobs[0])
	}
	if h := jobs[0].headers[queue.MsgIDHeader]; len(h) != 1 || h[0] != stale.String() {
		t.Fatalf("republish header %v", jobs[0].headers)
	}
	row := func(id uuid.UUID) (status string, attempts int, message *string, completed bool) {
		t.Helper()
		if err := f.dbc.QueryRowContext(f.ctx, `SELECT status,attempts,error_message,completed_at IS NOT NULL FROM alert_triage WHERE id=$1`, id).
			Scan(&status, &attempts, &message, &completed); err != nil {
			t.Fatal(err)
		}
		return
	}
	for id, want := range map[uuid.UUID]int{stale: 2, recent: 1, exhausted: maxTriageAttempts, inDelivery: 1, queued: maxTriageAttempts} {
		if s, a, _, _ := row(id); s != "pending" || a != want {
			t.Fatalf("row %s: %s attempts %d, want pending %d", id, s, a, want)
		}
	}
	for id, wantMsg := range map[uuid.UUID]string{timedOut: triageTimeoutMessage, notStarted: triageNotStartedMessage} {
		s, _, msg, completed := row(id)
		if s != "failed" || msg == nil || *msg != wantMsg || !completed {
			t.Fatalf("failed row %s: %s %v %t, want %q", id, s, msg, completed, wantMsg)
		}
		var meta ai.Metadata
		var raw []byte
		if err := f.dbc.QueryRowContext(f.ctx, `SELECT metadata FROM alert_triage WHERE id=$1`, id).Scan(&raw); err != nil || json.Unmarshal(raw, &meta) != nil || meta.Outcome != "failed" {
			t.Fatalf("failed row metadata %s %v", raw, err)
		}
	}
	// The re-armed row is not republished again until the window passes.
	if res := f.tick(f.relay()); res.Republished != 0 {
		t.Fatalf("immediate re-republish %+v", res)
	}
}

func TestRelayKillSwitchStopsClaimsButKeepsDraining(t *testing.T) {
	for _, disable := range []func(*config.WorkerConfig){
		func(c *config.WorkerConfig) { c.AITriageEnabled = false },
		func(c *config.WorkerConfig) { c.LocalAI.Disabled = true },
	} {
		f := newRelayFixture(t)
		disable(f.cfg)
		tenant := f.tenant(true, true, 0, 60)
		eligible, _ := f.openAlert(tenant, 30, 120)
		f.pendingRow(tenant, 4*time.Minute, 4*time.Minute, 1)
		f.startedRow(tenant, 40*time.Minute, time.Minute, 31*time.Minute, 1)
		res := f.tick(f.relay())
		if res.Claimed != 0 || res.Republished != 1 || res.Failed != 1 {
			t.Fatalf("kill switch tick %+v", res)
		}
		if f.triageCount(eligible) != 0 {
			t.Fatal("kill switch still claimed")
		}
	}
}

// A NATS outage delays rows without spending their attempts: the tick stops
// at the first failed publish, gives every unpublished row its attempt back,
// and the republish delivers them once NATS is back.
func TestRelayPublishErrorReturnsAttemptsAndStops(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 0, 60)
	var alerts []uuid.UUID
	for i := 0; i < 3; i++ {
		a, _ := f.openAlert(tenant, 30, float64(120+i))
		alerts = append(alerts, a)
	}
	f.pub.fail = errors.New("nats down")
	r := f.relay()
	// Three outage-long republish windows: attempts never run out.
	for round := 0; round < maxTriageAttempts+1; round++ {
		res := f.tick(r)
		if res.PublishErrors != 3 || res.Published != 0 {
			t.Fatalf("round %d tick %+v", round, res)
		}
		for _, a := range alerts {
			id := f.triageID(a)
			var status string
			if err := f.dbc.QueryRowContext(f.ctx, `SELECT status FROM alert_triage WHERE id=$1`, id).Scan(&status); err != nil || status != "pending" {
				t.Fatalf("row %s %v", status, err)
			}
			if n := f.attempts(id); n != 0 {
				t.Fatalf("round %d: attempts %d after failed publishes, want 0", round, n)
			}
		}
		f.exec(`UPDATE alert_triage SET enqueued_at=NOW()-INTERVAL '4 minutes' WHERE tenant_id=$1`, tenant)
	}
	if calls := f.pub.calls; calls != maxTriageAttempts+1 {
		t.Fatalf("%d publish calls, want one per tick (stop at the first error)", calls)
	}

	f.pub.fail = nil
	if res := f.tick(r); res.Republished != 3 || res.Published != 3 || res.PublishErrors != 0 {
		t.Fatalf("recovery tick %+v", res)
	}
	got := map[uuid.UUID]bool{}
	for _, j := range f.pub.published() {
		got[j.job.AlertID] = true
	}
	for _, a := range alerts {
		if !got[a] {
			t.Fatalf("alert %s not published after recovery", a)
		}
		if n := f.attempts(f.triageID(a)); n != 1 {
			t.Fatalf("attempts after recovery %d", n)
		}
	}
}

// Monitors checking less often than every 30 minutes wait an hour, not two
// intervals that would outlast the lookback.
func TestRelayCapsTheIntervalWait(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 60, 60)
	daily, _ := f.openAlert(tenant, 86400, (triageMaxIntervalWait + time.Minute).Seconds())
	tooSoon, _ := f.openAlert(tenant, 86400, (triageMaxIntervalWait - time.Minute).Seconds())
	if res := f.tick(f.relay()); res.Claimed != 1 {
		t.Fatalf("tick %+v", res)
	}
	if f.triageCount(daily) != 1 || f.triageCount(tooSoon) != 0 {
		t.Fatal("interval wait not capped at triageMaxIntervalWait")
	}
}

// Claims stop while the platform backlog is full and resume as it drains.
func TestRelayBoundsThePendingBacklog(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 0, 60)
	f.pendingRow(tenant, time.Minute, time.Minute, 1)
	f.pendingRow(tenant, time.Minute, time.Minute, 1)
	var alerts []uuid.UUID
	for i := 0; i < 3; i++ {
		a, _ := f.openAlert(tenant, 30, float64(300-i))
		alerts = append(alerts, a)
	}
	r := f.relay()
	r.maxPending = 3
	if res := f.tick(r); res.Claimed != 1 {
		t.Fatalf("backlog 2/3: %+v", res)
	}
	if f.triageCount(alerts[0]) != 1 {
		t.Fatal("oldest alert not claimed first")
	}
	if res := f.tick(r); res.Claimed != 0 {
		t.Fatalf("backlog full: %+v", res)
	}
	f.exec(`UPDATE alert_triage SET status='skipped',skip_reason='cooldown',completed_at=NOW() WHERE alert_id=$1`, alerts[0])
	if res := f.tick(r); res.Claimed != 1 {
		t.Fatalf("backlog drained by one: %+v", res)
	}
}

// The tick's transaction carries server-side timeouts, so a tick stalled on a
// dead connection is aborted and frees the platform-wide relay lock.
func TestRelayTickSetsServerTimeouts(t *testing.T) {
	f := newRelayFixture(t)
	tx, err := f.dbc.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range relayTimeoutsSQL {
		if _, err := tx.ExecContext(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"statement_timeout", "idle_in_transaction_session_timeout"} {
		var v string
		if err := tx.QueryRowContext(f.ctx, `SELECT current_setting($1)`, name).Scan(&v); err != nil || v != relayStatementTimeout {
			t.Fatalf("%s = %q (%v), want %s", name, v, err, relayStatementTimeout)
		}
	}
}

func TestRelayTickSkipsWhileAnotherRelayHoldsTheLock(t *testing.T) {
	f := newRelayFixture(t)
	tenant := f.tenant(true, true, 0, 60)
	alert, _ := f.openAlert(tenant, 30, 120)
	tx, err := f.dbc.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var locked bool
	if err := tx.QueryRowContext(f.ctx, relayLockSQL).Scan(&locked); err != nil || !locked {
		t.Fatalf("lock %t %v", locked, err)
	}
	if res := f.tick(f.relay()); !res.LockHeld || res.Claimed != 0 {
		t.Fatalf("tick under held lock %+v", res)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if res := f.tick(f.relay()); res.LockHeld || res.Claimed != 1 || f.triageCount(alert) != 1 {
		t.Fatalf("tick after release %+v", res)
	}
}

// Two relays (replicas) ticking from several goroutines at once: one row per
// alert, one publish per triage, and the hourly cap holds exactly.
func TestRelayConcurrentTicksClaimOnceWithinCap(t *testing.T) {
	f := newRelayFixture(t)
	const capPerHour = 7
	tenant := f.tenant(true, true, 0, capPerHour)
	other := f.tenant(true, true, 0, 60)
	var alerts []uuid.UUID
	for i := 0; i < 12; i++ {
		a, _ := f.openAlert(tenant, 30, float64(120+i))
		alerts = append(alerts, a)
	}
	for i := 0; i < 5; i++ {
		a, _ := f.openAlert(other, 30, float64(120+i))
		alerts = append(alerts, a)
	}
	relays := []*Relay{f.relay(), f.relay()}
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	claimed, lockHeld := 0, 0
	var tickErr error
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(r *Relay) {
			defer wg.Done()
			<-start
			for i := 0; i < 15; i++ {
				res, err := r.Tick(f.ctx)
				mu.Lock()
				if err != nil && tickErr == nil {
					tickErr = err
				}
				claimed += res.Claimed
				if res.LockHeld {
					lockHeld++
				}
				mu.Unlock()
			}
		}(relays[g%2])
	}
	close(start)
	wg.Wait()
	if tickErr != nil {
		t.Fatal(tickErr)
	}
	if claimed != capPerHour+5 {
		t.Fatalf("claimed %d, want %d", claimed, capPerHour+5)
	}
	var rows, tenantRows int
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE tenant_id=$1) FROM alert_triage`, tenant).Scan(&rows, &tenantRows); err != nil {
		t.Fatal(err)
	}
	if rows != capPerHour+5 || tenantRows != capPerHour {
		t.Fatalf("rows %d (tenant %d)", rows, tenantRows)
	}
	for _, a := range alerts {
		if n := f.triageCount(a); n > 1 {
			t.Fatalf("alert %s has %d rows", a, n)
		}
	}
	seen := map[uuid.UUID]int{}
	for _, j := range f.pub.published() {
		seen[j.job.TriageID]++
	}
	if len(seen) != capPerHour+5 {
		t.Fatalf("published %d distinct triages", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("triage %s published %d times", id, n)
		}
	}
	t.Logf("lock contention observed on %d ticks", lockHeld)
}
