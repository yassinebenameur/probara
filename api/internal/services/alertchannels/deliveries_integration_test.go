//go:build !unit

package alertchannels

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const testSendStubType = "test_send_history_stub"

const testSendSecret = "https://hooks.example.com/services/T1/B2/TEST-SECRET-PATH"

type testSendStub struct {
	mu   sync.Mutex
	fail bool
}

func (p *testSendStub) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:         testSendStubType,
		Capabilities: []plugin.Capability{plugin.CapabilityTestable},
		Fields:       []plugin.Field{{Key: "url", Type: plugin.FieldTypeURL, Secret: true}},
	}
}
func (p *testSendStub) Validate(json.RawMessage) error { return nil }
func (p *testSendStub) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return plugin.Permanent(errors.New("stub rejected " + req.Channel.String("url")))
	}
	return nil
}

var sendStub = &testSendStub{}

func init() { plugin.Register(sendStub) }

// A test send lands in the channel's delivery history, marked as a test,
// with its error sanitized, and moves the failing flag the channel list and
// detail carry.
func TestTestSendRecordedAndFlagsFailing(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "test-send-history")
	other := testutil.InsertTenant(ctx, t, db, "test-send-other")
	svc := NewService(db, secrets.NoOpEncryptor{}).WithLogger(logger.New("test", "error"))
	ch, err := svc.CreateAlertChannel(ctx, tenantID, &models.CreateAlertChannelRequest{
		Name: "hook", Type: testSendStubType, Config: json.RawMessage(`{"url":"` + testSendSecret + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	sendStub.fail = true
	if err := svc.TestAlertChannel(ctx, tenantID, ch.ID); err == nil {
		t.Fatal("a failing test send must report its error")
	}
	var isTest bool
	var alertNull bool
	var stored, outcome string
	if err := db.QueryRowContext(ctx, `SELECT is_test, alert_id IS NULL, error, outcome FROM alert_notification_deliveries WHERE channel_id = $1`,
		ch.ID).Scan(&isTest, &alertNull, &stored, &outcome); err != nil {
		t.Fatalf("test send row: %v", err)
	}
	if !isTest || !alertNull || outcome != "permanent_failure" {
		t.Fatalf("row = test %v, no alert %v, outcome %s", isTest, alertNull, outcome)
	}
	if strings.Contains(stored, "TEST-SECRET-PATH") || strings.Contains(stored, "/services/") {
		t.Fatalf("stored error %q leaks the webhook URL", stored)
	}

	list, err := svc.ListAlertChannels(ctx, tenantID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || !list.Items[0].Failing || list.Items[0].LastFailedAt == nil {
		t.Fatalf("listed channel = %+v, want failing", list.Items)
	}

	sendStub.fail = false
	if err := svc.TestAlertChannel(ctx, tenantID, ch.ID); err != nil {
		t.Fatalf("test send: %v", err)
	}
	got, err := svc.GetAlertChannel(ctx, tenantID, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Failing || got.LastDeliveredAt == nil {
		t.Fatalf("channel after a successful test = %+v, want not failing", got)
	}
	if _, err := svc.GetAlertChannel(ctx, other, ch.ID); !errors.Is(err, ErrAlertChannelNotFound) {
		t.Fatalf("another workspace read the channel: %v", err)
	}
}
