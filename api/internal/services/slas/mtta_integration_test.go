//go:build !unit

package slas

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A relapse (an alert reopened within the reopen cooldown) clears
// acknowledged_at, so the report's response block reads the first
// acknowledgement instead, measured from the start of the episode it
// acknowledged. A relapse neither stretches an ack across the recovered gap
// nor erases it, and the reopened alert still counts once.
func TestSLAReportMTTAAcrossRelapses(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "sla-mtta")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "api")
	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient), false)
	svc.now = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }
	interval(ctx, t, dbClient, tenantID, monitorID, "up",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), at(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)))

	insert := func(triggered time.Time, relapses int, reopened, acked, firstAcked, firstAckEpisode *time.Time) {
		t.Helper()
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, failure_count, created_at, updated_at,
				relapse_count, reopened_at, acknowledged_at, first_acknowledged_at, first_ack_episode_started_at)
			VALUES ($1, $2, $3, 'availability', 'resolved', $4, $4::timestamptz + INTERVAL '2 hours', 3, NOW(), NOW(), $5, $6, $7, $8, $9)
		`, uuid.New(), tenantID, monitorID, triggered, relapses, reopened, acked, firstAcked, firstAckEpisode)
		require.NoError(t, err)
	}
	// Acked 2 minutes into its first episode, auto-resolved, reopened 40
	// minutes after the trigger and acked again a minute later: MTTA 2 min,
	// not the 41 minutes from the first trigger to the relapse's ack.
	t1 := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	insert(t1, 1, at(t1.Add(40*time.Minute)), at(t1.Add(41*time.Minute)), at(t1.Add(2*time.Minute)), at(t1))
	// Acked 4 minutes into its first episode, then a relapse resolved
	// unacknowledged (acknowledged_at cleared): still acknowledged, MTTA 4 min.
	t2 := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	insert(t2, 1, at(t2.Add(30*time.Minute)), nil, at(t2.Add(4*time.Minute)), at(t2))
	// Unacknowledged first episode, relapse acked 6 minutes after its reopen:
	// measured from the reopen, not across the recovered gap.
	t3 := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	insert(t3, 1, at(t3.Add(50*time.Minute)), at(t3.Add(56*time.Minute)), at(t3.Add(56*time.Minute)), at(t3.Add(50*time.Minute)))
	// Never acknowledged.
	insert(time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC), 0, nil, nil, nil, nil)

	sla, err := svc.Create(ctx, tenantID, &models.CreateSLARequest{Name: "API", TargetPct: 99.9, MonitorIDs: []string{monitorID.String()}})
	require.NoError(t, err)
	r, err := svc.Report(ctx, tenantID, sla.ID, ReportQuery{Period: "2026-09"})
	require.NoError(t, err)
	require.Equal(t, 4, r.Response.AlertCount, "a relapsed alert counts once")
	require.Equal(t, 3, r.Response.AcknowledgedCount, "a relapse never erases an earlier acknowledgement")
	require.NotNil(t, r.Response.MTTASeconds)
	require.InDelta(t, (120.0+240+360)/3, *r.Response.MTTASeconds, 1e-6)
}
