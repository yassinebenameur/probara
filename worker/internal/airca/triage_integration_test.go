package airca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// recordingAnalyzer returns a supported diagnosis with a decision snapshot
// and records every input it was given.
type recordingAnalyzer struct {
	mu     sync.Mutex
	inputs []ai.AnalysisInput
	err    error
	during func()
}

func (a *recordingAnalyzer) Analyze(_ context.Context, in ai.AnalysisInput) (ai.AnalysisResult, error) {
	a.mu.Lock()
	a.inputs = append(a.inputs, in)
	a.mu.Unlock()
	if a.during != nil {
		a.during()
	}
	if a.err != nil {
		return ai.AnalysisResult{}, a.err
	}
	snap := &ai.DecisionSnapshot{V: ai.DecisionSnapshotVersion, Catalogue: ai.CatalogueVersion, Hypothesis: "dns_resolution",
		Decisions: []ai.SnapshotDecision{{ID: uuid.NewString(), Task: "rca", Predicted: "dns_resolution", Final: "dns_resolution"}}}
	return ai.AnalysisResult{Summary: "1 of 1 affected monitor decisions support a suspected DNS failure.", Confidence: "medium",
		Metadata: ai.Metadata{Engine: "laya-cpu", Hypothesis: "dns_resolution", Outcome: "supported", Evidence: []ai.EvidenceReference{}, Incomplete: []string{}},
		Snapshot: snap}, nil
}

func (a *recordingAnalyzer) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.inputs)
}

type triageFixture struct {
	t       *testing.T
	ctx     context.Context
	dbc     *db.Client
	tenant  uuid.UUID
	cfg     *config.WorkerConfig
	monitor uuid.UUID
}

func newTriageFixture(t *testing.T) *triageFixture {
	t.Helper()
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	f := &triageFixture{t: t, ctx: ctx, dbc: dbc, tenant: testutil.InsertTenant(ctx, t, dbc, "triage consumer"),
		cfg: &config.WorkerConfig{AITriageEnabled: true, LocalAI: ai.Config{Timeout: 10 * time.Second}}}
	f.exec(`INSERT INTO ai_settings(tenant_id,enabled,auto_triage_enabled,triage_cooldown_seconds) VALUES($1,TRUE,TRUE,900)`, f.tenant)
	f.monitor = testutil.InsertHTTPMonitor(ctx, t, dbc, f.tenant, "api")
	return f
}

func (f *triageFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.dbc.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *triageFixture) consumer(a ai.RootCauseAnalyzer) *TriageConsumer {
	return NewTriageConsumer(f.cfg, logger.New("test", "error"), f.dbc, nil, a)
}

// alertWithTriage creates an open alert on monitor triggered at `triggered`
// and its pending triage claimed at `claimed`.
func (f *triageFixture) alertWithTriage(monitor uuid.UUID, kind string, triggered, claimed time.Time) TriageJob {
	f.t.Helper()
	alert := uuid.New()
	f.exec(`INSERT INTO alerts(id,tenant_id,monitor_id,kind,triggered_at,failure_count) VALUES($1,$2,$3,$4,$5,1)`, alert, f.tenant, monitor, kind, triggered)
	return f.pendingTriage(alert, monitor, claimed)
}

func (f *triageFixture) pendingTriage(alert, monitor uuid.UUID, claimed time.Time) TriageJob {
	f.t.Helper()
	id := uuid.New()
	var mon any
	if monitor != uuid.Nil {
		mon = monitor
	}
	f.exec(`INSERT INTO alert_triage(id,tenant_id,alert_id,monitor_id,status,created_at) VALUES($1,$2,$3,$4,'pending',$5)`, id, f.tenant, alert, mon, claimed)
	return TriageJob{V: TriageJobVersion, TriageID: id, AlertID: alert, TenantID: f.tenant}
}

func (f *triageFixture) handle(c *TriageConsumer, job TriageJob, delivery uint64) error {
	f.t.Helper()
	b, err := json.Marshal(job)
	if err != nil {
		f.t.Fatal(err)
	}
	return c.handle(f.ctx, &queue.Message{Data: b, NumDelivered: delivery})
}

type triageState struct {
	status, skipReason, message, hypothesis, confidence, summary string
	metadata, snapshot                                           []byte
	completed                                                    bool
}

func (f *triageFixture) state(id uuid.UUID) triageState {
	f.t.Helper()
	var s triageState
	var skip, msg, hyp, conf, sum *string
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT status,skip_reason,error_message,hypothesis,confidence,summary,metadata,decision_snapshot,completed_at IS NOT NULL
		FROM alert_triage WHERE id=$1`, id).Scan(&s.status, &skip, &msg, &hyp, &conf, &sum, &s.metadata, &s.snapshot, &s.completed); err != nil {
		f.t.Fatal(err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	s.skipReason, s.message, s.hypothesis, s.confidence, s.summary = deref(skip), deref(msg), deref(hyp), deref(conf), deref(sum)
	return s
}

// alertingSnapshot captures everything alerting owns for one alert, so a test
// can assert triage left it byte-identical.
func (f *triageFixture) alertingSnapshot(alert uuid.UUID) string {
	f.t.Helper()
	var s string
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT row_to_json(a)::text || '|' || COALESCE((SELECT json_agg(n ORDER BY n.channel_id)::text FROM alert_notification_states n WHERE n.alert_id=a.id),'')
		FROM alerts a WHERE a.id=$1`, alert).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *triageFixture) notify(alert uuid.UUID) {
	f.t.Helper()
	channel := uuid.New()
	f.exec(`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at) VALUES ($1, $2, 'email', 'email', '{}'::jsonb, TRUE, NOW(), NOW())`, channel, f.tenant)
	f.exec(`INSERT INTO alert_notification_states(alert_id,channel_id,last_sent_at,last_event_type) VALUES($1,$2,NOW(),'created')`, alert, channel)
}

func (f *triageFixture) check(monitor, tenant uuid.UUID, status, message string, at time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$6,$6)`,
		monitor, tenant, uuid.New(), status, message, at)
}

func TestTriageReadyUsesSingleMonitorEvidenceAndLeavesAlertingUntouched(t *testing.T) {
	f := newTriageFixture(t)
	other := testutil.InsertTenant(f.ctx, t, f.dbc, "foreign")
	foreign := testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, other, "foreign")
	unrelated := testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "unrelated")
	triggered := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	claimed := triggered.Add(40 * time.Minute)
	for i := -5; i < 10; i++ {
		f.check(f.monitor, f.tenant, "failure", "dns lookup failed: no such host", triggered.Add(time.Duration(i)*time.Minute))
		f.check(unrelated, f.tenant, "failure", "connection refused", triggered.Add(time.Duration(i)*time.Minute))
	}
	// Corrupt cross-tenant rows: a foreign check row on our monitor id and a
	// foreign root cause. Neither may reach the evidence.
	f.check(f.monitor, other, "success", "", triggered)
	job := f.alertWithTriage(f.monitor, "availability", triggered, claimed)
	f.exec(`UPDATE alerts SET root_cause_monitor_id=$2 WHERE id=$1`, job.AlertID, foreign)
	f.notify(job.AlertID)
	before := f.alertingSnapshot(job.AlertID)

	a := &recordingAnalyzer{}
	if err := f.handle(f.consumer(a), job, 1); err != nil {
		t.Fatal(err)
	}
	s := f.state(job.TriageID)
	if s.status != "ready" || s.hypothesis != "dns_resolution" || s.confidence != "medium" || s.summary == "" || !s.completed || s.message != "" {
		t.Fatalf("ready row %+v", s)
	}
	var meta ai.Metadata
	if err := json.Unmarshal(s.metadata, &meta); err != nil || meta.Hypothesis != "dns_resolution" {
		t.Fatalf("metadata %s %v", s.metadata, err)
	}
	var snap ai.DecisionSnapshot
	if err := json.Unmarshal(s.snapshot, &snap); err != nil || len(snap.Decisions) != 1 {
		t.Fatalf("snapshot %s %v", s.snapshot, err)
	}
	if a.calls() != 1 {
		t.Fatalf("analyzer calls %d", a.calls())
	}
	in := a.inputs[0]
	if len(in.Monitors) != 1 || in.Monitors[0].Ref != f.monitor.String() || !in.Monitors[0].Affected {
		t.Fatalf("evidence monitors %+v", in.Monitors)
	}
	if len(in.Monitors[0].DependencyRoots) != 0 {
		t.Fatalf("foreign root reached evidence: %v", in.Monitors[0].DependencyRoots)
	}
	if got := in.Monitors[0].Summary; got.Checks != 15 || got.Failures != 15 {
		t.Fatalf("summary %+v (foreign check row counted?)", got)
	}
	if !in.WindowStart.Equal(triggered.Add(-15*time.Minute)) || !in.WindowEnd.Equal(claimed) || !in.Incident.CreatedAt.Equal(triggered) {
		t.Fatalf("window %s - %s anchor %s", in.WindowStart, in.WindowEnd, in.Incident.CreatedAt)
	}
	if after := f.alertingSnapshot(job.AlertID); after != before {
		t.Fatalf("triage changed alerting state:\n%s\n%s", before, after)
	}
}

func TestTriageOutageMarksFailedAndLeavesAlertingUntouched(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	f.notify(job.AlertID)
	before := f.alertingSnapshot(job.AlertID)
	a := &recordingAnalyzer{err: errors.New("dial tcp 10.0.0.7:8000: connection refused (confidential)")}
	if err := f.handle(f.consumer(a), job, 1); err != nil {
		t.Fatal(err)
	}
	s := f.state(job.TriageID)
	if s.status != "failed" || s.message != triageUnavailableMessage || !s.completed || s.snapshot != nil {
		t.Fatalf("outage row %+v", s)
	}
	var meta ai.Metadata
	if err := json.Unmarshal(s.metadata, &meta); err != nil || meta.Outcome != "failed" {
		t.Fatalf("failed metadata %s", s.metadata)
	}
	if after := f.alertingSnapshot(job.AlertID); after != before {
		t.Fatalf("outage changed alerting state:\n%s\n%s", before, after)
	}

	// No analyzer configured at all: same failure, no model call.
	job2 := f.alertWithTriage(testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "second"), "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	if err := f.handle(f.consumer(nil), job2, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(job2.TriageID); s.status != "failed" || s.message != triageUnavailableMessage {
		t.Fatalf("nil analyzer row %+v", s)
	}
}

func TestTriageSkips(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	a := &recordingAnalyzer{}
	c := f.consumer(a)

	// Resolved before the job ran.
	resolved := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	f.exec(`UPDATE alerts SET status='resolved',resolved_at=NOW() WHERE id=$1`, resolved.AlertID)
	if err := f.handle(c, resolved, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(resolved.TriageID); s.status != "skipped" || s.skipReason != triageSkipResolved || !s.completed {
		t.Fatalf("resolved %+v", s)
	}

	// Mesh edge: not modeled.
	var src, dst uuid.UUID
	if err := f.dbc.QueryRowContext(f.ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'a','a') RETURNING id`, f.tenant).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if err := f.dbc.QueryRowContext(f.ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'b','b') RETURNING id`, f.tenant).Scan(&dst); err != nil {
		t.Fatal(err)
	}
	mesh := uuid.New()
	f.exec(`INSERT INTO alerts(id,tenant_id,kind,source_location_id,target_location_id,triggered_at,failure_count) VALUES($1,$2,'mesh_edge',$3,$4,$5,1)`, mesh, f.tenant, src, dst, now.Add(-10*time.Minute))
	meshJob := f.pendingTriage(mesh, uuid.Nil, now.Add(-5*time.Minute))
	if err := f.handle(c, meshJob, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(meshJob.TriageID); s.status != "skipped" || s.skipReason != triageSkipNotModeled {
		t.Fatalf("mesh %+v", s)
	}
	if a.calls() != 0 {
		t.Fatalf("skips called the analyzer %d times", a.calls())
	}

	// Disabled: platform kill switch, installation, workspace AI, and
	// workspace auto-triage. Queued rows drain without a model call.
	for i, disable := range []func(){
		func() { f.cfg.AITriageEnabled = false },
		func() { f.cfg.LocalAI.Disabled = true },
		func() { f.exec(`UPDATE ai_settings SET enabled=FALSE WHERE tenant_id=$1`, f.tenant) },
		func() { f.exec(`UPDATE ai_settings SET auto_triage_enabled=FALSE WHERE tenant_id=$1`, f.tenant) },
	} {
		f.cfg.AITriageEnabled, f.cfg.LocalAI.Disabled = true, false
		f.exec(`UPDATE ai_settings SET enabled=TRUE,auto_triage_enabled=TRUE WHERE tenant_id=$1`, f.tenant)
		disable()
		job := f.alertWithTriage(testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "d-"+uuid.NewString()[:6]), "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
		if err := f.handle(c, job, 1); err != nil {
			t.Fatal(err)
		}
		if s := f.state(job.TriageID); s.status != "skipped" || s.skipReason != triageSkipDisabled {
			t.Fatalf("disabled case %d: %+v", i, s)
		}
	}
	if a.calls() != 0 {
		t.Fatalf("disabled triage called the analyzer %d times", a.calls())
	}
}

func TestTriageCooldownPerMonitor(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	a := &recordingAnalyzer{}
	c := f.consumer(a)
	// A ready triage of an earlier (resolved) alert on the same monitor.
	prior := uuid.New()
	f.exec(`INSERT INTO alerts(id,tenant_id,monitor_id,status,triggered_at,resolved_at,failure_count) VALUES($1,$2,$3,'resolved',$4,$4,1)`, prior, f.tenant, f.monitor, now.Add(-time.Hour))
	priorTriage := uuid.New()
	f.exec(`INSERT INTO alert_triage(id,tenant_id,alert_id,monitor_id,status,completed_at) VALUES($1,$2,$3,$4,'ready',NOW()-INTERVAL '5 minutes')`, priorTriage, f.tenant, prior, f.monitor)

	job := f.alertWithTriage(f.monitor, "availability", now.Add(-3*time.Minute), now.Add(-time.Minute))
	if err := f.handle(c, job, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(job.TriageID); s.status != "skipped" || s.skipReason != triageSkipCooldown {
		t.Fatalf("within cooldown %+v", s)
	}

	// Cooldown 0 disables it.
	f.exec(`UPDATE ai_settings SET triage_cooldown_seconds=0 WHERE tenant_id=$1`, f.tenant)
	job = f.alertWithTriage(f.monitor, "latency_anomaly", now.Add(-3*time.Minute), now.Add(-time.Minute))
	if err := f.handle(c, job, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(job.TriageID); s.status != "ready" {
		t.Fatalf("cooldown 0 %+v", s)
	}

	// A ready triage older than the cooldown does not block.
	f.exec(`UPDATE ai_settings SET triage_cooldown_seconds=900 WHERE tenant_id=$1`, f.tenant)
	f.exec(`UPDATE alert_triage SET completed_at=NOW()-INTERVAL '20 minutes' WHERE monitor_id=$1 AND status='ready'`, f.monitor)
	f.exec(`UPDATE alerts SET status='resolved',resolved_at=NOW() WHERE tenant_id=$1 AND monitor_id=$2 AND kind='availability'`, f.tenant, f.monitor)
	job = f.alertWithTriage(f.monitor, "availability", now.Add(-3*time.Minute), now.Add(-time.Minute))
	if err := f.handle(c, job, 1); err != nil {
		t.Fatal(err)
	}
	if s := f.state(job.TriageID); s.status != "ready" {
		t.Fatalf("expired cooldown %+v", s)
	}
	if a.calls() != 2 {
		t.Fatalf("analyzer calls %d", a.calls())
	}
}

func TestTriageForgedTenantIsDropped(t *testing.T) {
	f := newTriageFixture(t)
	other := testutil.InsertTenant(f.ctx, t, f.dbc, "attacker")
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	a := &recordingAnalyzer{}
	c := f.consumer(a)
	for _, forged := range []TriageJob{
		{V: TriageJobVersion, TriageID: job.TriageID, AlertID: job.AlertID, TenantID: other},
		{V: TriageJobVersion, TriageID: job.TriageID, AlertID: uuid.New(), TenantID: f.tenant},
	} {
		if err := f.handle(c, forged, 1); err != nil {
			t.Fatal(err)
		}
	}
	if a.calls() != 0 {
		t.Fatal("forged job reached the analyzer")
	}
	if s := f.state(job.TriageID); s.status != "pending" {
		t.Fatalf("forged job wrote the row: %+v", s)
	}
	// Writes are tenant-guarded as well.
	forged := job
	forged.TenantID = other
	for _, write := range []func() (bool, error){
		func() (bool, error) { return c.markTriageFailed(f.ctx, forged, "x") },
		func() (bool, error) { return c.markTriageSkipped(f.ctx, forged, triageSkipCooldown) },
		func() (bool, error) { return c.markTriageReady(f.ctx, forged, ai.AnalysisResult{Confidence: "low"}) },
	} {
		if updated, err := write(); err != nil || updated {
			t.Fatalf("cross-tenant write: updated=%t err=%v", updated, err)
		}
	}
}

func TestTriageLostRaceToRelayTimeout(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	a := &recordingAnalyzer{during: func() {
		f.exec(`UPDATE alert_triage SET status='failed',error_message=$2,completed_at=NOW() WHERE id=$1`, job.TriageID, triageTimeoutMessage)
	}}
	if err := f.handle(f.consumer(a), job, 1); err != nil {
		t.Fatalf("lost race must ack, got %v", err)
	}
	if s := f.state(job.TriageID); s.status != "failed" || s.message != triageTimeoutMessage {
		t.Fatalf("lost race overwrote the row: %+v", s)
	}
}

// The first delivery stamps started_at (the relay's run-timeout clock); a
// later delivery keeps the first stamp.
func TestTriageStampsFirstDelivery(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	startedDuringRun := false
	a := &recordingAnalyzer{during: func() {
		if err := f.dbc.QueryRowContext(f.ctx, `SELECT started_at IS NOT NULL FROM alert_triage WHERE id=$1`, job.TriageID).Scan(&startedDuringRun); err != nil {
			t.Error(err)
		}
	}}
	if err := f.handle(f.consumer(a), job, 1); err != nil {
		t.Fatal(err)
	}
	if !startedDuringRun || f.state(job.TriageID).status != "ready" {
		t.Fatalf("started_at not stamped before the analysis (started %t)", startedDuringRun)
	}

	again := f.alertWithTriage(testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "again"), "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	first := now.Add(-3 * time.Minute).Truncate(time.Microsecond)
	f.exec(`UPDATE alert_triage SET started_at=$2 WHERE id=$1`, again.TriageID, first)
	if err := f.handle(f.consumer(&recordingAnalyzer{}), again, 2); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT started_at FROM alert_triage WHERE id=$1`, again.TriageID).Scan(&started); err != nil || !started.Equal(first) {
		t.Fatalf("redelivery moved started_at %s -> %s (%v)", first, started, err)
	}
}

func TestTriageTransientErrorsFailOnFinalDelivery(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	c := f.consumer(&recordingAnalyzer{})
	f.exec(`ALTER TABLE ai_settings RENAME TO ai_settings_broken`)
	defer f.exec(`ALTER TABLE ai_settings_broken RENAME TO ai_settings`)
	for delivery := uint64(1); delivery < maxDeliver; delivery++ {
		err := f.handle(c, job, delivery)
		if err == nil || errors.Unwrap(err) == nil {
			t.Fatalf("delivery %d: want RetryAfter, got %v", delivery, err)
		}
		if s := f.state(job.TriageID); s.status != "pending" {
			t.Fatalf("delivery %d left %s", delivery, s.status)
		}
	}
	if err := f.handle(c, job, maxDeliver); err != nil {
		t.Fatalf("final delivery must ack, got %v", err)
	}
	if s := f.state(job.TriageID); s.status != "failed" || s.message != triageFinalDeliveryMessage || !s.completed {
		t.Fatalf("final delivery row %+v", s)
	}
}

// A local AI that is saturated, loading or restarting (ai.ErrUnavailable)
// is retried, not recorded: nothing else ever retries a triage. The final
// delivery records it as unavailable.
func TestTriageUnavailableLocalAIRetries(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	a := &recordingAnalyzer{err: fmt.Errorf("decide: %w", ai.ErrUnavailable)}
	c := f.consumer(a)
	err := f.handle(c, job, 1)
	if !errors.Is(err, ai.ErrUnavailable) || errors.Unwrap(err) == nil {
		t.Fatalf("delivery 1: want a RetryAfter around ErrUnavailable, got %v", err)
	}
	if s := f.state(job.TriageID); s.status != "pending" {
		t.Fatalf("delivery 1 left %+v", s)
	}
	a.err = nil
	if err := f.handle(c, job, 2); err != nil {
		t.Fatalf("delivery 2: %v", err)
	}
	if s := f.state(job.TriageID); s.status != "ready" || a.calls() != 2 {
		t.Fatalf("delivery 2 row %+v after %d calls", s, a.calls())
	}

	// Still unavailable on the last delivery: failed, with the unavailable message.
	job2 := f.alertWithTriage(testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "second"), "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	a.err = fmt.Errorf("decide: %w", ai.ErrUnavailable)
	if err := f.handle(c, job2, maxDeliver); err != nil {
		t.Fatalf("final delivery must ack, got %v", err)
	}
	if s := f.state(job2.TriageID); s.status != "failed" || s.message != triageUnavailableMessage || !s.completed {
		t.Fatalf("final delivery row %+v", s)
	}
}

func TestTriageConcurrentDeliveriesRunOneAnalysis(t *testing.T) {
	f := newTriageFixture(t)
	now := time.Now().UTC()
	job := f.alertWithTriage(f.monitor, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	ctl := &controlledAnalyzer{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("outage")}
	c := f.consumer(ctl)
	done := make(chan error, 1)
	go func() { done <- f.handle(c, job, 1) }()
	select {
	case <-ctl.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("analyzer did not start")
	}
	if err := f.handle(c, job, 1); !errors.Is(err, errJobBusy) {
		t.Fatalf("concurrent delivery must retry as busy, got %v", err)
	}
	close(ctl.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := f.state(job.TriageID); s.status != "failed" {
		t.Fatalf("row %+v", s)
	}
	// A late duplicate finds the row finalized and drops it.
	if err := f.handle(c, job, 2); err != nil {
		t.Fatal(err)
	}
}

func TestTriageGroupAlertExpandsToLeaves(t *testing.T) {
	f := newTriageFixture(t)
	group := testutil.InsertGroupMonitor(f.ctx, t, f.dbc, f.tenant, "edge")
	leafA := testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "leaf-a")
	leafB := testutil.InsertHTTPMonitor(f.ctx, t, f.dbc, f.tenant, "leaf-b")
	testutil.AddMonitorToGroup(f.ctx, t, f.dbc, leafA, group)
	testutil.AddMonitorToGroup(f.ctx, t, f.dbc, leafB, group)
	now := time.Now().UTC()
	job := f.alertWithTriage(group, "availability", now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	a := &recordingAnalyzer{}
	if err := f.handle(f.consumer(a), job, 1); err != nil {
		t.Fatal(err)
	}
	if a.calls() != 1 {
		t.Fatalf("calls %d", a.calls())
	}
	got := map[string]bool{}
	for _, m := range a.inputs[0].Monitors {
		if !m.Affected {
			t.Fatalf("leaf not affected %+v", m)
		}
		got[m.Ref] = true
	}
	if len(got) != 2 || !got[leafA.String()] || !got[leafB.String()] {
		t.Fatalf("group seeds did not expand to leaves: %v", got)
	}
}
