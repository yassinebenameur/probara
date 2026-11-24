//go:build !unit

package depsuggest

import (
	"context"
	"encoding/json"
	"fmt"
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

type inputAdvisor struct{ input ai.DependencyInput }

func (a *inputAdvisor) SuggestDependencies(_ context.Context, in ai.DependencyInput) (ai.DependencySuggestions, error) {
	a.input = in
	return ai.DependencySuggestions{}, nil
}

// Window sizes, per-monitor window totals and group sizes, and the co-fire
// cap keeping a directed pair with few co-fires over a simultaneous pile-up.
func TestCoFiringWindowEvidence(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "windows")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	alert := func(tenant, monitor uuid.UUID, at time.Time) {
		t.Helper()
		exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,resolved_at,failure_count,status,kind) VALUES($1,$2,$3,$4,$4::timestamptz+INTERVAL '1 minute',1,'resolved','availability')`, uuid.New(), tenant, monitor, at)
	}
	anchor := time.Now().UTC().Truncate(10 * time.Minute).Add(-3 * time.Hour)
	bucket := func(i int) time.Time { return anchor.Add(time.Duration(i)*10*time.Minute + time.Minute) }

	// 11 monitors failing together in three windows: 55 simultaneous pairs
	// of 3 co-fires each, more than the co-fire cap.
	var pile []uuid.UUID
	for i := 0; i < 11; i++ {
		pile = append(pile, testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, fmt.Sprintf("node-%02d", i)))
	}
	for b := 0; b < 3; b++ {
		for _, m := range pile {
			alert(tenant, m, bucket(b))
		}
	}
	// A directed pair with two co-fires, alone in its windows (a foreign
	// tenant's alert in the same window does not count), plus one lone window.
	api := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-api")
	store := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-store")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "foreign")
	for b := 4; b < 6; b++ {
		alert(tenant, store, bucket(b))
		alert(tenant, api, bucket(b).Add(30*time.Second))
		alert(other, foreign, bucket(b))
	}
	alert(tenant, store, bucket(7))
	group := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "billing")
	for _, m := range []uuid.UUID{api, store, pile[0]} {
		testutil.AddMonitorToGroup(ctx, t, dbc, m, group)
	}
	// A different group with the same name is a different group.
	twin := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "billing")
	testutil.AddMonitorToGroup(ctx, t, dbc, pile[3], twin)

	s := NewService(dbc, testConfig{})
	pairs, err := s.coFiringPairs(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != maxPairs {
		t.Fatalf("%d pairs", len(pairs))
	}
	var directed, piled *coFire
	for i := range pairs {
		p := &pairs[i]
		if p.earlier == store && p.later == api {
			directed = p
		} else if piled == nil {
			piled = p
		}
	}
	if directed == nil || directed.count != 2 || directed.lead != 30 || directed.reverse != 0 || directed.windowMonitors != 2 {
		t.Fatalf("directed pair %+v (dropped behind the pile-up?)", directed)
	}
	if piled.count != 3 || piled.lead != 0 || piled.windowMonitors != 11 {
		t.Fatalf("pile-up pair %+v", piled)
	}

	advisor := &inputAdvisor{}
	s.advisor = func(ai.Config) (ai.DependencyAdvisor, error) { return advisor, nil }
	if _, err := s.Suggest(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	byName := map[string]ai.DependencyMonitorRef{}
	for _, m := range advisor.input.Monitors {
		byName[m.Name] = m
	}
	if byName["billing-store"].AlertWindows != 3 || byName["billing-api"].AlertWindows != 2 || byName["node-03"].AlertWindows != 3 {
		t.Fatalf("alert windows %+v", advisor.input.Monitors)
	}
	g, store3, node3 := byName["billing-api"], byName["billing-store"], byName["node-03"]
	if len(g.Groups) != 1 || g.Groups[0].Ref == "" || g.Groups[0].Members != 3 || len(store3.Groups) != 1 || store3.Groups[0] != g.Groups[0] {
		t.Fatalf("groups %+v %+v", g, store3)
	}
	if len(node3.Groups) != 1 || node3.Groups[0].Ref == g.Groups[0].Ref || node3.Groups[0].Members != 1 {
		t.Fatalf("same-name group matched by name: %+v %+v", node3.Groups, g.Groups)
	}
	var found bool
	for _, p := range advisor.input.CoFiringAlerts {
		if p.Earlier == byName["billing-store"].Ref && p.Later == byName["billing-api"].Ref {
			found = p.WindowMonitors == 2 && p.TimesCoFired == 2
		}
	}
	if !found {
		t.Fatalf("co-firing input %+v", advisor.input.CoFiringAlerts)
	}
}

// The cap keeps a small directed pair ahead of a large ONE-DIRECTION pile-up
// (15 fast-interval monitors failing a couple of minutes before 15 slow ones,
// four times: 225 directed pairs of 4 co-fires), and window sizes count only
// candidate-eligible monitors (no group roll-up, disabled or deleted monitor).
func TestCoFiringCapKeepsSmallDirectedPair(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "cap")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	alert := func(monitor uuid.UUID, at time.Time) {
		t.Helper()
		exec(`INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,resolved_at,failure_count,status,kind) VALUES($1,$2,$3,$4,$4::timestamptz+INTERVAL '1 minute',1,'resolved','availability')`, uuid.New(), tenant, monitor, at)
	}
	anchor := time.Now().UTC().Truncate(10 * time.Minute).Add(-3 * time.Hour)
	bucket := func(i int) time.Time { return anchor.Add(time.Duration(i) * 10 * time.Minute) }

	for i := 0; i < 15; i++ {
		fast := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, fmt.Sprintf("fast-%02d", i))
		slow := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, fmt.Sprintf("slow-%02d", i))
		for b := 0; b < 4; b++ {
			alert(fast, bucket(b).Add(time.Minute))
			alert(slow, bucket(b).Add(3*time.Minute))
		}
	}
	api := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-api")
	store := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-store")
	group := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "billing")
	disabled := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-disabled")
	deleted := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "billing-deleted")
	exec(`UPDATE monitors SET enabled=FALSE WHERE id=$1`, disabled)
	exec(`UPDATE monitors SET deleted_at=NOW() WHERE id=$1`, deleted)
	for b := 5; b < 8; b++ {
		alert(store, bucket(b).Add(time.Minute))
		alert(api, bucket(b).Add(90*time.Second))
		// Would make each window 5 monitors if they counted.
		for _, m := range []uuid.UUID{group, disabled, deleted} {
			alert(m, bucket(b).Add(2*time.Minute))
		}
	}

	pairs, err := NewService(dbc, testConfig{}).coFiringPairs(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != maxPairs {
		t.Fatalf("%d pairs", len(pairs))
	}
	if p := pairs[0]; p.earlier != store || p.later != api || p.count != 3 || p.lead != 30 || p.windowMonitors != 2 {
		t.Fatalf("small directed pair not first: %+v", p)
	}
	for _, p := range pairs[1:] {
		for _, id := range []uuid.UUID{group, disabled, deleted} {
			if p.earlier == id || p.later == id {
				t.Fatalf("ineligible monitor in a pair: %+v", p)
			}
		}
		if p.count != 4 || p.windowMonitors != 30 {
			t.Fatalf("pile-up pair %+v", p)
		}
	}
}

// Tag and name-prefix populations count every enabled, non-deleted,
// non-group monitor of the tenant, beyond the 250 loaded into the input.
func TestPopulationsAreTenantWide(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "fleet")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	bulk := func(tenant uuid.UUID, prefix string, n int, enabled bool) {
		t.Helper()
		if _, err := dbc.ExecContext(ctx, `
			INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds, enabled, tags, created_at, updated_at)
			SELECT gen_random_uuid(), $1, $2 || g, 'http', '{"url":"https://example.com","method":"GET"}'::jsonb, 60, 30, $4, ARRAY['fleet'], NOW(), NOW()
			FROM generate_series(1, $3) g`, tenant, prefix, n, enabled); err != nil {
			t.Fatal(err)
		}
	}
	bulk(tenant, "filler-", 248, true)
	bulk(tenant, "orders api ", 12, true)
	bulk(tenant, "orders api off ", 5, false)
	bulk(other, "orders api ", 7, true)
	deleted := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "orders api gone")
	group := testutil.InsertGroupMonitor(ctx, t, dbc, tenant, "orders api group")
	if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET deleted_at=NOW(), tags=ARRAY['fleet'] WHERE id=$1`, deleted); err != nil {
		t.Fatal(err)
	}
	if _, err := dbc.ExecContext(ctx, `UPDATE monitors SET tags=ARRAY['fleet'] WHERE id=$1`, group); err != nil {
		t.Fatal(err)
	}

	set, err := NewService(dbc, testConfig{}).buildInput(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.input.Monitors) != maxMonitors {
		t.Fatalf("%d monitors loaded", len(set.input.Monitors))
	}
	if n := set.input.TagMonitors["fleet"]; n != 260 {
		t.Fatalf("fleet population %d, want 260", n)
	}
	if n := set.input.PrefixMonitors["orders api"]; n != 12 {
		t.Fatalf("prefix population %d, want 12", n)
	}
}
