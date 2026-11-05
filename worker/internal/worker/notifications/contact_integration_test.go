//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const directStubType = "test_worker_direct_stub"

// directPlugin records the direct recipient of each send.
type directPlugin struct {
	mu   sync.Mutex
	sent []*plugin.Recipient
}

func (p *directPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: directStubType, Capabilities: []plugin.Capability{plugin.CapabilityDirectRecipient}}
}
func (p *directPlugin) Validate(json.RawMessage) error { return nil }
func (p *directPlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, req.Recipient)
	return nil
}

var directStub = &directPlugin{}

func init() { plugin.Register(directStub) }

// An envelope with a ContactID reaches that contact's decrypted address, and
// only while the contact is verified; the address never rides on the wire.
func TestHandle_ContactIDResolvesVerifiedAddress(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	enc := secrets.NewAESGCMEncryptor(testKeys{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "direct")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID, userID, contactID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	sealed, err := enc.Encrypt("+15557654321")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
			VALUES ($1, $2, 'sms', $3, '{}'::jsonb, TRUE, NOW(), NOW())`, []any{channelID, tenantID, directStubType}},
		{`INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())`, []any{alertID, tenantID, monitorID}},
		{`INSERT INTO admin_users (id, username, password_hash) VALUES ($1, 'oncall', 'x')`, []any{userID}},
		{`INSERT INTO user_contacts (id, user_id, kind, address, verified_at) VALUES ($1, $2, 'sms', $3, NOW())`, []any{contactID, userID, sealed}},
	} {
		if _, err := dbClient.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}

	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: enc}
	message := func(contact string) *queue.Message {
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: directStubType,
			AlertID: alertID.String(), EventType: "created",
			Event:     notifications.AlertEvent{Type: "created", Timestamp: time.Now().UTC()},
			ContactID: contact,
		})
		return &queue.Message{Data: raw, NumDelivered: 1}
	}

	if err := worker.handle(ctx, message(contactID.String())); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(directStub.sent) != 1 || directStub.sent[0] == nil || directStub.sent[0].Address != "+15557654321" ||
		directStub.sent[0].ContactID != contactID.String() {
		t.Fatalf("sent = %+v, want the decrypted contact address", directStub.sent)
	}

	// Unverified (or deleted): acked without a send.
	if _, err := dbClient.ExecContext(ctx, `UPDATE user_contacts SET verified_at = NULL WHERE id = $1`, contactID); err != nil {
		t.Fatal(err)
	}
	if err := worker.handle(ctx, message(contactID.String())); err != nil {
		t.Fatalf("unverified contact must be dropped, not retried: %v", err)
	}
	if err := worker.handle(ctx, message(uuid.NewString())); err != nil {
		t.Fatalf("unknown contact must be dropped, not retried: %v", err)
	}
	if len(directStub.sent) != 1 {
		t.Fatalf("sent = %d, want no send to an undeliverable contact", len(directStub.sent))
	}

	// Without a ContactID the channel's own recipients apply.
	if err := worker.handle(ctx, message("")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(directStub.sent) != 2 || directStub.sent[1] != nil {
		t.Fatalf("sent = %+v, want a channel send without a direct recipient", directStub.sent)
	}
}

// testKeys is a fixed 32-byte key at version 1.
type testKeys struct{}

func (testKeys) CurrentKey() ([]byte, int, error) {
	return []byte("0123456789abcdef0123456789abcdef"), 1, nil
}
func (testKeys) KeyForVersion(int) ([]byte, error) {
	return []byte("0123456789abcdef0123456789abcdef"), nil
}
