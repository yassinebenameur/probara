package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func testEncryptor(t *testing.T) secrets.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	kp, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	return secrets.NewAESGCMEncryptor(kp)
}

func TestMigrateSensitiveHeaders_DryRunThenApply(t *testing.T) {
	ctx := context.Background()
	client, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()
	enc := testEncryptor(t)
	tenantID := testutil.InsertTenant(ctx, t, client, "backfill")

	insert := func(monitorType, config string, deleted bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		deletedAt := "NULL"
		if deleted {
			deletedAt = "NOW()"
		}
		if _, err := client.ExecContext(ctx, `
			INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
				enabled, tags, created_at, updated_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, 60, 30, TRUE, ARRAY[]::text[], NOW(), NOW(), `+deletedAt+`)
		`, id, tenantID, id.String(), monitorType, config); err != nil {
			t.Fatalf("insert %s monitor: %v", monitorType, err)
		}
		return id
	}
	readConfig := func(id uuid.UUID) map[string]any {
		t.Helper()
		var raw []byte
		if err := client.QueryRowContext(ctx, `SELECT config FROM monitors WHERE id = $1`, id).Scan(&raw); err != nil {
			t.Fatalf("read config: %v", err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		return cfg
	}

	httpID := insert("http", `{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*","Authorization":"Bearer legacy"}}`, false)
	cleanID := insert("http", `{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"}}`, false)
	synthID := insert("synthetic_api", `{"steps":[{"id":"login","request":{"method":"GET","url":"https://x.test","headers":{"X-Api-Key":"literal-key"}}}]}`, false)
	deletedID := insert("http", `{"url":"https://x.test","method":"GET","headers":{"Cookie":"sid=1"}}`, true)
	browserID := insert("synthetic_browser", `{"start_url":"https://x.test/login","variables":{"username":"u","password":"change-me"},"steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`, false)
	grpcID := insert("grpc", `{"host":"g.test","metadata":{"authorization":"Bearer g","x-tenant":"t"}}`, false)
	wsID := insert("websocket", `{"url":"wss://x.test","headers":{"Authorization":"ws"}}`, false)

	var logs []string
	logf := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	dry, err := migrate(ctx, client.DB, enc, false, logf)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Processed != 5 || dry.Updated != 4 || dry.Skipped != 1 || dry.Errored != 0 {
		t.Fatalf("dry run stats = %+v, want processed 5 / updated 4 / skipped 1", dry)
	}
	if h := readConfig(httpID)["headers"].(map[string]any); h["Authorization"] != "Bearer legacy" {
		t.Fatalf("dry run wrote the http monitor: %v", h)
	}

	applied, err := migrate(ctx, client.DB, enc, true, logf)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Updated != 4 || applied.Errored != 0 {
		t.Fatalf("apply stats = %+v", applied)
	}

	httpCfg := readConfig(httpID)
	if h := httpCfg["headers"].(map[string]any); len(h) != 1 || h["Accept"] != "*/*" {
		t.Fatalf("http plain headers after apply = %v", h)
	}
	ct := httpCfg["secret_headers"].(map[string]any)["Authorization"].(string)
	if plain, err := enc.Decrypt(ct); !secrets.LooksLikeEnvelope(ct) || err != nil || plain != "Bearer legacy" {
		t.Fatalf("secret Authorization = %q (plain %q, err %v)", ct, plain, err)
	}

	synthCfg := readConfig(synthID)
	step := synthCfg["steps"].([]any)[0].(map[string]any)
	headerRef := step["request"].(map[string]any)["headers"].(map[string]any)["X-Api-Key"]
	if headerRef != "{{login_x_api_key}}" {
		t.Fatalf("synthetic header = %v, want a {{ref}}", headerRef)
	}
	varCT := synthCfg["secret_variables"].(map[string]any)["login_x_api_key"].(string)
	if plain, err := enc.Decrypt(varCT); !secrets.LooksLikeEnvelope(varCT) || err != nil || plain != "literal-key" {
		t.Fatalf("secret variable = %q (plain %q, err %v)", varCT, plain, err)
	}

	// The old login template's plain password becomes a secret variable of
	// the same name, so {{password}} still resolves.
	browserCfg := readConfig(browserID)
	if vars := browserCfg["variables"].(map[string]any); len(vars) != 1 || vars["username"] != "u" {
		t.Fatalf("browser variables = %v, want only username", vars)
	}
	pwCT := browserCfg["secret_variables"].(map[string]any)["password"].(string)
	if plain, err := enc.Decrypt(pwCT); !secrets.LooksLikeEnvelope(pwCT) || err != nil || plain != "change-me" {
		t.Fatalf("secret password = %q (plain %q, err %v)", pwCT, plain, err)
	}
	if browserCfg["steps"].([]any)[0].(map[string]any)["value"] != "{{password}}" {
		t.Fatal("the {{password}} reference changed")
	}

	grpcCfg := readConfig(grpcID)
	mdCT := grpcCfg["secret_metadata"].(map[string]any)["authorization"].(string)
	if plain, err := enc.Decrypt(mdCT); err != nil || plain != "Bearer g" {
		t.Fatalf("secret metadata = %q (plain %q, err %v)", mdCT, plain, err)
	}
	if md := grpcCfg["metadata"].(map[string]any); len(md) != 1 || md["x-tenant"] != "t" {
		t.Fatalf("grpc metadata = %v, want only x-tenant", md)
	}

	if _, moved := readConfig(cleanID)["secret_headers"]; moved {
		t.Fatal("a monitor without sensitive headers was rewritten")
	}
	if h := readConfig(deletedID)["headers"].(map[string]any); h["Cookie"] != "sid=1" {
		t.Fatal("a deleted monitor was migrated")
	}
	if h := readConfig(wsID)["headers"].(map[string]any); h["Authorization"] != "ws" {
		t.Fatal("a websocket monitor was touched")
	}

	again, err := migrate(ctx, client.DB, enc, true, logf)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if again.Updated != 0 || again.Skipped != 5 {
		t.Fatalf("second apply stats = %+v, want nothing to do", again)
	}

	for _, line := range logs {
		if strings.Contains(line, "legacy") || strings.Contains(line, "literal-key") || strings.Contains(line, "change-me") {
			t.Fatalf("a log line carries a secret: %q", line)
		}
	}
}
