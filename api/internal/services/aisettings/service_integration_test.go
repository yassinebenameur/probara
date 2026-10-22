//go:build !unit

package aisettings

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestWorkspaceEnablementPreservesLegacySwitch(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "AI workspace")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	s := NewService(dbc, ai.Config{})
	enabled, err := s.EffectiveEnabled(ctx, tenant)
	if err != nil || !enabled {
		t.Fatalf("default %v %v", enabled, err)
	}
	if _, err := dbc.ExecContext(ctx, `INSERT INTO ai_settings (tenant_id,enabled,base_url,api_key_encrypted,model) VALUES ($1,FALSE,'http://legacy-unreachable','legacy-secret','old-model')`, tenant); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.EffectiveEnabled(ctx, tenant)
	if err != nil || enabled {
		t.Fatal("explicit legacy disable ignored")
	}
	enabled, err = s.EffectiveEnabled(ctx, other)
	if err != nil || !enabled {
		t.Fatal("tenant switches leaked")
	}
	yes := true
	if _, err := s.Update(ctx, tenant, UpdateRequest{Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	var url, key string
	if err := dbc.QueryRowContext(ctx, `SELECT base_url,api_key_encrypted FROM ai_settings WHERE tenant_id=$1`, tenant).Scan(&url, &key); err != nil {
		t.Fatal(err)
	}
	if url != "http://legacy-unreachable" || key != "legacy-secret" {
		t.Fatal("rollback columns overwritten")
	}
	disabled := NewService(dbc, ai.Config{Disabled: true})
	enabled, err = disabled.EffectiveEnabled(ctx, tenant)
	if err != nil || enabled {
		t.Fatal("installation disable ignored")
	}
}

func TestGetCachesReadinessAcrossConcurrentRequests(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "AI readiness cache")
	s := NewService(dbc, ai.Config{BaseURL: "http://10.20.30.40:8090", APIKey: "0123456789abcdef-token"})
	var calls atomic.Int32
	s.probe = func(ctx context.Context, cfg ai.Config) (string, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return "", fmt.Errorf("local AI unavailable: %w", &url.Error{Op: "Get", URL: cfg.BaseURL + "/readyz", Err: errors.New("connection refused")})
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Get(ctx, tenant)
			if err != nil {
				t.Error(err)
				return
			}
			if got.Available || got.Message != MessageUnreachable || strings.Contains(got.Message, "10.20.30.40") {
				t.Errorf("settings %+v", got)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("probe calls = %d, want 1", n)
	}
}

func TestTriageSettingsDefaultsAndPartialUpdates(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "AI triage settings")
	other := testutil.InsertTenant(ctx, t, dbc, "other")
	s := NewService(dbc, ai.Config{Disabled: true}) // no readiness probe

	got, err := s.Get(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoTriageEnabled || got.TriageDelaySeconds != 60 || got.TriageCooldownSeconds != 900 || got.TriageMaxPerHour != 60 {
		t.Fatalf("defaults without a row: %+v", got)
	}

	// The legacy single switch still works alone, and seeds triage defaults.
	no := false
	if _, err := s.Update(ctx, tenant, UpdateRequest{Enabled: &no}); err != nil {
		t.Fatal(err)
	}
	var enabled, auto bool
	var delay, cooldown, max int
	row := func(id uuid.UUID) {
		t.Helper()
		if err := dbc.QueryRowContext(ctx, `SELECT enabled, auto_triage_enabled, triage_delay_seconds, triage_cooldown_seconds, triage_max_per_hour
			FROM ai_settings WHERE tenant_id=$1`, id).Scan(&enabled, &auto, &delay, &cooldown, &max); err != nil {
			t.Fatal(err)
		}
	}
	row(tenant)
	if enabled || auto || delay != 60 || cooldown != 900 || max != 60 {
		t.Fatalf("after enabled=false: %v %v %d %d %d", enabled, auto, delay, cooldown, max)
	}

	// A partial update keeps every other field; auto-triage may be turned on
	// while the workspace switch is off.
	yes, d, c := true, 120, 0
	if _, err := s.Update(ctx, tenant, UpdateRequest{AutoTriageEnabled: &yes, TriageDelaySeconds: &d}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageCooldownSeconds: &c}); err != nil {
		t.Fatal(err)
	}
	row(tenant)
	if enabled || !auto || delay != 120 || cooldown != 0 || max != 60 {
		t.Fatalf("after partial updates: %v %v %d %d %d", enabled, auto, delay, cooldown, max)
	}

	// First write through a triage field creates the row with defaults.
	m := 5
	if _, err := s.Update(ctx, other, UpdateRequest{TriageMaxPerHour: &m}); err != nil {
		t.Fatal(err)
	}
	row(other)
	if !enabled || auto || delay != 60 || cooldown != 900 || max != 5 {
		t.Fatalf("first triage write: %v %v %d %d %d", enabled, auto, delay, cooldown, max)
	}

	installed := NewService(dbc, ai.Config{Disabled: true})
	got, err = installed.Get(ctx, tenant)
	if err != nil || got.Enabled || !got.AutoTriageEnabled || got.TriageDelaySeconds != 120 || got.TriageCooldownSeconds != 0 {
		t.Fatalf("get after updates: %+v %v", got, err)
	}

	// Out-of-range values never reach the database.
	bad := 3601
	var ve *ValidationError
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageDelaySeconds: &bad}); !errors.As(err, &ve) {
		t.Fatalf("range: %v", err)
	}
	if _, err := s.Update(ctx, tenant, UpdateRequest{}); !errors.As(err, &ve) || ve.Msg != "at least one setting is required" {
		t.Fatalf("empty: %v", err)
	}
}

// Suspected-cause notifications default off, round-trip, survive partial
// updates, and stamp triage_notifications_enabled_at only when they turn on.
func TestTriageNotificationsSetting(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "")
	fresh := testutil.InsertTenant(ctx, t, dbc, "")
	s := NewService(dbc, ai.Config{Disabled: true})

	got, err := s.Get(ctx, tenant)
	if err != nil || got.TriageNotificationsEnabled {
		t.Fatalf("default: %+v %v", got, err)
	}
	stamp := func(id uuid.UUID) (bool, *time.Time) {
		t.Helper()
		var on bool
		var at *time.Time
		if err := dbc.QueryRowContext(ctx, `SELECT triage_notifications_enabled, triage_notifications_enabled_at
			FROM ai_settings WHERE tenant_id = $1`, id).Scan(&on, &at); err != nil {
			t.Fatal(err)
		}
		return on, at
	}
	yes, no, d := true, false, 30

	// A row created by another field keeps the flag off and unstamped.
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageDelaySeconds: &d}); err != nil {
		t.Fatal(err)
	}
	if on, at := stamp(tenant); on || at != nil {
		t.Fatalf("after unrelated write: %v %v", on, at)
	}

	// Turning it on (accepted with auto-triage off) stamps the opt-in time.
	got, err = s.Update(ctx, tenant, UpdateRequest{TriageNotificationsEnabled: &yes})
	if err != nil || !got.TriageNotificationsEnabled || got.AutoTriageEnabled {
		t.Fatalf("enable: %+v %v", got, err)
	}
	on, first := stamp(tenant)
	if !on || first == nil {
		t.Fatalf("enable did not stamp: %v %v", on, first)
	}
	// Re-saving it on, or a partial update of another field, keeps the stamp.
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageNotificationsEnabled: &yes, AutoTriageEnabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageDelaySeconds: &d}); err != nil {
		t.Fatal(err)
	}
	if on, at := stamp(tenant); !on || at == nil || !at.Equal(*first) {
		t.Fatalf("stamp moved: %v %v (was %v)", on, at, first)
	}
	// Off clears it; on again restamps.
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageNotificationsEnabled: &no}); err != nil {
		t.Fatal(err)
	}
	if on, at := stamp(tenant); on || at != nil {
		t.Fatalf("disable: %v %v", on, at)
	}
	if _, err := s.Update(ctx, tenant, UpdateRequest{TriageNotificationsEnabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if on, at := stamp(tenant); !on || at == nil || at.Before(*first) {
		t.Fatalf("re-enable: %v %v", on, at)
	}

	// First write through the flag creates the row stamped.
	if _, err := s.Update(ctx, fresh, UpdateRequest{TriageNotificationsEnabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if on, at := stamp(fresh); !on || at == nil {
		t.Fatalf("first write: %v %v", on, at)
	}
}
