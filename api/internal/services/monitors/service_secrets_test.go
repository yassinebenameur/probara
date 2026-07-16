package monitors

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// copyingRepo mimics the real repository's row semantics on top of
// MockRepository: callers get copies, not pointers into storage, and the
// config passed to Create/Update is captured for assertions.
type copyingRepo struct {
	*MockRepository
	createdConfig    json.RawMessage
	lastUpdateConfig json.RawMessage
}

func (r *copyingRepo) Create(ctx context.Context, monitor *models.Monitor) error {
	r.createdConfig = append(json.RawMessage(nil), monitor.Config...)
	clone := *monitor
	return r.MockRepository.Create(ctx, &clone)
}

func (r *copyingRepo) GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, err := r.MockRepository.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}
	clone := *monitor
	clone.Config = append(json.RawMessage(nil), monitor.Config...)
	return &clone, nil
}

func (r *copyingRepo) Update(ctx context.Context, monitor *models.Monitor, setParts []string, args []interface{}) error {
	for _, arg := range args {
		if cfg, ok := arg.(json.RawMessage); ok {
			r.lastUpdateConfig = append(json.RawMessage(nil), cfg...)
		}
	}
	return nil
}

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

func decodeConfig(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return m
}

func TestMonitorSecretLifecycle(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)

	tenantID := uuid.New()
	created, err := svc.CreateMonitor(context.Background(), tenantID, &models.CreateMonitorRequest{
		Name:            "redis prod",
		Type:            models.MonitorTypeRedis,
		Config:          json.RawMessage(`{"host":"redis.internal","port":6379,"password":"hunter2"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// Stored config must be encrypted, never plaintext.
	storedPW, _ := decodeConfig(t, repo.createdConfig)["password"].(string)
	if !secrets.LooksLikeEnvelope(storedPW) {
		t.Fatalf("stored password not encrypted: %q", storedPW)
	}

	// Returned and fetched configs must be masked.
	if pw := decodeConfig(t, created.Config)["password"]; pw != secrets.MaskedSecret {
		t.Fatalf("create response password = %v, want masked", pw)
	}
	fetched, err := svc.GetMonitor(context.Background(), tenantID, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if pw := decodeConfig(t, fetched.Config)["password"]; pw != secrets.MaskedSecret {
		t.Fatalf("get response password = %v, want masked", pw)
	}

	list, err := svc.ListMonitors(context.Background(), tenantID, nil, nil, 1, 20)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if pw := decodeConfig(t, list.Items[0].Config)["password"]; pw != secrets.MaskedSecret {
		t.Fatalf("list response password = %v, want masked", pw)
	}

	// Updating with the masked placeholder keeps the stored secret.
	_, err = svc.UpdateMonitor(context.Background(), tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"host":"redis-new.internal","port":6379,"password":"***"}`),
	})
	if err != nil {
		t.Fatalf("UpdateMonitor (keep secret): %v", err)
	}
	updated := decodeConfig(t, repo.lastUpdateConfig)
	if updated["host"] != "redis-new.internal" {
		t.Fatalf("host not updated: %v", updated["host"])
	}
	plain, err := enc.Decrypt(updated["password"].(string))
	if err != nil || plain != "hunter2" {
		t.Fatalf("preserved password decrypts to %q (err %v), want hunter2", plain, err)
	}

	// Updating with a new password re-encrypts the new value.
	_, err = svc.UpdateMonitor(context.Background(), tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"host":"redis-new.internal","password":"rotated"}`),
	})
	if err != nil {
		t.Fatalf("UpdateMonitor (rotate secret): %v", err)
	}
	rotated := decodeConfig(t, repo.lastUpdateConfig)
	rotatedPW, _ := rotated["password"].(string)
	if !secrets.LooksLikeEnvelope(rotatedPW) {
		t.Fatalf("rotated password not encrypted: %q", rotatedPW)
	}
	if plain, err := enc.Decrypt(rotatedPW); err != nil || plain != "rotated" {
		t.Fatalf("rotated password decrypts to %q (err %v), want rotated", plain, err)
	}
}

func TestMonitorSecretLifecycle_NonSecretTypeUntouched(t *testing.T) {
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(testEncryptor(t))

	tenantID := uuid.New()
	config := `{"url":"https://example.com","method":"GET"}`
	created, err := svc.CreateMonitor(context.Background(), tenantID, &models.CreateMonitorRequest{
		Name:            "http",
		Type:            models.MonitorTypeHTTP,
		Config:          json.RawMessage(config),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if string(repo.createdConfig) != config {
		t.Fatalf("http config modified at rest: %s", repo.createdConfig)
	}
	if string(created.Config) != config {
		t.Fatalf("http config modified in response: %s", created.Config)
	}
}

func TestWebSocketHeaderSecretLifecycle(t *testing.T) {
	enc := testEncryptor(t)
	repo := &copyingRepo{MockRepository: NewMockRepository()}
	svc := NewService(repo)
	svc.ConfigureEncryption(enc)

	tenantID := uuid.New()
	created, err := svc.CreateMonitor(context.Background(), tenantID, &models.CreateMonitorRequest{
		Name:            "authenticated socket",
		Type:            models.MonitorTypeWebSocket,
		Config:          json.RawMessage(`{"url":"wss://example.test/socket","headers":{"Authorization":"Bearer original","Origin":"https://app.test"}}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	storedHeaders := decodeConfig(t, repo.createdConfig)["headers"].(map[string]any)
	for name := range storedHeaders {
		if value, _ := storedHeaders[name].(string); !secrets.LooksLikeEnvelope(value) {
			t.Fatalf("stored header %q not encrypted: %v", name, storedHeaders[name])
		}
	}
	responseHeaders := decodeConfig(t, created.Config)["headers"].(map[string]any)
	for name := range responseHeaders {
		if responseHeaders[name] != secrets.MaskedSecret {
			t.Fatalf("response header %q = %v, want masked", name, responseHeaders[name])
		}
	}

	// Edit-mode tests resolve a write-only placeholder to ciphertext; the
	// worker's standard decrypt boundary then restores the handshake value.
	resolved, err := svc.ResolveTestConfig(
		context.Background(), tenantID, &created.ID, models.MonitorTypeWebSocket,
		json.RawMessage(`{"url":"wss://example.test/socket","headers":{"Authorization":"***"}}`),
	)
	if err != nil {
		t.Fatalf("ResolveTestConfig: %v", err)
	}
	decrypted, err := secrets.DecryptMonitorConfig(enc, "websocket", resolved)
	if err != nil {
		t.Fatalf("DecryptMonitorConfig: %v", err)
	}
	if got := decodeConfig(t, decrypted)["headers"].(map[string]any)["Authorization"]; got != "Bearer original" {
		t.Fatalf("resolved Authorization = %v, want original value", got)
	}

	_, err = svc.UpdateMonitor(context.Background(), tenantID, created.ID, &models.UpdateMonitorRequest{
		Config: json.RawMessage(`{"url":"wss://new.example.test/socket","headers":{"Authorization":"***","X-API-Key":"rotated"}}`),
	})
	if err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	updatedHeaders := decodeConfig(t, repo.lastUpdateConfig)["headers"].(map[string]any)
	authPlain, err := enc.Decrypt(updatedHeaders["Authorization"].(string))
	if err != nil || authPlain != "Bearer original" {
		t.Fatalf("preserved Authorization = %q (err %v)", authPlain, err)
	}
	apiKeyPlain, err := enc.Decrypt(updatedHeaders["X-API-Key"].(string))
	if err != nil || apiKeyPlain != "rotated" {
		t.Fatalf("rotated X-API-Key = %q (err %v)", apiKeyPlain, err)
	}
	if _, ok := updatedHeaders["Origin"]; ok {
		t.Fatal("omitted Origin header should be removed")
	}
}
