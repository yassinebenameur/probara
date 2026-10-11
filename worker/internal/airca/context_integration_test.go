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
	anchor := time.Now().UTC().Add(-2 * time.Hour)
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
	anchor := time.Now().UTC().Add(-time.Hour)
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
