//go:build !unit

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/analytics"
)

// TestDowntimeProjectionMatchesRawAnalyticsPath pins the contract between the
// two analytics downtime sources: the rollup path reads the projection this
// package writes (syncDowntimeFromIntervals), the raw path reads the state
// timeline directly (analytics loadTimelineDowntime). Once the projection
// has run, both must list the same periods for the same time, including
// down time inside a maintenance window and nothing for degraded or suspect
// time.
func TestDowntimeProjectionMatchesRawAnalyticsPath(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	closedMon := insertMonitor(ctx, t, dbClient, tenantID, "parity-closed")
	openMon := insertMonitor(ctx, t, dbClient, tenantID, "parity-open")
	degradedMon := insertMonitor(ctx, t, dbClient, tenantID, "parity-degraded")
	flakyMon := insertMonitor(ctx, t, dbClient, tenantID, "parity-flaky")
	maintMon := insertMonitor(ctx, t, dbClient, tenantID, "parity-maintenance")

	base := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) *time.Time { ts := base.Add(d); return &ts }

	insertStateInterval(ctx, t, dbClient, tenantID, closedMon, "up", *at(-26 * time.Hour), at(-3*time.Hour))
	insertStateInterval(ctx, t, dbClient, tenantID, closedMon, "down", *at(-3 * time.Hour), at(-170*time.Minute))
	insertStateInterval(ctx, t, dbClient, tenantID, closedMon, "up", *at(-170 * time.Minute), nil)

	insertStateInterval(ctx, t, dbClient, tenantID, openMon, "up", *at(-26 * time.Hour), at(-30*time.Minute))
	insertStateInterval(ctx, t, dbClient, tenantID, openMon, "down", *at(-30 * time.Minute), nil)

	insertStateInterval(ctx, t, dbClient, tenantID, degradedMon, "degraded", *at(-26 * time.Hour), nil)

	insertStateInterval(ctx, t, dbClient, tenantID, flakyMon, "up", *at(-26 * time.Hour), at(-2*time.Hour))
	insertStateInterval(ctx, t, dbClient, tenantID, flakyMon, "suspect", *at(-2 * time.Hour), at(-119*time.Minute))
	insertStateInterval(ctx, t, dbClient, tenantID, flakyMon, "up", *at(-119 * time.Minute), nil)

	insertStateInterval(ctx, t, dbClient, tenantID, maintMon, "up", *at(-26 * time.Hour), at(-5*time.Hour))
	insertStateInterval(ctx, t, dbClient, tenantID, maintMon, "down", *at(-5 * time.Hour), at(-4*time.Hour))
	insertStateInterval(ctx, t, dbClient, tenantID, maintMon, "up", *at(-4 * time.Hour), nil)
	windowID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at) VALUES ($1, $2, 'planned', $3, $4)
	`, windowID, tenantID, *at(-6 * time.Hour), *at(-3 * time.Hour)); err != nil {
		t.Fatalf("insert maintenance window: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1, $2)
	`, windowID, maintMon); err != nil {
		t.Fatalf("attach maintenance window: %v", err)
	}

	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}

	repo := analytics.NewRepository(dbClient)
	now := time.Now().UTC()
	all := []uuid.UUID{closedMon, openMon, degradedMon, flakyMon, maintMon}
	scopes := map[string][]uuid.UUID{
		"closed":      {closedMon},
		"open":        {openMon},
		"degraded":    {degradedMon},
		"flaky":       {flakyMon},
		"maintenance": {maintMon},
		"scope":       all,
	}
	for name, ids := range scopes {
		raw, err := repo.GetScopeAnalytics(ctx, tenantID, ids, analytics.Range24h, now)
		if err != nil {
			t.Fatalf("%s: raw analytics: %v", name, err)
		}
		rollup, err := repo.GetScopeAnalytics(ctx, tenantID, ids, analytics.Range7d, now)
		if err != nil {
			t.Fatalf("%s: rollup analytics: %v", name, err)
		}
		if raw.Source != analytics.SourceRaw || rollup.Source != analytics.SourceRollup {
			t.Fatalf("%s: sources = %s/%s, want raw/rollup", name, raw.Source, rollup.Source)
		}
		// Every interval here lies inside the 24h window, so the 7d list
		// must equal the 24h list exactly.
		if len(raw.Downtime) != len(rollup.Downtime) {
			t.Fatalf("%s: raw downtime %+v, rollup downtime %+v", name, raw.Downtime, rollup.Downtime)
		}
		for i := range raw.Downtime {
			r, p := raw.Downtime[i], rollup.Downtime[i]
			if !r.Start.Equal(p.Start) || !r.End.Equal(p.End) || r.IsOpen != p.IsOpen {
				t.Fatalf("%s: period %d raw %+v != rollup %+v", name, i, r, p)
			}
		}
	}

	scope, err := repo.GetScopeAnalytics(ctx, tenantID, all, analytics.Range24h, now)
	if err != nil {
		t.Fatalf("scope analytics: %v", err)
	}
	// maintenance (-5h..-4h), closed (-3h..-2h50m), open (-30m..now).
	if len(scope.Downtime) != 3 || !scope.Downtime[2].IsOpen {
		t.Fatalf("scope downtime = %+v, want 3 periods, the last open", scope.Downtime)
	}
}
