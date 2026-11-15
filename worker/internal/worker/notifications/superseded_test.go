package notifications

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

const supersededStubType = "test_superseded_stub"

// alertStatusQuery is the tail of eventSuperseded's locked status read.
const alertStatusQuery = `FROM alerts al WHERE al.id = $1 FOR SHARE`

type countingPlugin struct{ sends []string }

func (p *countingPlugin) Manifest() plugin.Manifest      { return plugin.Manifest{Type: supersededStubType} }
func (p *countingPlugin) Validate(json.RawMessage) error { return nil }
func (p *countingPlugin) Send(_ context.Context, req plugin.DispatchRequest) error {
	p.sends = append(p.sends, req.Type())
	return nil
}

var stub = &countingPlugin{}

func init() { plugin.Register(stub) }

// A trigger that failed and was redelivered after the alert resolved must be
// dropped: the resolve already went out, and a late trigger would open a
// provider incident nothing ever closes.
func TestHandle_DropsEventsSupersededByResolve(t *testing.T) {
	cases := []struct {
		eventType   string
		alertStatus string // "" = alert row gone
		snoozed     bool
		wantSend    bool
	}{
		{"created", "resolved", false, false},
		{"reminder", "resolved", false, false},
		{"acknowledged", "resolved", false, false},
		{"created", "", false, false},
		{"created", "active", false, true},
		{"reminder", "active", false, true},
		{"acknowledged", "acknowledged", false, true},
		{"suspected_cause", "resolved", false, false},
		{"suspected_cause", "active", false, true},
		{"resolved", "resolved", false, true}, // resolves are never superseded
		// Acknowledged or snoozed since publish: the mute holds the page and
		// the reminder, never the follow-up or the provider acknowledgement.
		{"created", "acknowledged", false, false},
		{"reminder", "acknowledged", false, false},
		{"suspected_cause", "acknowledged", false, true},
		{"created", "active", true, false},
		{"reminder", "active", true, false},
		{"suspected_cause", "active", true, true},
		{"acknowledged", "acknowledged", true, true},
		// Unacknowledged since publish: the provider must not be told to stop.
		{"acknowledged", "active", false, false},
	}
	for _, tc := range cases {
		name := tc.eventType + "/" + tc.alertStatus
		if tc.snoozed {
			name += "/snoozed"
		}
		t.Run(name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			stub.sends = nil

			channelID, alertID := uuid.New(), uuid.New()
			mock.ExpectQuery(regexp.QuoteMeta(`FROM alert_channels`)).WithArgs(channelID).
				WillReturnRows(sqlmock.NewRows([]string{"name", "type", "config", "is_active"}).
					AddRow("pager", supersededStubType, []byte(`{}`), true))
			if tc.eventType != "resolved" {
				mock.ExpectBegin()
				q := mock.ExpectQuery(regexp.QuoteMeta(alertStatusQuery)).WithArgs(alertID)
				if tc.alertStatus == "" {
					q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}))
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}).AddRow(tc.alertStatus, tc.snoozed))
				}
				if muted := tc.alertStatus != "" && tc.alertStatus != "resolved" && tc.eventType != "acknowledged" && !tc.wantSend; muted {
					// The held event's claim is handed back (a no-op here:
					// the envelope carries no claim time) and committed.
					mock.ExpectCommit()
				} else {
					if tc.eventType == "created" && tc.wantSend {
						// The page attempt marker, written before the send.
						mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO alert_notification_recipient_deliveries`)).
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
					mock.ExpectRollback()
				}
			} else {
				// A delivered resolve clears the channel's page marker.
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM alert_notification_recipient_deliveries`)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			}

			c := &Consumer{logger: logger.New("test", "error"), db: &db.Client{DB: sqlDB}, encryptor: secrets.NoOpEncryptor{}}
			raw, _ := json.Marshal(notifications.DispatchEnvelope{
				V: 1, ChannelID: channelID.String(), ChannelType: supersededStubType,
				AlertID: alertID.String(), EventType: tc.eventType,
				Event: notifications.AlertEvent{Type: tc.eventType},
			})
			if err := c.handle(context.Background(), &queue.Message{Data: raw}); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if sent := len(stub.sends) == 1; sent != tc.wantSend {
				t.Fatalf("sent = %v, want %v", sent, tc.wantSend)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

const pagingStubType = "test_paging_stub"

type pagingPlugin struct{ countingPlugin }

func (p *pagingPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: pagingStubType, Capabilities: []plugin.Capability{plugin.CapabilityAcknowledge}}
}
func (p *pagingPlugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	return p.countingPlugin.Send(ctx, req)
}

var pagingStub = &pagingPlugin{}

func init() { plugin.Register(pagingStub) }

// An operator acknowledged while the trigger sat on a retry backoff. The
// acknowledgement stops the alert's notifications, so the late trigger is
// dropped on paging and chat channels alike (the provider never opens an
// incident that would need acknowledging); on an alert still active it goes
// out as usual.
func TestHandle_DropsTriggerQueuedBeforeAck(t *testing.T) {
	cases := []struct {
		name        string
		channelType string
		sends       func() []string
		alertStatus string
		want        []string
	}{
		{"paging/acknowledged", pagingStubType, func() []string { return pagingStub.sends }, "acknowledged", nil},
		{"paging/active", pagingStubType, func() []string { return pagingStub.sends }, "active", []string{"created"}},
		{"chat/acknowledged", supersededStubType, func() []string { return stub.sends }, "acknowledged", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			stub.sends, pagingStub.sends = nil, nil

			channelID, alertID := uuid.New(), uuid.New()
			mock.ExpectQuery(regexp.QuoteMeta(`FROM alert_channels`)).WithArgs(channelID).
				WillReturnRows(sqlmock.NewRows([]string{"name", "type", "config", "is_active"}).
					AddRow("pager", tc.channelType, []byte(`{}`), true))
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(alertStatusQuery)).WithArgs(alertID).
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}).AddRow(tc.alertStatus, false))
			if tc.alertStatus == "acknowledged" {
				mock.ExpectCommit()
			} else {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO alert_notification_recipient_deliveries`)).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectRollback()
			}

			c := &Consumer{logger: logger.New("test", "error"), db: &db.Client{DB: sqlDB}, encryptor: secrets.NoOpEncryptor{}}
			raw, _ := json.Marshal(notifications.DispatchEnvelope{
				V: 1, ChannelID: channelID.String(), ChannelType: tc.channelType,
				AlertID: alertID.String(), EventType: "created",
				Event: notifications.AlertEvent{Type: "created"},
			})
			if err := c.handle(context.Background(), &queue.Message{Data: raw}); err != nil {
				t.Fatalf("handle: %v", err)
			}
			got := tc.sends()
			if len(got) != len(tc.want) {
				t.Fatalf("sends = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("sends = %v, want %v", got, tc.want)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
