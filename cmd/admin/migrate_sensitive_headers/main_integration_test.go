//go:build !unit

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
	synthVarID := insert("synthetic_api", `{"variables":{"username":"u","password":"change-me"},"steps":[{"id":"login","request":{"method":"POST","url":"https://x.test/login","body":"{\"u\":\"{{username}}\",\"p\":\"{{password}}\"}"}}]}`, false)
	browserID := insert("synthetic_browser", `{"start_url":"https://x.test/login","variables":{"password":"browser-pw"},"steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`, false)
	grpcID := insert("grpc", `{"host":"g.test","metadata":{"authorization":"Bearer g","x-tenant":"t"}}`, false)
	wsID := insert("websocket", `{"url":"wss://x.test","headers":{"Authorization":"ws"}}`, false)
	// Keys stored miscased before validation refused them.
	miscasedHTTPID := insert("http", `{"url":"https://x.test","method":"GET","Headers":{"Authorization":"Bearer miscased","Accept":"*/*"}}`, false)
	miscasedSynthID := insert("synthetic_api", `{"steps":[{"id":"a","Request":{"method":"GET","url":"https://x.test","Headers":{"X-Api-Key":"miscased-step-key"}}}]}`, false)
	bothSpellingsID := insert("http", `{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"},"Headers":{"Cookie":"both=1"}}`, false)
	conflictID := insert("http", `{"url":"https://x.test","method":"GET","headers":{"Authorization":"Bearer one"},"Headers":{"Authorization":"Bearer two"}}`, false)
	postgresID := insert("postgres", `{"host":"db.test","Password":"pg-miscased"}`, false)
	unmovableID := insert("synthetic_api", `{"variables":{"Password":"unmovable-one","password":"unmovable-two"},"steps":[]}`, false)

	var logs []string
	logf := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	dry, err := migrate(ctx, client.DB, enc, false, logf)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Processed != 14 || dry.Updated != 10 || dry.Skipped != 3 || dry.Conflicted != 1 || dry.Unmovable != 1 || dry.Errored != 0 {
		t.Fatalf("dry run stats = %+v, want processed 14 / updated 10 / skipped 3 / conflicted 1 / unmovable 1", dry)
	}
	if h := readConfig(httpID)["headers"].(map[string]any); h["Authorization"] != "Bearer legacy" {
		t.Fatalf("dry run wrote the http monitor: %v", h)
	}

	applied, err := migrate(ctx, client.DB, enc, true, logf)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Updated != 10 || applied.Conflicted != 1 || applied.Errored != 0 {
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

	// A credential-named plain variable becomes a secret variable of the
	// same name, so {{password}} still resolves.
	varCfg := readConfig(synthVarID)
	if vars := varCfg["variables"].(map[string]any); len(vars) != 1 || vars["username"] != "u" {
		t.Fatalf("synthetic variables = %v, want only username", vars)
	}
	pwCT := varCfg["secret_variables"].(map[string]any)["password"].(string)
	if plain, err := enc.Decrypt(pwCT); !secrets.LooksLikeEnvelope(pwCT) || err != nil || plain != "change-me" {
		t.Fatalf("secret password = %q (plain %q, err %v)", pwCT, plain, err)
	}
	if body := varCfg["steps"].([]any)[0].(map[string]any)["request"].(map[string]any)["body"]; !strings.Contains(body.(string), "{{password}}") {
		t.Fatal("the {{password}} reference changed")
	}
	// A browser journey's credential-named variable moves the same way, and
	// its {{password}} fill keeps resolving.
	browserCfg := readConfig(browserID)
	if _, plainLeft := browserCfg["variables"]; plainLeft {
		t.Fatalf("browser variables = %v, want the password moved", browserCfg["variables"])
	}
	browserCT := browserCfg["secret_variables"].(map[string]any)["password"].(string)
	if plain, err := enc.Decrypt(browserCT); !secrets.LooksLikeEnvelope(browserCT) || err != nil || plain != "browser-pw" {
		t.Fatalf("browser secret password = %q (plain %q, err %v)", browserCT, plain, err)
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
	// A soft-deleted monitor's row is still at rest, so it is migrated too.
	deletedCfg := readConfig(deletedID)
	if _, plainLeft := deletedCfg["headers"]; plainLeft {
		t.Fatalf("a deleted monitor kept its plaintext Cookie: %v", deletedCfg)
	}
	if ct := deletedCfg["secret_headers"].(map[string]any)["Cookie"].(string); !secrets.LooksLikeEnvelope(ct) {
		t.Fatalf("deleted monitor Cookie not encrypted: %q", ct)
	}
	if h := readConfig(wsID)["headers"].(map[string]any); h["Authorization"] != "ws" {
		t.Fatal("a websocket monitor was touched")
	}

	// Miscased keys are renamed to their exact spelling first, then
	// normalized and encrypted like any other row.
	decrypted := func(cfg map[string]any, field, key string) string {
		t.Helper()
		values, _ := cfg[field].(map[string]any)
		ct, _ := values[key].(string)
		plain, err := enc.Decrypt(ct)
		if !secrets.LooksLikeEnvelope(ct) || err != nil {
			t.Fatalf("%s.%s = %q (err %v), want an envelope", field, key, ct, err)
		}
		return plain
	}
	miscasedHTTP := readConfig(miscasedHTTPID)
	if _, left := miscasedHTTP["Headers"]; left || miscasedHTTP["headers"].(map[string]any)["Accept"] != "*/*" {
		t.Fatalf("miscased http after apply = %v", miscasedHTTP)
	}
	if decrypted(miscasedHTTP, "secret_headers", "Authorization") != "Bearer miscased" {
		t.Fatal("miscased Authorization not moved")
	}
	miscasedSynth := readConfig(miscasedSynthID)
	miscasedStep := miscasedSynth["steps"].([]any)[0].(map[string]any)
	if _, left := miscasedStep["Request"]; left {
		t.Fatalf("step Request not renamed: %v", miscasedStep)
	}
	if miscasedStep["request"].(map[string]any)["headers"].(map[string]any)["X-Api-Key"] != "{{a_x_api_key}}" {
		t.Fatalf("miscased step header = %v", miscasedStep)
	}
	if decrypted(miscasedSynth, "secret_variables", "a_x_api_key") != "miscased-step-key" {
		t.Fatal("miscased step header not moved")
	}
	both := readConfig(bothSpellingsID)
	if _, left := both["Headers"]; left || both["headers"].(map[string]any)["Accept"] != "*/*" || decrypted(both, "secret_headers", "Cookie") != "both=1" {
		t.Fatalf("two agreeing spellings were not merged: %v", both)
	}
	if conflict := readConfig(conflictID); conflict["Headers"] == nil || conflict["headers"].(map[string]any)["Authorization"] != "Bearer one" {
		t.Fatalf("a conflicting row was changed: %v", conflict)
	}
	pg := readConfig(postgresID)
	if _, left := pg["Password"]; left {
		t.Fatalf("postgres Password not renamed: %v", pg)
	}
	if ct, _ := pg["password"].(string); !secrets.LooksLikeEnvelope(ct) {
		t.Fatalf("postgres password = %q, want encrypted", ct)
	}
	if vars := readConfig(unmovableID)["variables"].(map[string]any); vars["Password"] != "unmovable-one" || vars["password"] != "unmovable-two" {
		t.Fatalf("unmovable variables changed: %v", vars)
	}
	var reported bool
	for _, line := range logs {
		if strings.Contains(line, "[conflict] monitor "+conflictID.String()) && strings.Contains(line, "Headers") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the conflict was not reported by monitor id: %q", logs)
	}

	again, err := migrate(ctx, client.DB, enc, true, logf)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if again.Updated != 0 || again.Skipped != 13 || again.Conflicted != 1 {
		t.Fatalf("second apply stats = %+v, want nothing to do", again)
	}

	for _, line := range logs {
		if strings.Contains(line, "legacy") || strings.Contains(line, "literal-key") || strings.Contains(line, "change-me") || strings.Contains(line, "Bearer g") || strings.Contains(line, "browser-pw") || strings.Contains(line, "sid=1") ||
			strings.Contains(line, "Bearer miscased") || strings.Contains(line, "miscased-step-key") || strings.Contains(line, "both=1") ||
			strings.Contains(line, "Bearer one") || strings.Contains(line, "Bearer two") || strings.Contains(line, "pg-miscased") || strings.Contains(line, "unmovable-") {
			t.Fatalf("a log line carries a secret: %q", line)
		}
	}
}
