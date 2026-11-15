package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// TestHTTPRequestSecretsRoundTrip walks an http monitor's credentials through
// create, masked read, an update that round-trips the masks, an update from a
// client that knows nothing of secret_headers, and an explicit clear.
func TestHTTPRequestSecretsRoundTrip(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)
	ctx := context.Background()
	tenantID := uuid.New()

	// A plaintext Authorization in `headers` is moved to secret_headers on
	// write, so API/MCP/import clients get it encrypted too.
	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP,
		Config: json.RawMessage(`{"url":"https://api.example.test","method":"GET",
			"headers":{"Accept":"application/json","Authorization":"Bearer one"},
			"secret_headers":{"X-Api-Key":"key-1"},
			"basic_auth_username":"","basic_auth_password":""}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	stored := decodeConfig(t, repo.createdConfig)
	plainHeaders, _ := stored["headers"].(map[string]any)
	if _, leaked := plainHeaders["Authorization"]; leaked || plainHeaders["Accept"] != "application/json" {
		t.Fatalf("stored plain headers = %v, want only Accept", plainHeaders)
	}
	storedSecret, _ := stored["secret_headers"].(map[string]any)
	for name, want := range map[string]string{"Authorization": "Bearer one", "X-Api-Key": "key-1"} {
		ct, _ := storedSecret[name].(string)
		if !secrets.LooksLikeEnvelope(ct) {
			t.Fatalf("stored secret_headers[%s] not encrypted: %q", name, ct)
		}
		if plain, err := enc.Decrypt(ct); err != nil || plain != want {
			t.Fatalf("secret_headers[%s] decrypts to %q (err %v), want %q", name, plain, err, want)
		}
	}

	read := decodeConfig(t, created.Config)
	readSecret, _ := read["secret_headers"].(map[string]any)
	if readSecret["Authorization"] != secrets.MaskedSecret || readSecret["X-Api-Key"] != secrets.MaskedSecret {
		t.Fatalf("read secret_headers = %v, want masked", readSecret)
	}

	// Round-trip the masked read, rotating the key.
	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.createdConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://api.example.test","method":"GET",
			"headers":{"Accept":"application/json"},
			"secret_headers":{"Authorization":"***","X-Api-Key":"key-2"}}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	updated := decodeConfig(t, repo.lastUpdateConfig)
	updatedSecret, _ := updated["secret_headers"].(map[string]any)
	if plain, _ := enc.Decrypt(updatedSecret["Authorization"].(string)); plain != "Bearer one" {
		t.Fatalf("\"***\" did not keep Authorization: %q", plain)
	}
	if plain, _ := enc.Decrypt(updatedSecret["X-Api-Key"].(string)); plain != "key-2" {
		t.Fatalf("X-Api-Key not rotated: %q", plain)
	}

	// Swap the Authorization header for basic auth: the submitted map leaves
	// it out, so it is removed.
	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.lastUpdateConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://api.example.test","method":"GET",
			"headers":{"Accept":"application/json"},
			"secret_headers":{"X-Api-Key":"***"},
			"basic_auth_username":"ops","basic_auth_password":"pw"}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor (basic auth): %v", err)
	}
	updated = decodeConfig(t, repo.lastUpdateConfig)
	pwCT, _ := updated["basic_auth_password"].(string)
	if plain, err := enc.Decrypt(pwCT); !secrets.LooksLikeEnvelope(pwCT) || err != nil || plain != "pw" {
		t.Fatalf("basic_auth_password stored as %q (plain %q, err %v)", pwCT, plain, err)
	}

	// A client that never heard of secret_headers sends a new token in
	// plain headers: it is moved, and the other stored secret headers stay
	// (an absent secret map keeps the stored one before the move edits it).
	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.lastUpdateConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://api.example.test","method":"GET",
			"headers":{"X-Auth-Token":"tok"},"basic_auth_username":"ops"}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor (legacy client): %v", err)
	}
	legacy := decodeConfig(t, repo.lastUpdateConfig)
	legacySecret, _ := legacy["secret_headers"].(map[string]any)
	if len(legacySecret) != 2 || legacySecret["X-Api-Key"] == nil || legacySecret["X-Auth-Token"] == nil {
		t.Fatalf("secret_headers after legacy update = %v, want X-Api-Key and X-Auth-Token", legacySecret)
	}
	if _, plainLeft := legacy["headers"]; plainLeft {
		t.Fatalf("plain headers should be empty after the move: %v", legacy["headers"])
	}
	if pw, _ := legacy["basic_auth_password"].(string); !secrets.LooksLikeEnvelope(pw) {
		t.Fatalf("absent basic_auth_password must keep the stored one, got %q", pw)
	}

	// Explicit clears: "" for the password, an empty map for the headers.
	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.lastUpdateConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://api.example.test","method":"GET",
			"secret_headers":{},"basic_auth_username":"","basic_auth_password":""}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor (clear): %v", err)
	}
	cleared := decodeConfig(t, repo.lastUpdateConfig)
	for _, field := range []string{"secret_headers", "basic_auth_password"} {
		if v, present := cleared[field]; present {
			t.Fatalf("%s = %v after clear, want it removed", field, v)
		}
	}
}

// TestGRPCCredentialMetadataIsEncryptedOnWrite: a REST/MCP/import client that
// puts a token in plain grpc metadata still gets it encrypted and masked.
func TestGRPCCredentialMetadataIsEncryptedOnWrite(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)

	created, err := svc.CreateMonitor(context.Background(), uuid.New(), &models.CreateMonitorRequest{
		Name: "grpc", Type: models.MonitorTypeGRPC,
		Config:          json.RawMessage(`{"host":"g.test","metadata":{"x-tenant":"t1","authorization":"Bearer g"}}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	stored := decodeConfig(t, repo.createdConfig)
	if md, _ := stored["metadata"].(map[string]any); len(md) != 1 || md["x-tenant"] != "t1" {
		t.Fatalf("stored metadata = %v, want only x-tenant", stored["metadata"])
	}
	ct, _ := stored["secret_metadata"].(map[string]any)["authorization"].(string)
	if plain, err := enc.Decrypt(ct); !secrets.LooksLikeEnvelope(ct) || err != nil || plain != "Bearer g" {
		t.Fatalf("secret_metadata.authorization stored as %q (plain %q, err %v)", ct, plain, err)
	}
	if read := decodeConfig(t, created.Config)["secret_metadata"].(map[string]any); read["authorization"] != secrets.MaskedSecret {
		t.Fatalf("read secret_metadata = %v, want masked", read)
	}
}

// TestUpdateMonitor_RefusesConflictWithKeptSecret: validation sees only the
// request, where an omitted secret_headers reads as absent; the merge then
// restores a stored Authorization header that basic auth would silently
// override. The service re-checks the merged config and refuses it.
func TestUpdateMonitor_RefusesConflictWithKeptSecret(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)
	ctx := context.Background()
	tenantID := uuid.New()

	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP,
		Config:          json.RawMessage(`{"url":"https://x.test","method":"GET","secret_headers":{"Authorization":"Bearer one"}}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.createdConfig...)

	_, err = svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"pw"}`),
	})
	if !errors.Is(err, ErrSecretConflict) || !strings.Contains(err.Error(), "Authorization header") {
		t.Fatalf("err = %v, want ErrSecretConflict naming the Authorization header", err)
	}

	// The same for grpc: a plain metadata key beside a kept secret one.
	grpcMonitor, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name: "grpc", Type: models.MonitorTypeGRPC,
		Config:          json.RawMessage(`{"host":"g.test","secret_metadata":{"x-session":"s"}}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor grpc: %v", err)
	}
	repo.monitors[grpcMonitor.ID].Config = append(json.RawMessage(nil), repo.createdConfig...)
	_, err = svc.UpdateMonitor(ctx, tenantID, grpcMonitor.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"host":"g.test","metadata":{"X-Session":"plain"}}`),
	})
	if !errors.Is(err, ErrSecretConflict) {
		t.Fatalf("grpc err = %v, want ErrSecretConflict", err)
	}
}

// TestSyntheticAPIStepCredentialIsMovedToSecretVariables: a literal
// credential header in a nested step cannot be reached by the registry, so
// the write path turns it into a generated secret variable and a {{ref}}.
func TestSyntheticAPIStepCredentialIsMovedToSecretVariables(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)

	created, err := svc.CreateMonitor(context.Background(), uuid.New(), &models.CreateMonitorRequest{
		Name: "journey", Type: models.MonitorTypeSyntheticAPI,
		Config: json.RawMessage(`{"secret_variables":{"pw":"hunter22"},"steps":[{"id":"login","request":{"method":"POST","url":"https://x.test/login",
			"headers":{"Authorization":"Bearer literal","X-Trace":"t"},"body":"{\"p\":\"{{pw}}\"}"}}]}`),
		IntervalSeconds: 60, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	stored := decodeConfig(t, repo.createdConfig)
	headers := stored["steps"].([]any)[0].(map[string]any)["request"].(map[string]any)["headers"].(map[string]any)
	if headers["Authorization"] != "{{login_authorization}}" || headers["X-Trace"] != "t" {
		t.Fatalf("stored step headers = %v", headers)
	}
	vars := stored["secret_variables"].(map[string]any)
	for name, want := range map[string]string{"pw": "hunter22", "login_authorization": "Bearer literal"} {
		ct, _ := vars[name].(string)
		if plain, err := enc.Decrypt(ct); !secrets.LooksLikeEnvelope(ct) || err != nil || plain != want {
			t.Fatalf("secret_variables[%s] stored as %q (plain %q, err %v)", name, ct, plain, err)
		}
	}
	if strings.Contains(string(created.Config), "hunter22") || strings.Contains(string(created.Config), "Bearer literal") {
		t.Fatalf("read config leaks a secret: %s", created.Config)
	}
}

// TestCredentialNamedVariablesAreMovedOnWrite: an API/MCP create that puts a
// password in plain synthetic `variables` gets it encrypted and masked, for
// API and browser journeys alike: the write path applies the backfill's rule.
func TestCredentialNamedVariablesAreMovedOnWrite(t *testing.T) {
	enc := testEncryptor(t)
	for _, tc := range []struct {
		monitorType models.MonitorType
		config      string
	}{
		{models.MonitorTypeSyntheticAPI, `{"variables":{"user":"u","password":"pw-1"},"steps":[{"id":"a","request":{"method":"POST","url":"https://x.test","body":"{{password}}"}}]}`},
		{models.MonitorTypeSyntheticBrowser, `{"start_url":"https://x.test/login","variables":{"user":"u","password":"pw-1"},"steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`},
	} {
		t.Run(string(tc.monitorType), func(t *testing.T) {
			repo := &copyingRepo{MockRepository: NewMockRepository()}
			svc := NewService(repo)
			svc.ConfigureEncryption(enc)
			created, err := svc.CreateMonitor(context.Background(), uuid.New(), &models.CreateMonitorRequest{
				Name: "journey", Type: tc.monitorType, Config: json.RawMessage(tc.config),
				IntervalSeconds: 60, TimeoutSeconds: 30,
			})
			if err != nil {
				t.Fatalf("CreateMonitor: %v", err)
			}
			stored := decodeConfig(t, repo.createdConfig)
			if vars := stored["variables"].(map[string]any); len(vars) != 1 || vars["user"] != "u" {
				t.Fatalf("stored variables = %v, want only user", vars)
			}
			ct, _ := stored["secret_variables"].(map[string]any)["password"].(string)
			if plain, err := enc.Decrypt(ct); !secrets.LooksLikeEnvelope(ct) || err != nil || plain != "pw-1" {
				t.Fatalf("secret password stored as %q (plain %q, err %v)", ct, plain, err)
			}
			if strings.Contains(string(created.Config), "pw-1") {
				t.Fatalf("read config leaks the password: %s", created.Config)
			}
		})
	}
}

// TestSyntheticBrowserSecretVariablesRoundTrip: create, masked read, an
// update that round-trips the mask keeps the stored password, "" clears it.
func TestSyntheticBrowserSecretVariablesRoundTrip(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)
	ctx := context.Background()
	tenantID := uuid.New()
	const base = `"start_url":"https://x.test/login","steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]`

	created, err := svc.CreateMonitor(ctx, tenantID, &models.CreateMonitorRequest{
		Name: "login", Type: models.MonitorTypeSyntheticBrowser,
		Config:          json.RawMessage(`{` + base + `,"secret_variables":{"password":"pw-1"}}`),
		IntervalSeconds: 60, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if read := decodeConfig(t, created.Config)["secret_variables"].(map[string]any); read["password"] != secrets.MaskedSecret {
		t.Fatalf("read secret_variables = %v, want masked", read)
	}

	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.createdConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{` + base + `,"secret_variables":{"password":"***"}}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor (keep): %v", err)
	}
	ct := decodeConfig(t, repo.lastUpdateConfig)["secret_variables"].(map[string]any)["password"].(string)
	if plain, _ := enc.Decrypt(ct); plain != "pw-1" {
		t.Fatalf("\"***\" did not keep the password: %q", plain)
	}

	repo.monitors[created.ID].Config = append(json.RawMessage(nil), repo.lastUpdateConfig...)
	if _, err := svc.UpdateMonitor(ctx, tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{` + base + `,"secret_variables":{"password":""}}`),
	}); err != nil {
		t.Fatalf("UpdateMonitor (clear): %v", err)
	}
	if v, present := decodeConfig(t, repo.lastUpdateConfig)["secret_variables"]; present {
		t.Fatalf("secret_variables = %v after clear, want it removed", v)
	}
}
