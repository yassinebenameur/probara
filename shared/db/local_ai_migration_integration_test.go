package db_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/testutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocalAIMigrationPreservesLegacyResultsAndSwitches(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, dbc, "legacy AI")
	tx, err := dbc.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	_, file, _, _ := runtime.Caller(0)
	migration := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	pendingIndexExists := func() bool {
		t.Helper()
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname='idx_incident_ai_analyses_pending'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	// Recreate the exact pre-092 schema boundary in this isolated DB with the
	// migration's own down file.
	exec(migration("000092_local_ai_metadata.down.sql"))
	if pendingIndexExists() {
		t.Fatal("down migration kept idx_incident_ai_analyses_pending")
	}
	incident := uuid.New()
	analysis := uuid.New()
	exec(`INSERT INTO incidents(id,tenant_id,title) VALUES($1,$2,'legacy incident')`, incident, tenant)
	exec(`INSERT INTO incident_ai_analyses(id,tenant_id,incident_id,status,model,summary,probable_root_cause) VALUES($1,$2,$3,'ready','old-provider','preserved historical summary','historical suspected cause')`, analysis, tenant, incident)
	exec(`INSERT INTO ai_settings(tenant_id,enabled,base_url,api_key_encrypted,model) VALUES($1,FALSE,'https://legacy.invalid','retained ciphertext','legacy-model')`, tenant)
	exec(migration("000092_local_ai_metadata.up.sql"))
	if !pendingIndexExists() {
		t.Fatal("up migration did not create idx_incident_ai_analyses_pending (worker stale sweep)")
	}
	var text string
	var metadata []byte
	if err := tx.QueryRowContext(ctx, `SELECT summary,metadata FROM incident_ai_analyses WHERE id=$1`, analysis).Scan(&text, &metadata); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(metadata, &value); err != nil {
		t.Fatal(err)
	}
	if text != "preserved historical summary" || value["engine"] != "legacy-llm" {
		t.Fatalf("%s %s", text, metadata)
	}
	var enabled bool
	var key string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,api_key_encrypted FROM ai_settings WHERE tenant_id=$1`, tenant).Scan(&enabled, &key); err != nil {
		t.Fatal(err)
	}
	if enabled || key != "retained ciphertext" {
		t.Fatal("Legacy workspace switch or key changed")
	}
	var defaultValue string
	if err := tx.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns WHERE table_name='ai_settings' AND column_name='enabled'`).Scan(&defaultValue); err != nil {
		t.Fatal(err)
	}
	if defaultValue != "true" {
		t.Fatal(defaultValue)
	}
}
