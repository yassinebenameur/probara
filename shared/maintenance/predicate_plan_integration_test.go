//go:build !unit

package maintenance

// Coverage queries stay index-driven as occurrence history grows
// (docs/state-semantics.md S-M5). The predicates run per monitor row on every
// alerter tick, monitor list, dashboard and status page, so they must start
// from the monitor's own links and read only the occurrences that can matter
// at the instant or range asked about, never a tenant's whole history.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestCoverageQueriesStayIndexDrivenWithLongHistory(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := testutil.InsertTenant(ctx, t, db, "maintenance-plan")

	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// 2,000 monitors, 20 groups of 50 members, 200 daily windows (150
	// targeting 5 monitors each, 50 targeting a group, each with one removed
	// target) over three years: about 226k occurrences.
	exec(`INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds, enabled, tags, created_at, updated_at)
		SELECT gen_random_uuid(), $1, 'm' || i, 'http', '{"url":"https://example.com","method":"GET"}'::jsonb, 60, 30, TRUE, ARRAY[]::text[], NOW(), NOW()
		FROM generate_series(1, 2000) i`, tenant)
	exec(`INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds, enabled, tags, created_at, updated_at)
		SELECT gen_random_uuid(), $1, 'g' || i, 'group', '{}'::jsonb, 60, 30, TRUE, ARRAY[]::text[], NOW(), NOW()
		FROM generate_series(1, 20) i`, tenant)
	exec(`INSERT INTO monitor_groups (group_id, monitor_id)
		SELECT g.id, m.id
		FROM (SELECT id, row_number() OVER (ORDER BY name) - 1 AS n FROM monitors WHERE tenant_id = $1 AND type = 'group') g
		JOIN (SELECT id, row_number() OVER (ORDER BY name) - 1 AS n FROM monitors WHERE tenant_id = $1 AND type = 'http') m
		  ON m.n / 50 = g.n`, tenant)
	exec(`INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at,
			recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes, recurrence_timezone)
		SELECT gen_random_uuid(), $1, 'w' || i, NOW() - INTERVAL '3 years', NOW() - INTERVAL '3 years' + INTERVAL '1 hour',
			'{0,1,2,3,4,5,6}', '02:00', 60, 'UTC'
		FROM generate_series(1, 200) i`, tenant)
	exec(`WITH w AS (SELECT id, row_number() OVER (ORDER BY title) - 1 AS n FROM maintenance_windows WHERE tenant_id = $1),
		     m AS (SELECT id, row_number() OVER (ORDER BY name) - 1 AS n FROM monitors WHERE tenant_id = $1 AND type = 'http'),
		     g AS (SELECT id, row_number() OVER (ORDER BY name) - 1 AS n FROM monitors WHERE tenant_id = $1 AND type = 'group')
		INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id)
		SELECT w.id, m.id FROM w JOIN m ON w.n < 150 AND m.n / 5 = w.n
		UNION ALL
		SELECT w.id, g.id FROM w JOIN g ON w.n >= 150 AND g.n = w.n % 20`, tenant)
	exec(`INSERT INTO maintenance_window_past_targets (maintenance_window_id, monitor_id, linked_at, unlinked_at)
		SELECT w.id, m.id, NOW() - INTERVAL '3 years', NOW() - INTERVAL '1 year'
		FROM (SELECT id, row_number() OVER (ORDER BY title) - 1 AS n FROM maintenance_windows WHERE tenant_id = $1) w
		JOIN (SELECT id, row_number() OVER (ORDER BY name) - 1 AS n FROM monitors WHERE tenant_id = $1 AND type = 'http') m
		  ON m.n = (w.n * 7 + 1000) % 2000`, tenant)
	exec(`INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at)
		SELECT w.id, w.tenant_id, d + INTERVAL '2 hours', d + INTERVAL '3 hours'
		FROM maintenance_windows w
		CROSS JOIN generate_series(date_trunc('day', NOW()) - INTERVAL '1095 days', date_trunc('day', NOW()) + INTERVAL '35 days', INTERVAL '1 day') d
		WHERE w.tenant_id = $1`, tenant)
	// Autovacuum keeps the visibility map current in production, which is
	// what lets the covering index answer without heap fetches.
	exec(`VACUUM ANALYZE`)

	var occurrences int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_window_occurrences`).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if occurrences < 200_000 {
		t.Fatalf("seeded %d occurrences, want a long history", occurrences)
	}

	for _, tc := range []struct {
		name  string
		query string
		args  []interface{}
		bound time.Duration
	}{
		{
			// The monitor list's pair (api/internal/services/monitors).
			name: "monitor list",
			query: `SELECT m.id, ` + InMaintenancePredicate("m") + `, ` + MaintenanceUntilExpr("m") + `
				FROM monitors m WHERE m.tenant_id = $1`,
			args:  []interface{}{tenant},
			bound: time.Second,
		},
		{
			// The alerter's candidate filter.
			name:  "alerter filter",
			query: `SELECT COUNT(*) FROM monitors m WHERE m.tenant_id = $1 AND NOT ` + InMaintenancePredicate("m"),
			args:  []interface{}{tenant},
			bound: time.Second,
		},
		{
			// A point in the past (incident analysis).
			name:  "historical instant",
			query: `SELECT COUNT(*) FROM monitors m WHERE m.tenant_id = $1 AND ` + InMaintenanceAtPredicate("m", "$2::timestamptz"),
			args:  []interface{}{tenant, time.Now().Add(-400 * 24 * time.Hour).Truncate(24 * time.Hour).Add(150 * time.Minute)},
			bound: time.Second,
		},
		{
			// The interval engine's range read for a 30-day bucket of 200
			// monitors (an SLA report's scope).
			name: "range",
			query: `SELECT COUNT(*) FROM (SELECT id FROM monitors WHERE tenant_id = $1 ORDER BY name LIMIT 200) m
				CROSS JOIN LATERAL ` + OccurrenceRangesSQL("$1", "m.id", "$2::timestamptz", "$3::timestamptz") + ` mo`,
			args:  []interface{}{tenant, time.Now().Add(-30 * 24 * time.Hour), time.Now()},
			bound: time.Second,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, `EXPLAIN (ANALYZE, BUFFERS) `+tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, line)
			}
			rows.Close()
			text := strings.Join(plan, "\n")
			t.Logf("%s: %s", tc.name, plan[len(plan)-1])
			if testing.Verbose() {
				t.Logf("%s plan:\n%s", tc.name, strings.Join(plan, "\n"))
			}
			if strings.Contains(text, "Seq Scan on maintenance_window_occurrences") {
				t.Errorf("%s reads the occurrence table sequentially:\n%s", tc.name, text)
			}
			// Link-first: the monitor's links by monitor_id, then each
			// window's occurrences through (window_id, ends_at), never a
			// tenant-wide occurrence scan.
			if !strings.Contains(text, "Index Only Scan using idx_maintenance_window_occurrences_window_end") {
				t.Errorf("%s does not reach occurrences through (window_id, ends_at):\n%s", tc.name, text)
			}
			if strings.Contains(text, "idx_maintenance_window_occurrences_tenant_time") {
				t.Errorf("%s reads occurrences by tenant:\n%s", tc.name, text)
			}
			if !strings.Contains(text, "idx_maintenance_window_monitors_monitor") {
				t.Errorf("%s does not start from the monitor's links:\n%s", tc.name, text)
			}

			start := time.Now()
			r, err := db.QueryContext(ctx, tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			for r.Next() {
			}
			r.Close()
			took := time.Since(start)
			t.Logf("%s: query took %v", tc.name, took)
			if took > tc.bound {
				t.Errorf("%s took %v, want under %v", tc.name, took, tc.bound)
			}
		})
	}
}
