package notificationsettings

import (
	"context"
	"testing"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestGetUpdateNotificationSettings(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ns")
	channelID := insertTestChannel(ctx, t, dbClient, tenantID, "teams-ops")
	svc := NewService(dbClient)

	// empty default initially
	settings, err := svc.Get(ctx, tenantID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(settings.DefaultChannels) != 0 || settings.AlertReminderSeconds != 3600 || settings.AutoCreateIncident {
		t.Fatalf("unexpected initial settings: %+v", settings)
	}

	// set default: one channel with 600s escalation, reminder 1800, incidents on
	upd := UpdateRequest{
		DefaultChannels:      []ChannelAssignment{{ChannelID: channelID, DelaySeconds: 600}},
		AlertReminderSeconds: intPtr(1800),
		AutoCreateIncident:   boolPtr(true),
	}
	settings, err = svc.Update(ctx, tenantID, upd)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(settings.DefaultChannels) != 1 ||
		settings.DefaultChannels[0].ChannelID != channelID ||
		settings.DefaultChannels[0].DelaySeconds != 600 ||
		settings.AlertReminderSeconds != 1800 || !settings.AutoCreateIncident {
		t.Fatalf("unexpected updated settings: %+v", settings)
	}

	// replacing the list removes old assignments
	settings, err = svc.Update(ctx, tenantID, UpdateRequest{DefaultChannels: []ChannelAssignment{}})
	if err != nil {
		t.Fatalf("Update(clear) error = %v", err)
	}
	if len(settings.DefaultChannels) != 0 {
		t.Fatalf("default channels not cleared: %+v", settings.DefaultChannels)
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func insertTestChannel(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'webhook', '{"url":"https://example.com/hook"}'::jsonb, TRUE, NOW(), NOW())
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return id
}
