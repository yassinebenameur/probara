package monitors

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestPrometheusMonitorLifecycleIntegration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "prometheus-owner")
	other := testutil.InsertTenant(ctx, t, db, "prometheus-other")
	enc := testEncryptor(t)
	svc := NewService(NewPostgresRepository(db))
	svc.ConfigureEncryption(enc)
	raw := json.RawMessage(`{"url":"https://prom.example.com","query":"sum(up)","operator":"gte","threshold":1,"auth_type":"bearer","bearer_token":"secret"}`)
	created, err := svc.CreateMonitor(ctx, tenant, &models.CreateMonitorRequest{Name: "Prometheus", Type: models.MonitorTypePrometheus, Config: raw, IntervalSeconds: 60, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if decodeConfig(t, created.Config)["bearer_token"] != "***" {
		t.Fatal("create did not mask token")
	}
	var stored json.RawMessage
	if err := db.QueryRowContext(ctx, `SELECT config FROM monitors WHERE id=$1`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !secrets.LooksLikeEnvelope(decodeConfig(t, stored)["bearer_token"].(string)) {
		t.Fatal("token not encrypted at rest")
	}
	cfg := decodeConfig(t, created.Config)
	cfg["query"] = "max(up)"
	updatedRaw, _ := json.Marshal(cfg)
	updated, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Config: updatedRaw})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := svc.ResolveTestConfig(ctx, tenant, &created.ID, models.MonitorTypePrometheus, updated.Config)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := secrets.DecryptMonitorConfig(enc, "prometheus", resolved)
	if err != nil {
		t.Fatal(err)
	}
	if decodeConfig(t, decrypted)["bearer_token"] != "secret" {
		t.Fatal("saved token not resolved for preview")
	}
	if _, err := svc.ResolveTestConfig(ctx, other, &created.ID, models.MonitorTypePrometheus, updated.Config); err == nil {
		t.Fatal("cross-tenant preview allowed")
	}
	badTimeout := 60
	if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{TimeoutSeconds: &badTimeout}); err == nil {
		t.Fatal("invalid timeout accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET timeout_seconds=interval_seconds WHERE id=$1`, created.ID); err == nil {
		t.Fatal("migration did not enforce active timeout")
	}
}
