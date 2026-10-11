package incidents

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAIAnalysisHistoricalResultsAndPendingRecovery(t *testing.T) {
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
	pending := uuid.New()
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,created_at) VALUES($1,$2,$3,'pending',NOW()-INTERVAL '11 minutes')`, pending, tenant, incident)
	if _, err := svc.GetLatestAIAnalysis(ctx, other, incident); err == nil {
		t.Fatal("cross-tenant incident read")
	}
	var status string
	if err := dbc.QueryRowContext(ctx, `SELECT status FROM incident_ai_analyses WHERE id=$1`, pending).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("foreign read changed pending result %s %v", status, err)
	}
	result, err = svc.GetLatestAIAnalysis(ctx, tenant, incident)
	if err != nil || result == nil || string(result.Status) != "failed" || len(result.RecommendedActions) == 0 {
		t.Fatalf("stale result %+v %v", result, err)
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
