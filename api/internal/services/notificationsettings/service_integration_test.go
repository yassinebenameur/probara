package notificationsettings

import (
	"context"
	"errors"
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

func TestDependencySuppressionSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ns-dep")
	svc := NewService(dbClient)

	// Opt-in: off with a 120s grace for every existing tenant.
	settings, err := svc.Get(ctx, tenantID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if settings.DependencySuppressionEnabled || settings.DependencySuppressionGraceSeconds != 120 {
		t.Fatalf("unexpected defaults: enabled=%v grace=%d", settings.DependencySuppressionEnabled, settings.DependencySuppressionGraceSeconds)
	}

	settings, err = svc.Update(ctx, tenantID, UpdateRequest{
		DependencySuppressionEnabled:      boolPtr(true),
		DependencySuppressionGraceSeconds: intPtr(300),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !settings.DependencySuppressionEnabled || settings.DependencySuppressionGraceSeconds != 300 {
		t.Fatalf("settings not persisted: enabled=%v grace=%d", settings.DependencySuppressionEnabled, settings.DependencySuppressionGraceSeconds)
	}

	// A partial update leaves the other field alone.
	settings, err = svc.Update(ctx, tenantID, UpdateRequest{DependencySuppressionEnabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("Update(partial) error = %v", err)
	}
	if settings.DependencySuppressionEnabled || settings.DependencySuppressionGraceSeconds != 300 {
		t.Fatalf("partial update clobbered grace: enabled=%v grace=%d", settings.DependencySuppressionEnabled, settings.DependencySuppressionGraceSeconds)
	}

	for _, bad := range []int{-1, MaxDependencySuppressionGraceSeconds + 1} {
		if _, err := svc.Update(ctx, tenantID, UpdateRequest{DependencySuppressionGraceSeconds: intPtr(bad)}); err == nil {
			t.Fatalf("Update(grace=%d) must be rejected", bad)
		}
	}
}

// TestUpdateRejectsChannelFromOtherTenant asserts that Update returns
// ErrChannelNotFound (wrapped) when the caller supplies a channel_id that
// belongs to a different tenant, and that no row is persisted.
func TestUpdateRejectsChannelFromOtherTenant(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, dbClient, "tenant-a")
	tenantB := testutil.InsertTenant(ctx, t, dbClient, "tenant-b")
	// channelB belongs to tenant B — tenant A must NOT be able to use it.
	channelB := insertTestChannel(ctx, t, dbClient, tenantB, "channel-b")

	svc := NewService(dbClient)

	_, err := svc.Update(ctx, tenantA, UpdateRequest{
		DefaultChannels: []ChannelAssignment{{ChannelID: channelB, DelaySeconds: 0}},
	})
	if err == nil {
		t.Fatal("Update() expected error for cross-tenant channel, got nil")
	}
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("Update() expected ErrChannelNotFound, got: %v", err)
	}

	// Verify nothing was persisted for tenant A.
	settings, err := svc.Get(ctx, tenantA)
	if err != nil {
		t.Fatalf("Get() after failed update error = %v", err)
	}
	if len(settings.DefaultChannels) != 0 {
		t.Fatalf("expected no default channels for tenant A after rejected update, got: %+v", settings.DefaultChannels)
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
