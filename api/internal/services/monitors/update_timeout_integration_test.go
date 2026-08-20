package monitors_test

// Regression: the update path's timeout < interval check must apply only to
// active check types (validation.IsActiveCheckType). Passive types never
// execute a check — the DB constraint exempts them (migration 000012) and
// the agent form has always written timeout == interval, so the blanket
// check made every UI edit of an agent monitor fail with
// "timeout_seconds must be less than interval_seconds".

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestUpdateMonitorTimeoutCheckSkipsPassiveTypes(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	repo := monitorsvc.NewPostgresRepository(dbClient)
	svc := monitorsvc.NewService(repo)

	// Agent monitors are created with timeout == interval (the form doubles
	// expected_interval_seconds into both).
	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name:            "agent-timeout-edit",
		Type:            models.MonitorTypeAgent,
		Config:          json.RawMessage(`{"agent_id":"","expected_interval_seconds":60}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  60,
	})
	require.NoError(t, err)

	// A config-only edit (what the UI sends when saving the form) must not
	// trip the timeout check.
	updated, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"agent_id":"` + *created.AgentID + `","expected_interval_seconds":60,"metric_rules":[{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.9}]}`),
	})
	require.NoError(t, err, "editing an agent monitor with timeout == interval must succeed")
	require.Len(t, updated.NotificationChannels, 0)

	// Active types keep the guard.
	httpMon, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name:            "http-timeout-edit",
		Type:            models.MonitorTypeHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	})
	require.NoError(t, err)

	badTimeout := 60
	_, err = svc.UpdateMonitor(ctx, tenantID, httpMon.ID, &models.UpdateMonitorRequest{
		TimeoutSeconds: &badTimeout,
	})
	require.Error(t, err, "http timeout == interval must still be rejected")
	require.Contains(t, err.Error(), "timeout_seconds must be less than interval_seconds")
}
