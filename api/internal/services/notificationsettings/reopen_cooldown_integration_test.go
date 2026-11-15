//go:build !unit

package notificationsettings

import (
	"context"
	"errors"
	"testing"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Relapse reopen is off (0) for every workspace until set; it accepts 0-1440
// minutes, survives unrelated partial updates, and an out-of-range value is
// refused as invalid input without writing anything.
func TestReopenCooldownSetting(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ns-reopen")
	svc := NewService(dbClient)

	settings, err := svc.Get(ctx, tenantID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if settings.AlertReopenCooldownMinutes != 0 {
		t.Fatalf("default cooldown = %d, want 0 (off)", settings.AlertReopenCooldownMinutes)
	}

	settings, err = svc.Update(ctx, tenantID, UpdateRequest{AlertReopenCooldownMinutes: intPtr(15)})
	if err != nil || settings.AlertReopenCooldownMinutes != 15 {
		t.Fatalf("Update(15) = %+v, %v", settings, err)
	}
	settings, err = svc.Update(ctx, tenantID, UpdateRequest{AutoCreateIncident: boolPtr(true)})
	if err != nil || settings.AlertReopenCooldownMinutes != 15 {
		t.Fatalf("a partial update clobbered the cooldown: %+v, %v", settings, err)
	}
	if settings, err = svc.Update(ctx, tenantID, UpdateRequest{AlertReopenCooldownMinutes: intPtr(alertrouting.MaxReopenCooldownMinutes)}); err != nil ||
		settings.AlertReopenCooldownMinutes != alertrouting.MaxReopenCooldownMinutes {
		t.Fatalf("Update(max) = %+v, %v", settings, err)
	}

	for _, bad := range []int{-1, alertrouting.MaxReopenCooldownMinutes + 1} {
		_, err := svc.Update(ctx, tenantID, UpdateRequest{AlertReopenCooldownMinutes: intPtr(bad), AutoCreateIncident: boolPtr(false)})
		if !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("Update(%d) error = %v, want ErrInvalidSetting", bad, err)
		}
	}
	settings, err = svc.Update(ctx, tenantID, UpdateRequest{AlertReopenCooldownMinutes: intPtr(0)})
	if err != nil || settings.AlertReopenCooldownMinutes != 0 || !settings.AutoCreateIncident {
		t.Fatalf("turning it off: %+v, %v (a refused update must write nothing)", settings, err)
	}
}
