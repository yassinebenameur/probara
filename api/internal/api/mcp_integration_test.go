package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/crypto/bcrypt"

	"github.com/yassinebenameur/probara/api/internal/models"
	adminusersservice "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type mcpFixture struct {
	t       *testing.T
	ctx     context.Context
	db      *db.Client
	server  *Server
	http    *httptest.Server
	tenantA uuid.UUID
	tenantB uuid.UUID
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	cfg.AdminJWTSecret = "mcp-test-secret-mcp-test-secret-32b"
	cfg.AdminAccessTTLMinutes = 15
	cfg.AdminRefreshTTLDays = 30
	cfg.AdminBcryptCost = 4
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), dbClient)
	if s.mcpErr != nil {
		t.Fatal(s.mcpErr)
	}
	s.auditRecorder.Start()
	t.Cleanup(s.auditRecorder.Stop)
	srv := httptest.NewServer(s.http.Handler)
	t.Cleanup(srv.Close)

	return &mcpFixture{
		t: t, ctx: ctx, db: dbClient, server: s, http: srv,
		tenantA: testutil.InsertTenant(ctx, t, dbClient, "Tenant A"),
		tenantB: testutil.InsertTenant(ctx, t, dbClient, "Tenant B"),
	}
}

func (f *mcpFixture) apiKey(tenant uuid.UUID, scope string) string {
	f.t.Helper()
	key := "pk_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Exercise real key validation without production-cost bcrypt work on
	// every outer MCP request and inner REST dispatch, especially under -race.
	hash, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.MinCost)
	if err != nil {
		f.t.Fatal(err)
	}
	prefix := sha256.Sum256([]byte(key))
	if _, err := f.db.ExecContext(f.ctx, `
		INSERT INTO api_keys (tenant_id, name, key_hash, key_prefix, scope) VALUES ($1, $2, $3, $4, $5)`,
		tenant, "mcp-test-"+scope, string(hash), hex.EncodeToString(prefix[:]), scope); err != nil {
		f.t.Fatal(err)
	}
	return key
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (f *mcpFixture) connect(token string) *mcp.ClientSession {
	f.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-test", Version: "1"}, nil)
	session, err := client.Connect(f.ctx, &mcp.StreamableClientTransport{
		Endpoint:             f.http.URL + "/api/v1/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	f.t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, s *mcp.ClientSession) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for tool, err := range s.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = true
	}
	return out
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPAPIKeySessions(t *testing.T) {
	f := newMCPFixture(t)
	writeKey := f.apiKey(f.tenantA, "write")
	readKey := f.apiKey(f.tenantA, "read")
	otherTenantKey := f.apiKey(f.tenantB, "write")

	writer := f.connect(writeKey)
	tools := toolNames(t, writer)
	for _, want := range []string{"list_monitors", "create_monitor", "acknowledge_alert", "describe_monitor_type", "wait_for_alert_events"} {
		if !tools[want] {
			t.Errorf("write key: tool %s missing", want)
		}
	}
	// API keys never pass tenant-admin or superadmin gates; those tools are not offered.
	for _, hidden := range []string{"create_api_key", "list_audit_log", "list_users", "update_tenant_settings"} {
		if tools[hidden] {
			t.Errorf("write key: tool %s should be hidden", hidden)
		}
	}

	out, isErr := callTool(t, writer, "create_monitor", map[string]any{
		"name": "MCP created", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"url": "https://example.com", "method": "GET"},
	})
	if isErr {
		t.Fatalf("create_monitor failed: %s", out)
	}
	var created models.Monitor
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create_monitor result: %v: %s", err, out)
	}

	out, isErr = callTool(t, writer, "get_monitor", map[string]any{"id": created.ID.String()})
	if isErr || !strings.Contains(out, "MCP created") {
		t.Fatalf("get_monitor: %v %s", isErr, out)
	}

	// Validation errors come back as tool errors carrying the API's message.
	out, isErr = callTool(t, writer, "create_monitor", map[string]any{"name": "bad", "type": "http", "config": map[string]any{}})
	if !isErr || !strings.Contains(out, "HTTP 400") {
		t.Fatalf("expected a validation tool error, got %v %s", isErr, out)
	}
	out, isErr = callTool(t, writer, "get_monitor", map[string]any{"id": created.ID.String(), "nope": 1})
	if !isErr || !strings.Contains(out, "unknown argument") {
		t.Fatalf("expected unknown-argument error, got %v %s", isErr, out)
	}

	out, isErr = callTool(t, writer, "describe_monitor_type", map[string]any{"type": "http"})
	if isErr || !strings.Contains(out, `"url"`) {
		t.Fatalf("describe_monitor_type: %v %s", isErr, out)
	}

	// Read-only key: write tools are not offered, reads work.
	reader := f.connect(readKey)
	readTools := toolNames(t, reader)
	if readTools["create_monitor"] || readTools["delete_monitor"] || readTools["get_push_info"] {
		t.Error("read key must not see write tools")
	}
	if !readTools["list_monitors"] || !readTools["test_monitor_config"] {
		t.Error("read key must see read tools, including compute-only POSTs")
	}
	if out, isErr := callTool(t, reader, "list_monitors", nil); isErr || !strings.Contains(out, "MCP created") {
		t.Fatalf("read list_monitors: %v %s", isErr, out)
	}
	if out, isErr := callTool(t, reader, "whoami", nil); isErr || !strings.Contains(out, `"can_write":false`) {
		t.Fatalf("read whoami: %v %s", isErr, out)
	}

	// Tenant isolation: another tenant's key cannot see the monitor.
	other := f.connect(otherTenantKey)
	if out, isErr := callTool(t, other, "get_monitor", map[string]any{"id": created.ID.String()}); !isErr || !strings.Contains(out, "404") {
		t.Fatalf("cross-tenant get_monitor should 404, got %v %s", isErr, out)
	}

	// The audit trail records the real route, attributed to the key, marked via MCP.
	var details string
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := f.db.QueryRowContext(f.ctx, `
			SELECT COALESCE(details::text, '') FROM audit_log
			WHERE tenant_id = $1 AND action = 'monitor.create' AND outcome = 'success' AND actor_type = 'api_key'
			ORDER BY id DESC LIMIT 1`, f.tenantA).Scan(&details)
		if err == nil || time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("audit row: %v", err)
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(strings.ReplaceAll(details, " ", ""), `"via":"mcp"`) {
		t.Fatalf("audit details missing via=mcp: %s", details)
	}
}

func TestMCPRejectsCookiesAndAdvertisesOAuth(t *testing.T) {
	f := newMCPFixture(t)
	req, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.AddCookie(&http.Cookie{Name: "admin_access", Value: "anything"})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie-only MCP request: want 401, got %d", res.StatusCode)
	}
	challenge := res.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "resource_metadata=") || !strings.Contains(challenge, "/.well-known/oauth-protected-resource/api/v1/mcp") {
		t.Fatalf("missing OAuth challenge: %q", challenge)
	}

	var prm map[string]any
	getJSON(t, f.http.URL+"/.well-known/oauth-protected-resource/api/v1/mcp", &prm)
	servers, _ := prm["authorization_servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("protected resource metadata: %v", prm)
	}
	issuer := servers[0].(string)
	u, _ := url.Parse(issuer)
	var asm map[string]any
	getJSON(t, f.http.URL+"/.well-known/oauth-authorization-server"+u.Path, &asm)
	if asm["issuer"] != issuer || asm["registration_endpoint"] == nil {
		t.Fatalf("authorization server metadata: %v", asm)
	}
	getJSON(t, issuer+"/.well-known/openid-configuration", &asm)
}

func getJSON(t *testing.T, target string, v any) {
	t.Helper()
	res, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", target, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// oauthSignIn runs the browser half of the flow as the given user: register,
// authorize, consent (cookie session), and returns the token response.
func (f *mcpFixture) oauthSignIn(username, password, scope string) map[string]any {
	t := f.t
	t.Helper()
	redirectURI := "http://127.0.0.1:53682/callback"
	var reg map[string]any
	postJSON(t, http.DefaultClient, f.http.URL+"/api/v1/oauth/register", nil,
		map[string]any{"client_name": "Test Agent", "redirect_uris": []string{redirectURI}}, http.StatusCreated, &reg)
	clientID := reg["client_id"].(string)

	verifierBytes := make([]byte, 32)
	_, _ = rand.Read(verifierBytes)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
		"scope":                 {scope},
	}.Encode()

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(f.http.URL + "/api/v1/oauth/authorize?" + query)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/oauth/consent?") {
		t.Fatalf("authorize: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	postJSON(t, browser, f.http.URL+"/api/v1/auth/login", nil,
		map[string]any{"username": username, "password": password}, http.StatusOK, nil)

	headers := map[string]string{"X-Tenant-ID": f.tenantA.String()}
	var decision map[string]string
	postJSON(t, browser, f.http.URL+"/api/v1/oauth/authorize/decision", headers,
		map[string]any{"query": query, "approve": true, "tenant_id": f.tenantA.String(), "scope": scope}, http.StatusOK, &decision)
	back, _ := url.Parse(decision["redirect_to"])
	if back.Query().Get("state") != "xyz" || back.Query().Get("code") == "" {
		t.Fatalf("decision redirect: %s", decision["redirect_to"])
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {back.Query().Get("code")},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	tokens := postForm(t, f.http.URL+"/api/v1/oauth/token", form, http.StatusOK)
	tokens["client_id"] = clientID
	tokens["_code_form"] = form.Encode()
	return tokens
}

func postJSON(t *testing.T, c *http.Client, target string, headers map[string]string, body any, want int, out any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("POST %s: want %d, got %d: %s", target, want, res.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("POST %s: %v: %s", target, err, data)
		}
	}
}

func postForm(t *testing.T, target string, form url.Values, want int) map[string]any {
	t.Helper()
	res, err := http.PostForm(target, form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("POST %s: want %d, got %d: %s", target, want, res.StatusCode, data)
	}
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func (f *mcpFixture) createUser(username, platformRole string, memberships []models.MembershipInput) {
	f.t.Helper()
	password := "correct-horse-battery"
	svc := adminusersservice.NewService(f.db, 4)
	if _, err := svc.CreateUser(f.ctx, &models.CreateAdminUserRequest{
		Username: username, Password: &password, PlatformRole: platformRole, Memberships: memberships,
	}); err != nil {
		f.t.Fatal(err)
	}
}

func TestMCPOAuthFlow(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("viewer", "member", []models.MembershipInput{{TenantID: f.tenantA, Role: "viewer"}})

	tokens := f.oauthSignIn("root", "correct-horse-battery", "write")
	access := tokens["access_token"].(string)
	session := f.connect(access)
	tools := toolNames(t, session)
	for _, want := range []string{"list_users", "create_api_key", "list_audit_log", "create_monitor", "list_tenants"} {
		if !tools[want] {
			t.Errorf("superadmin OAuth session: tool %s missing", want)
		}
	}
	if out, isErr := callTool(t, session, "whoami", nil); isErr || !strings.Contains(out, `"actor_type":"admin_user"`) || !strings.Contains(out, f.tenantA.String()) {
		t.Fatalf("whoami: %v %s", isErr, out)
	}
	if out, isErr := callTool(t, session, "list_users", nil); isErr || !strings.Contains(out, "root") {
		t.Fatalf("list_users: %v %s", isErr, out)
	}
	// tenant_id switches workspace per call.
	if out, isErr := callTool(t, session, "whoami", map[string]any{"tenant_id": f.tenantB.String()}); isErr || !strings.Contains(out, f.tenantB.String()) {
		t.Fatalf("whoami tenant B: %v %s", isErr, out)
	}

	// The token is for the MCP resource only, not the REST API.
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REST call with MCP token: want 401, got %d", res.StatusCode)
	}

	// A code is single-use; replaying it revokes what it created.
	codeForm, _ := url.ParseQuery(tokens["_code_form"].(string))
	postForm(t, f.http.URL+"/api/v1/oauth/token", codeForm, http.StatusBadRequest)
	if _, err := mcp.NewClient(&mcp.Implementation{Name: "x", Version: "1"}, nil).Connect(f.ctx, &mcp.StreamableClientTransport{
		Endpoint: f.http.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: access}},
		DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil); err == nil {
		t.Fatal("token of a replayed code must be revoked")
	}

	// Refresh rotation, and reuse of a rotated refresh token revokes the grant.
	tokens = f.oauthSignIn("root", "correct-horse-battery", "write")
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {tokens["client_id"].(string)}}
	rotated := postForm(t, f.http.URL+"/api/v1/oauth/token", refresh, http.StatusOK)
	f.connect(rotated["access_token"].(string))
	postForm(t, f.http.URL+"/api/v1/oauth/token", refresh, http.StatusBadRequest)
	postForm(t, f.http.URL+"/api/v1/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {rotated["refresh_token"].(string)}, "client_id": {tokens["client_id"].(string)},
	}, http.StatusBadRequest)

	// A read-scope grant narrows even a superadmin.
	readTokens := f.oauthSignIn("root", "correct-horse-battery", "read")
	readSession := f.connect(readTokens["access_token"].(string))
	readTools := toolNames(t, readSession)
	if readTools["create_monitor"] || readTools["create_user"] || !readTools["list_users"] {
		t.Fatalf("read-scope superadmin tools wrong: create_monitor=%v create_user=%v list_users=%v",
			readTools["create_monitor"], readTools["create_user"], readTools["list_users"])
	}

	// A viewer may connect, read, and is refused writes by the API.
	viewerTokens := f.oauthSignIn("viewer", "correct-horse-battery", "write")
	viewer := f.connect(viewerTokens["access_token"].(string))
	if out, isErr := callTool(t, viewer, "list_monitors", nil); isErr {
		t.Fatalf("viewer list_monitors: %s", out)
	}
	viewerTools := toolNames(t, viewer)
	if viewerTools["create_monitor"] || viewerTools["list_users"] {
		t.Fatal("a viewer's session must not offer write or superadmin tools")
	}
	// Tenant-admin tools are listed for users (the role is per tenant) but
	// the API still refuses a viewer.
	if out, isErr := callTool(t, viewer, "list_audit_log", nil); !isErr || !strings.Contains(out, "403") {
		t.Fatalf("viewer list_audit_log should be forbidden: %v %s", isErr, out)
	}
}

func TestMCPWaitForAlertEventsTimesOutQuietly(t *testing.T) {
	f := newMCPFixture(t)
	session := f.connect(f.apiKey(f.tenantA, "read"))
	// Assert the response through the full authenticated transport here;
	// the exact wait duration is covered with a fake clock in the MCP package.
	out, isErr := callTool(t, session, "wait_for_alert_events", map[string]any{"timeout_seconds": 1})
	if isErr {
		t.Fatalf("wait_for_alert_events: %s", out)
	}
	if !strings.Contains(out, `"events":[]`) {
		t.Fatalf("expected no events: %s", out)
	}
}
