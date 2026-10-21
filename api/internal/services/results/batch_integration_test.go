//go:build !unit

package results

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The batch must return, per monitor, exactly what GET /monitors/{id}/results
// returns for it — the monitors list swapped one for the other.
func TestService_GetMonitorResultsBatch_MatchesPerMonitorResults(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, groupservice.NewService(dbClient), sharedanalytics.NewRepository(dbClient))
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "results-batch")
	alpha := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "alpha")
	beta := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "beta")
	gamma := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "gamma")   // a leaf only, never requested
	quiet := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "quiet")   // one row, past raw retention
	silent := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "silent") // no results
	outer := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "outer")
	inner := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "inner")
	empty := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "empty")
	stale := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "stale")
	testutil.AddMonitorToGroup(ctx, t, dbClient, alpha, outer)
	testutil.AddMonitorToGroup(ctx, t, dbClient, inner, outer)
	testutil.AddMonitorToGroup(ctx, t, dbClient, beta, inner)
	testutil.AddMonitorToGroup(ctx, t, dbClient, gamma, inner)
	testutil.AddMonitorToGroup(ctx, t, dbClient, quiet, stale)

	deleted := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "deleted")
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "results-batch-other")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenant, "foreign")

	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, alpha, now.Add(-time.Duration(10*i+1)*time.Minute), "success", "monitor", testutil.IntPtr(100+i))
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, beta, now.Add(-time.Duration(10*i+5)*time.Minute), "failure", "monitor", nil)
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, gamma, now.Add(-time.Duration(10*i+3)*time.Minute), "success", "monitor", nil)
	}
	// Older than raw retention but not pruned yet: both endpoints still
	// return it (a newest-N read, not a window), for the monitor and the
	// group whose only leaf it is.
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, quiet, now.AddDate(0, 0, -40), "success", "monitor", testutil.IntPtr(1))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, deleted, now.Add(-time.Minute), "success", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, otherTenant, foreign, now.Add(-time.Minute), "success", "monitor", nil)
	if _, err := dbClient.ExecContext(ctx, `UPDATE check_results SET metrics_data = '{"http":{"n":1}}'::jsonb WHERE monitor_id = ANY($1)`, pq.Array([]uuid.UUID{alpha, gamma})); err != nil {
		t.Fatalf("set metrics_data: %v", err)
	}

	const limit = 3
	resp, err := svc.GetMonitorResultsBatch(ctx, tenantID, []uuid.UUID{alpha, beta, quiet, silent, outer, empty, stale, deleted, foreign, alpha}, limit)
	if err != nil {
		t.Fatalf("GetMonitorResultsBatch: %v", err)
	}
	if resp.Limit != limit {
		t.Fatalf("limit = %d, want %d", resp.Limit, limit)
	}

	// Tenant isolation and soft deletes: unknown ids are simply absent.
	for _, id := range []uuid.UUID{deleted, foreign} {
		if _, ok := resp.Results[id]; ok {
			t.Fatalf("results include %s, which is not a live monitor of the tenant", id)
		}
	}
	if len(resp.Results) != 7 {
		t.Fatalf("results for %d monitors, want 7", len(resp.Results))
	}
	if got := resp.Results[silent]; got == nil || len(got) != 0 {
		t.Fatalf("silent monitor results = %v, want an empty list", got)
	}
	if got := resp.Results[quiet]; len(got) != 1 {
		t.Fatalf("quiet monitor results = %v, want its one (old) row", got)
	}

	// Members: direct ones only, by name; groups only.
	if got := resp.Members[outer]; len(got) != 2 || got[0] != alpha || got[1] != inner {
		t.Fatalf("outer members = %v, want [alpha inner]", got)
	}
	if got, ok := resp.Members[empty]; !ok || len(got) != 0 {
		t.Fatalf("empty group members = %v (present %v), want an empty list", got, ok)
	}
	if _, ok := resp.Members[alpha]; ok {
		t.Fatalf("members reported for a non-group monitor")
	}

	for _, id := range []uuid.UUID{alpha, beta, quiet, silent, outer, empty, stale} {
		single, err := svc.GetMonitorResults(ctx, tenantID, id, limit, nil)
		if err != nil {
			t.Fatalf("GetMonitorResults(%s): %v", id, err)
		}
		assertSameResults(t, id, resp.Results[id], single.Results)
	}
	if got := resp.Results[alpha][0].MetricsData; string(got) == "" || string(got) == "null" {
		t.Fatalf("alpha metrics_data = %q, want the stored payload", got)
	}

	// The group row: derived status first (alpha up, beta down → degraded),
	// then the newest `limit` rows across both leaves, nested ones included.
	group := resp.Results[outer]
	if len(group) != limit+1 || group[0].ResultSource != string(sharedmodels.ResultSourceDerived) || group[0].Status != "degraded" {
		t.Fatalf("outer results = %+v, want derived degraded row + %d rows", group, limit)
	}
	// gamma (a leaf nobody requested, newest row at -3m) is read with the
	// light columns: its rows appear in the group without metrics_data.
	sawGamma := false
	for _, row := range group[1:] {
		if row.MetricsData != nil || row.LocationName != nil {
			t.Fatalf("group row carries leaf detail: %+v", row)
		}
		if row.Status == "success" && row.LatencyMS == nil {
			sawGamma = true
		}
	}
	if !sawGamma {
		t.Fatalf("outer results = %+v, want gamma's rows merged in", group)
	}
	if got := resp.Results[empty]; len(got) != 0 {
		t.Fatalf("empty group results = %v, want none", got)
	}
}

// assertSameResults compares two result lists, ignoring the derived row's
// generated id and timestamp.
func assertSameResults(t *testing.T, id uuid.UUID, batch, single []models.CheckResult) {
	t.Helper()
	if len(batch) != len(single) {
		t.Fatalf("%s: batch has %d results, per-monitor endpoint %d", id, len(batch), len(single))
	}
	for i := range batch {
		b, s := batch[i], single[i]
		if b.ResultSource == string(sharedmodels.ResultSourceDerived) && s.ResultSource == b.ResultSource {
			if b.Status != s.Status {
				t.Fatalf("%s: derived status %q, per-monitor %q", id, b.Status, s.Status)
			}
			continue
		}
		if b.ID != s.ID || b.Status != s.Status || !b.CreatedAt.Equal(s.CreatedAt) ||
			string(b.MetricsData) != string(s.MetricsData) || (b.LatencyMS == nil) != (s.LatencyMS == nil) {
			t.Fatalf("%s: result %d differs:\nbatch  %+v\nsingle %+v", id, i, b, s)
		}
	}
}
