package importservice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestExecuteImport_MaskedRequestSecretsNeverPersist runs a re-imported
// portable export, whose request credentials read back as "***", through the
// real monitor service and Postgres: every monitor is created, no "***" is
// stored (encrypted or not) as if it were the credential, and the
// non-secret parts survive.
func TestExecuteImport_MaskedRequestSecretsNeverPersist(t *testing.T) {
	ctx := context.Background()
	dbClient, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "import-secrets")

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	kp, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	enc := secrets.NewAESGCMEncryptor(kp)

	monitorSvc := monitorservice.NewService(monitorservice.NewPostgresRepository(dbClient))
	monitorSvc.ConfigureEncryption(enc)
	svc := NewService(dbClient, monitorSvc, groupservice.NewService(dbClient))

	req := &models.ImportExecuteRequest{
		Rows: []models.ImportRow{
			rowWithConfig(0, "API", "http", map[string]interface{}{
				"url": "https://example.com/health", "method": "GET",
				"headers":             map[string]interface{}{"Accept": "application/json"},
				"secret_headers":      map[string]interface{}{"X-Api-Key": "***"},
				"basic_auth_username": "ops", "basic_auth_password": "***",
			}, nil, 60, 30),
			rowWithConfig(1, "gRPC", "grpc", map[string]interface{}{
				"host": "grpc.example.com", "metadata": map[string]interface{}{"x-tenant": "t1"},
				"secret_metadata": map[string]interface{}{"authorization": "***"},
			}, nil, 60, 30),
			rowWithConfig(2, "Journey", "synthetic_api", map[string]interface{}{
				"secret_variables": map[string]interface{}{"token": "***"},
				"steps": []interface{}{map[string]interface{}{"id": "a", "request": map[string]interface{}{
					"method": "GET", "url": "https://example.com/me", "headers": map[string]interface{}{"Authorization": "Bearer {{token}}"},
				}}},
			}, nil, 60, 30),
			rowWithConfig(3, "Login", "synthetic_browser", map[string]interface{}{
				"start_url":        "https://example.com/login",
				"variables":        map[string]interface{}{"username": "robot"},
				"secret_variables": map[string]interface{}{"password": "***"},
				"steps":            []interface{}{map[string]interface{}{"id": "p", "action": "fill", "selector": "#p", "value": "{{password}}"}},
			}, nil, 60, 30),
		},
		Mapping: models.FieldMapping{Name: "name", Type: "type", Config: "config", IntervalSeconds: "interval_seconds", TimeoutSeconds: "timeout_seconds"},
	}
	result, err := svc.ExecuteImport(ctx, tenantID, req)
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}
	if result.SuccessCount != 4 || result.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %+v", result)
	}

	rows, err := dbClient.QueryContext(ctx, `SELECT name, config FROM monitors WHERE tenant_id = $1`, tenantID)
	if err != nil {
		t.Fatalf("read monitors: %v", err)
	}
	defer rows.Close()
	stored := map[string]map[string]any{}
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		// Any envelope left would be a persisted mask: there was nothing to
		// encrypt, since every secret arrived as "***".
		if strings.Contains(string(raw), "***") || strings.Contains(string(raw), `"alg"`) {
			t.Fatalf("%s stored a masked secret: %s", name, raw)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		stored[name] = cfg
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(stored) != 4 {
		t.Fatalf("stored %d monitors, want 4", len(stored))
	}

	for name, fields := range map[string][]string{
		"API":     {"secret_headers", "basic_auth_password"},
		"gRPC":    {"secret_metadata"},
		"Journey": {"secret_variables"},
		"Login":   {"secret_variables"},
	} {
		for _, field := range fields {
			if v, present := stored[name][field]; present {
				t.Fatalf("%s kept %s = %v from a masked import, want it dropped", name, field, v)
			}
		}
	}
	if stored["API"]["basic_auth_username"] != "ops" || stored["API"]["headers"].(map[string]any)["Accept"] != "application/json" {
		t.Fatalf("API lost non-secret fields: %v", stored["API"])
	}
	if stored["gRPC"]["metadata"].(map[string]any)["x-tenant"] != "t1" {
		t.Fatalf("gRPC lost its plain metadata: %v", stored["gRPC"])
	}
	if stored["Login"]["variables"].(map[string]any)["username"] != "robot" {
		t.Fatalf("Login lost its plain variables: %v", stored["Login"])
	}
}
