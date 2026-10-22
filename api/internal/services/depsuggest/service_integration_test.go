//go:build !unit

package depsuggest

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/testutil"
	"testing"
	"time"
)

type testConfig struct{}

func (testConfig) EffectiveConfig(context.Context, uuid.UUID) (ai.Config, bool, error) {
	return ai.Config{}, true, nil
}

type captureAdvisor struct{ input ai.DependencyInput }

func (a *captureAdvisor) SuggestDependencies(_ context.Context, in ai.DependencyInput) (ai.DependencySuggestions, error) {
	a.input = in
	refs := map[string]string{}
	for _, m := range in.Monitors {
		refs[m.Name] = m.Ref
	}
	return ai.DependencySuggestions{Analyzed: 1, Suggestions: []ai.DependencySuggestion{
		{Monitor: refs["database"], DependsOn: refs["api"]}, // produces cycle through disabled intermediate
		{Monitor: refs["api"], DependsOn: refs["database"]}, // existing edge
		{Monitor: refs["api"], DependsOn: refs["api"]},
		{Monitor: "unknown", DependsOn: refs["api"]},
	}}, nil
}
func TestCoFiringTenantIsolationAndFullGraph(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "pairs")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	a := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "database")
	b := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "api")
	middle := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "disabled-middle")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "foreign")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE monitors SET tags=ARRAY['service:checkout'] WHERE id=ANY($1)`, "{"+a.String()+","+b.String()+"}")
	exec(`UPDATE monitors SET enabled=FALSE WHERE id=$1`, middle)
	exec(`INSERT INTO monitor_dependencies(monitor_id,depends_on_id) VALUES($1,$2),($2,$3),($1,$3)`, b, middle, a)
	group := testutil.InsertGroupMonitor(ctx, t, dbc, other, "foreign-group")
	testutil.AddMonitorToGroup(ctx, t, dbc, a, group) // corrupt legacy association must not leak group
	// Alerts have a one-open-availability-alert invariant. Resolved fixtures
	// occupy independent aligned buckets and deliberately reverse one window.
	anchor := time.Now().UTC().Truncate(10 * time.Minute).Add(-time.Hour)
	for i := 0; i < 3; i++ {
		at := anchor.Add(time.Duration(i) * 10 * time.Minute)
		lead := 30 * time.Second
		if i == 2 {
			lead = -30 * time.Second
			at = at.Add(time.Minute)
		}
		for _, v := range []struct {
			id     uuid.UUID
			tenant uuid.UUID
			at     time.Time
		}{{a, tenant, at}, {b, tenant, at.Add(lead)}, {foreign, other, at}} {
			exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,resolved_at,failure_count,status,kind) VALUES($1,$2,$3,$4,$4::timestamptz+INTERVAL '1 minute',1,'resolved','availability')`, uuid.New(), v.tenant, v.id, v.at)
		}
	}
	s := NewService(dbc, testConfig{})
	advisor := &captureAdvisor{}
	s.advisor = func(ai.Config) (ai.DependencyAdvisor, error) { return advisor, nil }
	pairs, err := s.coFiringPairs(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].count != 3 || pairs[0].reverse != 1 {
		t.Fatalf("pairs %+v", pairs)
	}
	result, err := s.Suggest(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Suggestions) != 0 {
		t.Fatalf("unsafe suggestions %+v", result)
	}
	if len(advisor.input.Monitors) != 2 || len(advisor.input.ExistingDependencies) != 3 {
		t.Fatalf("input %+v", advisor.input)
	}
	for _, m := range advisor.input.Monitors {
		if len(m.Groups) != 0 {
			t.Fatal("foreign group leaked")
		}
	}
}

type offConfig struct{}

func (offConfig) EffectiveConfig(context.Context, uuid.UUID) (ai.Config, bool, error) {
	return ai.Config{}, false, nil
}

// PairDecision serves operator labels: it needs no local AI, resolves the pair
// through the same input as Suggest, and refuses non-candidates.
func TestPairDecision(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "pairs")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	a := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-db")
	b := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-api")
	group := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "billing-group")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "billing-foreign")
	s := NewService(dbc, offConfig{})
	if _, err := s.Suggest(ctx, tenant); err != ai.ErrNotConfigured {
		t.Fatalf("Suggest must still require local AI: %v", err)
	}
	d, err := s.PairDecision(ctx, tenant, b, a)
	if err != nil {
		t.Fatal(err)
	}
	if d.Task != "dependency" || d.Pair == nil || (d.Pair.A != a.String() && d.Pair.A != b.String()) || d.Pair.A == d.Pair.B {
		t.Fatalf("%+v", d)
	}
	var e ai.PairEvidence
	if err := json.Unmarshal(d.Evidence, &e); err != nil || !e.NamingSignal || e.CoFires != 0 {
		t.Fatalf("evidence %s %v", d.Evidence, err)
	}
	if _, err := s.PairDecision(ctx, tenant, a, group); err != ErrPairUnavailable {
		t.Fatalf("group: %v", err)
	}
	if _, err := s.PairDecision(ctx, tenant, a, foreign); err != ErrMonitorNotFound {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := s.PairDecision(ctx, tenant, a, a); err != ErrMonitorNotFound {
		t.Fatalf("self: %v", err)
	}
}
