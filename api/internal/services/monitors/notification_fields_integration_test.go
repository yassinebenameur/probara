package monitors_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorsvc "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestMonitorNotificationFieldsRoundTrip(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")

	// Insert an alert channel to use in custom routing.
	channelID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'test-channel', 'email', '{"to":["ops@example.com"]}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID)
	require.NoError(t, err, "insert alert channel")

	repo := monitorsvc.NewPostgresRepository(dbClient)
	svc := monitorsvc.NewService(repo)

	threshold := 3
	mode := "custom"

	// --- Create a monitor with custom notification fields ---
	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name:                         "notif-roundtrip",
		Type:                         models.MonitorTypeHTTP,
		Config:                       json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
		IntervalSeconds:              60,
		TimeoutSeconds:               30,
		ConsecutiveFailuresThreshold: &threshold,
		NotificationMode:             &mode,
		NotificationChannels: []models.MonitorChannelAssignment{
			{ChannelID: channelID.String(), DelaySeconds: 0},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 3, created.ConsecutiveFailuresThreshold, "threshold should be 3")
	require.Equal(t, "custom", created.NotificationMode, "mode should be custom")
	require.Len(t, created.NotificationChannels, 1, "should have 1 channel")
	require.Equal(t, channelID.String(), created.NotificationChannels[0].ChannelID)
	require.Equal(t, "test-channel", created.NotificationChannels[0].ChannelName, "channel name should be populated")
	require.NotEmpty(t, created.NotificationChannels[0].ChannelType, "channel type should be populated")
	require.Equal(t, "email", created.NotificationChannels[0].ChannelType, "channel type should be email")

	// Verify Get also returns the channels
	got, err := svc.GetMonitor(ctx, tenantID, created.ID)
	require.NoError(t, err)
	require.Equal(t, 3, got.ConsecutiveFailuresThreshold)
	require.Equal(t, "custom", got.NotificationMode)
	require.Len(t, got.NotificationChannels, 1)

	// --- Update: switch to default mode — channels should be cleared ---
	defaultMode := "default"
	updated, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		NotificationMode: &defaultMode,
	})
	require.NoError(t, err)
	require.Equal(t, "default", updated.NotificationMode, "mode should be default after update")
	require.Empty(t, updated.NotificationChannels, "channels should be empty after switching to default")

	// Confirm with a fresh Get
	got2, err := svc.GetMonitor(ctx, tenantID, created.ID)
	require.NoError(t, err)
	require.Equal(t, "default", got2.NotificationMode)
	require.Empty(t, got2.NotificationChannels)
}
