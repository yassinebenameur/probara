//go:build !unit

package incidents

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAIAnalysisHistoricalResultsAndReadOnlyGet(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "AI history")
	other := testutil.InsertTenant(ctx, t, dbc, "other workspace")
	incident := uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'history')`, incident, tenant)
	legacy := uuid.New()
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,summary,metadata,created_at) VALUES($1,$2,$3,'ready','Historical provider summary','{"engine":"legacy-llm","outcome":"legacy"}',NOW()-INTERVAL '1 hour')`, legacy, tenant, incident)
	svc := NewService(dbc, nil)
	result, err := svc.GetLatestAIAnalysis(ctx, tenant, incident)
	if err != nil || result == nil || result.Summary != "Historical provider summary" || !strings.Contains(string(result.Metadata), "legacy-llm") {
		t.Fatalf("historical result %+v %v", result, err)
	}
	// GET is read-only: a stale pending row stays pending (the worker-side sweep
	// fails it), and no column of any analysis row changes on read.
	pending := uuid.New()
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,'pending',NOW()-INTERVAL '11 minutes')`, pending, tenant, incident)
	snapshot := func() string {
		t.Helper()
		var out string
		if err := dbc.QueryRowContext(ctx, `SELECT COALESCE(string_agg(to_jsonb(a)::text, '|' ORDER BY id), '') FROM incident_ai_analyses a`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if _, err := svc.GetLatestAIAnalysis(ctx, other, incident); err == nil {
		t.Fatal("cross-tenant incident read")
	}
	result, err = svc.GetLatestAIAnalysis(ctx, tenant, incident)
	if err != nil || result == nil || result.ID != pending || string(result.Status) != "pending" || result.CompletedAt != nil || result.ErrorMessage != "" {
		t.Fatalf("stale pending result %+v %v", result, err)
	}
	if _, err := svc.GetIncident(ctx, tenant, incident); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("GET mutated analyses:\nbefore %s\nafter  %s", before, after)
	}
	newRun, err := svc.RequestAIAnalysis(ctx, tenant, incident, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err = svc.GetLatestAIAnalysis(ctx, tenant, incident)
	if err != nil || result == nil || result.ID != newRun.ID || string(result.Status) != "pending" {
		t.Fatalf("new pending run %+v %v", result, err)
	}
}

func TestFailPendingAIAnalysisIsTenantScopedAndPendingOnly(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "AI enqueue")
	other := testutil.InsertTenant(ctx, t, dbc, "other workspace")
	incident := uuid.New()
	if _, err := dbc.ExecContext(ctx, `INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'enqueue')`, incident, tenant); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dbc, nil)
	run, err := svc.RequestAIAnalysis(ctx, tenant, incident, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := func(id uuid.UUID) (string, string) {
		t.Helper()
		var st, msg string
		if err := dbc.QueryRowContext(ctx, `SELECT status, COALESCE(error_message,'') FROM incident_ai_analyses WHERE id=$1`, id).Scan(&st, &msg); err != nil {
			t.Fatal(err)
		}
		return st, msg
	}

	if err := svc.FailPendingAIAnalysis(ctx, other, run.ID, "foreign"); err != nil {
		t.Fatal(err)
	}
	if st, _ := status(run.ID); st != "pending" {
		t.Fatalf("foreign tenant failed the run: %s", st)
	}

	if err := svc.FailPendingAIAnalysis(ctx, tenant, run.ID, "the analysis job could not be queued"); err != nil {
		t.Fatal(err)
	}
	result, err := svc.GetLatestAIAnalysis(ctx, tenant, incident)
	if err != nil || result == nil || string(result.Status) != "failed" || result.ErrorMessage != "the analysis job could not be queued" ||
		result.CompletedAt == nil || len(result.RecommendedActions) == 0 || !strings.Contains(string(result.Metadata), `"outcome": "failed"`) {
		t.Fatalf("failed run %+v %v", result, err)
	}

	// A run the worker already finished must never be overwritten.
	ready := uuid.New()
	if _, err := dbc.ExecContext(ctx, `INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,summary) VALUES($1,$2,$3,'ready','done')`, ready, tenant, incident); err != nil {
		t.Fatal(err)
	}
	if err := svc.FailPendingAIAnalysis(ctx, tenant, ready, "late"); err != nil {
		t.Fatal(err)
	}
	if st, msg := status(ready); st != "ready" || msg != "" {
		t.Fatalf("ready run overwritten: %s %q", st, msg)
	}
}
