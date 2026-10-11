package aisettings

import (
	"context"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/testutil"
	"testing"
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
