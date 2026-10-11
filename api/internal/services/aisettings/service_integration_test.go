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
