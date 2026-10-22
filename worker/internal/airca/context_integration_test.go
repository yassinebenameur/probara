//go:build !unit

package airca

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/testutil"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIncidentWindowTenantIsolationAndConsumerIdempotency(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "RCA")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	m := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	up := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "database")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "private-foreign")
	anchor := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	requested := anchor.Add(40 * time.Minute)
	incident := uuid.New()
	alert := uuid.New()
	mw := uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO incidents(id,tenant_id,title,created_at) VALUES($1,$2,'secret incident text',$3)`, incident, tenant, anchor.Add(time.Hour))
	exec(`INSERT INTO incident_monitors(incident_id,monitor_id) VALUES($1,$2),($1,$3)`, incident, m, foreign) // deliberately corrupt linkage
	exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,failure_count) VALUES($1,$2,$3,$4,1)`, alert, tenant, m, anchor)
	exec(`INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2)`, incident, alert)
	exec(`INSERT INTO monitor_dependencies(monitor_id,depends_on_id) VALUES($1,$2)`, m, up)
	for i := -20; i < 80; i++ {
		status := "success"
		message := ""
		latency := 100
		if i >= 0 && i < 25 {
			status = "failure"
			message = "dns lookup failed: secret=credentials no such host"
			latency = 900
		}
		exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,latency_ms,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7)`, m, tenant, uuid.New(), status, latency, message, anchor.Add(time.Duration(i)*time.Minute))
	}
	exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,'failure','connection refused',$4,$4,$4)`, up, tenant, uuid.New(), anchor.Add(-time.Minute))
	exec(`INSERT INTO maintenance_windows(id,tenant_id,title,starts_at,ends_at) VALUES($1,$2,'old maintenance',$3,$4)`, mw, tenant, anchor.Add(5*time.Minute), anchor.Add(10*time.Minute))
	exec(`INSERT INTO maintenance_window_monitors(maintenance_window_id,monitor_id) VALUES($1,$2)`, mw, m)
	group := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "maintenance group")
	exec(`INSERT INTO monitor_groups(group_id,monitor_id) VALUES($1,$2)`, group, up)
	groupMaintenance := uuid.New()
	exec(`INSERT INTO maintenance_windows(id,tenant_id,title,starts_at,ends_at) VALUES($1,$2,'upstream group maintenance',$3,$4)`, groupMaintenance, tenant, anchor.Add(-2*time.Minute), anchor)
	exec(`INSERT INTO maintenance_window_monitors(maintenance_window_id,monitor_id) VALUES($1,$2)`, groupMaintenance, group)
	in, err := buildAnalysisInput(ctx, dbc, tenant, incident, requested)
	if err != nil {
		t.Fatal(err)
	}
	if !in.WindowStart.Equal(anchor.Add(-15*time.Minute)) || !in.WindowEnd.Equal(requested) {
		t.Fatalf("window %+v", in)
	}
	if len(in.Monitors) != 2 {
		t.Fatalf("tenant scoped monitors %+v", in.Monitors)
	}
	for _, mc := range in.Monitors {
		if mc.Ref == foreign.String() {
			t.Fatal("foreign monitor exposed")
		}
		if mc.Ref == m.String() {
			if mc.Summary.Checks != 56 || mc.Summary.Failures != 25 || !mc.Summary.UpstreamFailedBefore || !mc.Summary.MaintenanceOverlap {
				t.Fatalf("summary %+v", mc.Summary)
			}
		}
		if mc.Ref == up.String() && !mc.Summary.MaintenanceOverlap {
			t.Fatal("historical group maintenance was missed")
		}
	}
	if _, err := buildAnalysisInput(ctx, dbc, other, incident, requested); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign incident readable %v", err)
	}
	id := uuid.New()
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,'pending',$4)`, id, tenant, incident, requested)
	analyzer := &countingAnalyzer{}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: time.Second}}, logger.New("test", "error"), dbc, nil, analyzer)
	job := ai.AnalysisJob{V: ai.AnalysisJobVersion, AnalysisID: id, IncidentID: incident, TenantID: tenant}
	payload, _ := json.Marshal(job)
	forged := job
	forged.TenantID = other
	f, _ := json.Marshal(forged)
	if err := c.handle(ctx, &queue.Message{Data: f}); err != nil {
		t.Fatal(err)
	}
	if analyzer.calls != 0 {
		t.Fatal("forged tenant invoked model")
	}
	for i := 0; i < 2; i++ {
		if err := c.handle(ctx, &queue.Message{Data: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if analyzer.calls != 1 {
		t.Fatalf("duplicate model calls %d", analyzer.calls)
	}
	var status string
	var meta []byte
	if err := dbc.QueryRowContext(ctx, `SELECT status,metadata FROM incident_ai_analyses WHERE id=$1`, id).Scan(&status, &meta); err != nil || status != "ready" {
		t.Fatalf("%s %v", status, err)
	}
	var parsed ai.Metadata
	if err := json.Unmarshal(meta, &parsed); err != nil || parsed.Outcome != "insufficient_evidence" {
		t.Fatalf("%s %v", meta, err)
	}
}

type countingAnalyzer struct{ calls int }

func (a *countingAnalyzer) Analyze(context.Context, ai.AnalysisInput) (ai.AnalysisResult, error) {
	a.calls++
	return ai.AnalysisResult{Summary: "Insufficient evidence", Confidence: "low", Metadata: ai.Metadata{Engine: "laya-cpu", Outcome: "insufficient_evidence"}}, nil
}

type controlledAnalyzer struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (a *controlledAnalyzer) Analyze(ctx context.Context, _ ai.AnalysisInput) (ai.AnalysisResult, error) {
	close(a.entered)
	select {
	case <-a.release:
		return ai.AnalysisResult{}, a.err
	case <-ctx.Done():
		return ai.AnalysisResult{}, ctx.Err()
	}
}

func TestConsumerConcurrentDeliveryOutageAndCancellation(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "failure handling")
	incident := uuid.New()
	if _, err := dbc.ExecContext(ctx, `INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'outage')`, incident, tenant); err != nil {
		t.Fatal(err)
	}
	for _, cancellation := range []bool{false, true} {
		id := uuid.New()
		if _, err := dbc.ExecContext(ctx, `INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status) VALUES($1,$2,$3,'pending')`, id, tenant, incident); err != nil {
			t.Fatal(err)
		}
		a := &controlledAnalyzer{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("infrastructure failure with confidential detail")}
		c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), dbc, nil, a)
		payload, _ := json.Marshal(ai.AnalysisJob{V: ai.AnalysisJobVersion, AnalysisID: id, IncidentID: incident, TenantID: tenant})
		jobCtx, cancel := context.WithCancel(ctx)
		finished := make(chan error, 1)
		go func() { finished <- c.handle(jobCtx, &queue.Message{Data: payload}) }()
		select {
		case <-a.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("analyzer did not start")
		}
		if err := c.handle(ctx, &queue.Message{Data: payload}); err == nil {
			t.Fatal("concurrent delivery was not rejected")
		}
		if cancellation {
			cancel()
		} else {
			close(a.release)
		}
		err := <-finished
		cancel()
		if cancellation {
			if err == nil {
				t.Fatal("cancelled persistence must retry delivery")
			}
			// A later delivery can acquire the released advisory lock.
			c.envAnalyzer = nil
			if err := c.handle(ctx, &queue.Message{Data: payload}); err != nil {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		var status, message string
		var actions []byte
		if err := dbc.QueryRowContext(ctx, `SELECT status,error_message,recommended_actions FROM incident_ai_analyses WHERE id=$1`, id).Scan(&status, &message, &actions); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || strings.Contains(message, "confidential") || !strings.Contains(strings.ToLower(string(actions)), "retry") {
			t.Fatalf("failure handling: %s %s %s", status, message, actions)
		}
	}
}

func TestHostEvidenceRejectsIdleExpiredAndForeignSamples(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "host evidence")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	m := testutil.InsertAgentMonitor(ctx, t, dbc, tenant, "host", "host-evidence", 30)
	anchor := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,created_at,started_at,completed_at) VALUES($1,$2,$3,'failure',$4,$4,$4)`, m, tenant, uuid.New(), anchor)
	insert := func(owner uuid.UUID, state string, at time.Time) {
		var id int64
		if err := dbc.QueryRowContext(ctx, `INSERT INTO metric_series(tenant_id,monitor_id,metric_name,metric_type,attributes,attr_hash) VALUES($1,$2,'system.cpu.utilization','gauge',jsonb_build_object('state',$3::text),$4) RETURNING id`, owner, m, state, []byte(uuid.NewString())).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO metric_samples(series_id,ts,value) VALUES($1,$2,0.99)`, id, at)
	}
	insert(tenant, "idle", anchor.Add(-time.Second))
	insert(tenant, "used", anchor.Add(-15*time.Minute))
	insert(other, "used", anchor.Add(-time.Second))
	available, pressure, err := loadHostMetrics(ctx, dbc, tenant, m, anchor.Add(-20*time.Minute), anchor.Add(time.Minute), 30)
	if err != nil || available || pressure {
		t.Fatalf("unsupported pressure %t %t %v", available, pressure, err)
	}
	insert(tenant, "used", anchor.Add(-time.Second))
	available, pressure, err = loadHostMetrics(ctx, dbc, tenant, m, anchor.Add(-20*time.Minute), anchor.Add(time.Minute), 30)
	if err != nil || !available || !pressure {
		t.Fatalf("fresh measured pressure missing %t %t %v", available, pressure, err)
	}
}

// echoAnalyzer mirrors the local analyzer's handling of incomplete evidence
// (copied into persisted metadata) and records which monitors it was given.
type echoAnalyzer struct{ monitors []ai.MonitorContext }

func (a *echoAnalyzer) Analyze(_ context.Context, in ai.AnalysisInput) (ai.AnalysisResult, error) {
	a.monitors = in.Monitors
	return ai.AnalysisResult{Summary: "Insufficient evidence", Confidence: "low", Metadata: ai.Metadata{Engine: "laya-cpu", Outcome: "insufficient_evidence", Evidence: []ai.EvidenceReference{}, Incomplete: append([]string{}, in.Incomplete...)}}, nil
}

func TestPlatformRowsAreNotEvidenceAndMeshAlertsArePersisted(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "evidence")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	location := func(owner uuid.UUID, name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := dbc.QueryRowContext(ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,$2,$2) RETURNING id`, owner, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(monitor uuid.UUID, status, source, message string, at time.Time) {
		t.Helper()
		exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,result_source,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7)`, monitor, tenant, uuid.New(), status, source, message, at)
	}
	m := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	anchor := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	requested := anchor.Add(40 * time.Minute)
	for i := 0; i < 10; i++ {
		check(m, "success", "monitor", "", anchor.Add(time.Duration(i)*time.Minute))
	}
	// Platform failures inside both the window and a maintenance window: none
	// of them may count as failures, evidence or maintenance overlap (S-E1).
	for i := 0; i < 5; i++ {
		check(m, "failure", "platform", "connection refused", anchor.Add(time.Duration(i)*time.Minute+30*time.Second))
	}
	mw := uuid.New()
	exec(`INSERT INTO maintenance_windows(id,tenant_id,title,starts_at,ends_at) VALUES($1,$2,'maintenance',$3,$4)`, mw, tenant, anchor.Add(-time.Minute), anchor.Add(10*time.Minute))
	exec(`INSERT INTO maintenance_window_monitors(maintenance_window_id,monitor_id) VALUES($1,$2)`, mw, m)

	incident := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title,created_at) VALUES($1,$2,'evidence incident',$3)`, incident, tenant, anchor.Add(time.Hour))
	monitorAlert := uuid.New()
	exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,failure_count) VALUES($1,$2,$3,$4,1)`, monitorAlert, tenant, m, anchor)
	src, dst := location(tenant, "paris"), location(tenant, "frankfurt")
	meshAlert := uuid.New()
	// Triggered well before the monitor alert: it must not drag the window
	// away from the monitor evidence it bounds.
	exec(`INSERT INTO alerts(id,tenant_id,kind,source_location_id,target_location_id,triggered_at,failure_count) VALUES($1,$2,'mesh_edge',$3,$4,$5,1)`, meshAlert, tenant, src, dst, anchor.Add(-3*time.Hour))
	foreignSrc, foreignDst := location(other, "foreign-a"), location(other, "foreign-b")
	foreignMesh := uuid.New()
	exec(`INSERT INTO alerts(id,tenant_id,kind,source_location_id,target_location_id,triggered_at,failure_count) VALUES($1,$2,'mesh_edge',$3,$4,$5,1)`, foreignMesh, other, foreignSrc, foreignDst, anchor.Add(-4*time.Hour))
	// The foreign link is deliberately corrupt cross-tenant linkage.
	exec(`INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2),($1,$3),($1,$4)`, incident, monitorAlert, meshAlert, foreignMesh)

	in, err := buildAnalysisInput(ctx, dbc, tenant, incident, requested)
	if err != nil {
		t.Fatal(err)
	}
	if !in.WindowStart.Equal(anchor.Add(-15 * time.Minute)) {
		t.Fatalf("mesh alert moved the monitor window: %s", in.WindowStart)
	}
	if len(in.Monitors) != 1 || in.Monitors[0].Ref != m.String() {
		t.Fatalf("monitor evidence dropped or mesh modeled as monitor: %+v", in.Monitors)
	}
	s := in.Monitors[0].Summary
	if s.Checks != 10 || s.Failures != 0 || s.MaintenanceOverlap {
		t.Fatalf("platform rows counted as evidence: %+v", s)
	}
	for _, c := range in.Monitors[0].RecentChecks {
		if c.Status != "success" {
			t.Fatalf("platform row in timeline: %+v", c)
		}
	}
	joined := strings.Join(in.Incomplete, "\n")
	if !strings.Contains(joined, meshAlert.String()) || !strings.Contains(joined, src.String()) || !strings.Contains(joined, dst.String()) {
		t.Fatalf("linked mesh alert not recorded: %q", in.Incomplete)
	}
	for _, leaked := range []string{foreignMesh.String(), foreignSrc.String(), "paris", "frankfurt"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("incomplete evidence leaked %q: %q", leaked, in.Incomplete)
		}
	}

	// The consumer persists the note while the analyzer still only sees the
	// monitor (mesh edges never become MonitorEvidence).
	id := uuid.New()
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,'pending',$4)`, id, tenant, incident, requested)
	analyzer := &echoAnalyzer{}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: time.Second}}, logger.New("test", "error"), dbc, nil, analyzer)
	payload, _ := json.Marshal(ai.AnalysisJob{V: ai.AnalysisJobVersion, AnalysisID: id, IncidentID: incident, TenantID: tenant})
	if err := c.handle(ctx, &queue.Message{Data: payload}); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.monitors) != 1 || analyzer.monitors[0].Ref != m.String() {
		t.Fatalf("analyzer monitors %+v", analyzer.monitors)
	}
	var status string
	var meta []byte
	if err := dbc.QueryRowContext(ctx, `SELECT status,metadata FROM incident_ai_analyses WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&status, &meta); err != nil || status != "ready" {
		t.Fatalf("%s %v", status, err)
	}
	var parsed ai.Metadata
	if err := json.Unmarshal(meta, &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(parsed.Incomplete, "\n"), meshAlert.String()) {
		t.Fatalf("persisted metadata lost mesh alert: %s", meta)
	}

	// A mesh-only incident still builds, anchored on its creation time (a
	// mesh alert never anchors the window), and the foreign tenant cannot
	// read either incident.
	meshOnly := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title,created_at) VALUES($1,$2,'mesh only',$3)`, meshOnly, tenant, anchor.Add(time.Hour))
	exec(`INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2)`, meshOnly, meshAlert)
	in, err = buildAnalysisInput(ctx, dbc, tenant, meshOnly, anchor.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Monitors) != 0 || !in.WindowStart.Equal(anchor.Add(45*time.Minute)) || !strings.Contains(strings.Join(in.Incomplete, "\n"), meshAlert.String()) {
		t.Fatalf("mesh-only incident %+v", in)
	}

	// Manual incident: an attached monitor with no alert of its own plus an
	// old, still-open mesh alert. The window must stay on created_at so the
	// monitor's failures around it are seen.
	manual := uuid.New()
	manualMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "manual")
	created := anchor.Add(90 * time.Minute)
	exec(`INSERT INTO incidents(id,tenant_id,title,created_at) VALUES($1,$2,'manual',$3)`, manual, tenant, created)
	exec(`INSERT INTO incident_monitors(incident_id,monitor_id) VALUES($1,$2)`, manual, manualMonitor)
	exec(`INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2)`, manual, meshAlert)
	for i := 0; i < 3; i++ {
		check(manualMonitor, "failure", "monitor", "connection refused", created.Add(time.Duration(i)*time.Minute))
	}
	in, err = buildAnalysisInput(ctx, dbc, tenant, manual, created.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !in.WindowStart.Equal(created.Add(-15*time.Minute)) || !in.WindowEnd.Equal(created.Add(60*time.Minute)) {
		t.Fatalf("mesh alert anchored manual incident window: %s - %s", in.WindowStart, in.WindowEnd)
	}
	if len(in.Monitors) != 1 || in.Monitors[0].Ref != manualMonitor.String() || in.Monitors[0].Summary.Failures != 3 {
		t.Fatalf("manual incident lost attached monitor failures: %+v", in.Monitors)
	}
	for _, note := range in.Incomplete {
		if strings.Contains(note, "expired") {
			t.Fatalf("spurious retention note: %q", in.Incomplete)
		}
	}
	for _, inc := range []uuid.UUID{incident, meshOnly, manual} {
		if _, err := buildAnalysisInput(ctx, dbc, other, inc, requested); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign incident readable %v", err)
		}
	}

	// Host evidence ignores platform failures too.
	host := testutil.InsertAgentMonitor(ctx, t, dbc, tenant, "host", "platform-host", 30)
	check(host, "failure", "platform", "", anchor)
	var series int64
	if err := dbc.QueryRowContext(ctx, `INSERT INTO metric_series(tenant_id,monitor_id,metric_name,metric_type,attributes,attr_hash) VALUES($1,$2,'system.cpu.utilization','gauge','{"state":"used"}'::jsonb,$3) RETURNING id`, tenant, host, []byte(uuid.NewString())).Scan(&series); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO metric_samples(series_id,ts,value) VALUES($1,$2,0.99)`, series, anchor.Add(-time.Second))
	available, pressure, err := loadHostMetrics(ctx, dbc, tenant, host, anchor.Add(-20*time.Minute), anchor.Add(time.Minute), 30)
	if err != nil || available || pressure {
		t.Fatalf("platform failure produced host evidence %t %t %v", available, pressure, err)
	}
}

// The incident and alert paths share buildEvidence: an incident holding only
// one alert and that alert analysed on its own, at the same request time,
// produce identical monitors and windows (including upstreams, stored
// dependency roots, maintenance overlap and the anchor cap).
func TestIncidentAndAlertEvidenceParity(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "parity")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	m := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	up := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "db")
	root := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "dns")
	exec(`INSERT INTO monitor_dependencies(monitor_id,depends_on_id) VALUES($1,$2)`, m, up)
	anchor := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Microsecond)
	for i := -20; i < 70; i++ {
		at := anchor.Add(time.Duration(i) * time.Minute)
		status, msg := "success", ""
		if i >= 0 && i < 30 {
			status, msg = "failure", "dns lookup failed: no such host"
		}
		exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$6,$6)`, m, tenant, uuid.New(), status, msg, at)
		exec(`INSERT INTO check_results(monitor_id,tenant_id,job_id,status,error_message,created_at,started_at,completed_at) VALUES($1,$2,$3,'failure','connection refused',$4,$4,$4)`, root, tenant, uuid.New(), at.Add(-30*time.Second))
	}
	mw := uuid.New()
	exec(`INSERT INTO maintenance_windows(id,tenant_id,title,starts_at,ends_at) VALUES($1,$2,'mw',$3,$4)`, mw, tenant, anchor.Add(5*time.Minute), anchor.Add(10*time.Minute))
	exec(`INSERT INTO maintenance_window_monitors(maintenance_window_id,monitor_id) VALUES($1,$2)`, mw, m)
	alert := uuid.New()
	exec(`INSERT INTO alerts(id,tenant_id,monitor_id,root_cause_monitor_id,triggered_at,failure_count) VALUES($1,$2,$3,$4,$5,1)`, alert, tenant, m, root, anchor)
	incident := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title,created_at) VALUES($1,$2,'parity',$3)`, incident, tenant, anchor.Add(2*time.Minute))
	exec(`INSERT INTO incident_alerts(incident_id,alert_id) VALUES($1,$2)`, incident, alert)

	for _, requested := range []time.Time{anchor.Add(40 * time.Minute), anchor.Add(2 * time.Hour), anchor.Add(-time.Minute)} {
		fromIncident, err := buildAnalysisInput(ctx, dbc, tenant, incident, requested)
		if err != nil {
			t.Fatal(err)
		}
		fromAlert, err := buildAlertInput(ctx, dbc, tenant, alert, requested)
		if err != nil {
			t.Fatal(err)
		}
		if !fromIncident.WindowStart.Equal(fromAlert.WindowStart) || !fromIncident.WindowEnd.Equal(fromAlert.WindowEnd) {
			t.Fatalf("requested %s: windows differ: incident %s-%s alert %s-%s", requested, fromIncident.WindowStart, fromIncident.WindowEnd, fromAlert.WindowStart, fromAlert.WindowEnd)
		}
		if !reflect.DeepEqual(fromIncident.Monitors, fromAlert.Monitors) {
			t.Fatalf("requested %s: monitors differ:\nincident %+v\nalert    %+v", requested, fromIncident.Monitors, fromAlert.Monitors)
		}
		if len(fromAlert.Monitors) != 3 {
			t.Fatalf("expected alert monitor, upstream and root: %+v", fromAlert.Monitors)
		}
		capped := requested.Before(anchor)
		if capped != strings.Contains(strings.Join(fromAlert.Incomplete, "\n"), "Alert anchor lies after triage request") {
			t.Fatalf("requested %s: cap note %q", requested, fromAlert.Incomplete)
		}
		for _, mc := range fromAlert.Monitors {
			if !capped && mc.Ref == m.String() && (len(mc.DependencyRoots) != 1 || mc.DependencyRoots[0] != root.String() || !mc.Summary.UpstreamFailedBefore || !mc.Summary.MaintenanceOverlap) {
				t.Fatalf("alert monitor evidence %+v", mc)
			}
		}
		if !fromAlert.Incident.CreatedAt.Equal(anchor) {
			t.Fatalf("alert input anchor %s", fromAlert.Incident.CreatedAt)
		}
	}

	// Foreign tenants cannot read the alert; mesh alerts are not modeled.
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	if _, err := buildAlertInput(ctx, dbc, other, alert, anchor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign alert readable: %v", err)
	}
	var src, dst uuid.UUID
	if err := dbc.QueryRowContext(ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'a','a') RETURNING id`, tenant).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if err := dbc.QueryRowContext(ctx, `INSERT INTO locations(tenant_id,name,slug) VALUES($1,'b','b') RETURNING id`, tenant).Scan(&dst); err != nil {
		t.Fatal(err)
	}
	mesh := uuid.New()
	exec(`INSERT INTO alerts(id,tenant_id,kind,source_location_id,target_location_id,triggered_at,failure_count) VALUES($1,$2,'mesh_edge',$3,$4,$5,1)`, mesh, tenant, src, dst, anchor)
	if _, err := buildAlertInput(ctx, dbc, tenant, mesh, anchor); !errors.Is(err, errNotModeled) {
		t.Fatalf("mesh alert: %v", err)
	}
}
