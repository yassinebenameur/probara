package notifications

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

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
		wantSend    bool
	}{
		{"created", "resolved", false},
		{"reminder", "resolved", false},
		{"acknowledged", "resolved", false},
		{"created", "", false},
		{"created", "active", true},
		{"acknowledged", "acknowledged", true},
		{"acknowledged", "active", false}, // unacknowledged since publish
		{"suspected_cause", "resolved", false},
		{"suspected_cause", "acknowledged", true},
		{"suspected_cause", "active", true},
		{"resolved", "resolved", true}, // resolves are never superseded
	}
	for _, tc := range cases {
		t.Run(tc.eventType+"/"+tc.alertStatus, func(t *testing.T) {
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
				q := mock.ExpectQuery(`FROM alerts al WHERE al.id = \$1 FOR SHARE`).WithArgs(alertID)
				if tc.alertStatus == "" {
					q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}))
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}).AddRow(tc.alertStatus, false))
				}
				mock.ExpectRollback()
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

// An operator acknowledged while the trigger sat on a retry backoff, so the
// acknowledge reached the provider first and was dropped as unknown. A paging
// channel must get the acknowledge again right after the late trigger, or its
// incident escalates forever; a chat channel just gets the trigger.
func TestHandle_AcknowledgesTriggerThatLandedAfterAck(t *testing.T) {
	cases := []struct {
		name        string
		channelType string
		sends       func() []string
		alertStatus string
		want        []string
	}{
		{"paging/acknowledged", pagingStubType, func() []string { return pagingStub.sends }, "acknowledged", []string{"created", "acknowledged"}},
		{"paging/active", pagingStubType, func() []string { return pagingStub.sends }, "active", []string{"created"}},
		{"chat/acknowledged", supersededStubType, func() []string { return stub.sends }, "acknowledged", []string{"created"}},
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
			mock.ExpectQuery(`FROM alerts al WHERE al.id = \$1 FOR SHARE`).WithArgs(alertID).
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}).AddRow(tc.alertStatus, false))
			mock.ExpectRollback()

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

// A tier or reminder published with StopOnAck must not reach the provider
// once the alert is acknowledged (it may have waited on a retry backoff), and
// a snooze holds everything but acknowledgements. Without StopOnAck the older
// contract stands: deliver, and re-acknowledge an overtaken paging trigger.
func TestHandle_DropsEventsHeldByAckOrSnooze(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		stopOnAck bool
		status    string
		snoozed   bool
		wantSends int
	}{
		{"created/stop_on_ack/acknowledged", "created", true, "acknowledged", false, 0},
		{"reminder/stop_on_ack/acknowledged", "reminder", true, "acknowledged", false, 0},
		{"created/stop_on_ack/active", "created", true, "active", false, 1},
		{"reminder/notify_after_ack/acknowledged", "reminder", false, "acknowledged", false, 1},
		{"suspected_cause/stop_on_ack/acknowledged", "suspected_cause", true, "acknowledged", false, 1},
		{"created/snoozed", "created", false, "active", true, 0},
		{"reminder/snoozed", "reminder", false, "active", true, 0},
		{"suspected_cause/snoozed", "suspected_cause", false, "active", true, 0},
		{"acknowledged/snoozed", "acknowledged", false, "acknowledged", true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			stub.sends = nil

			channelID, alertID := uuid.New(), uuid.New()
			mock.ExpectQuery(regexp.QuoteMeta(`FROM alert_channels`)).WithArgs(channelID).
				WillReturnRows(sqlmock.NewRows([]string{"name", "type", "config", "is_active"}).
					AddRow("chat", supersededStubType, []byte(`{}`), true))
			mock.ExpectBegin()
			mock.ExpectQuery(`FROM alerts al WHERE al.id = \$1 FOR SHARE`).WithArgs(alertID).
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed"}).AddRow(tc.status, tc.snoozed))
			claimedAt := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			if tc.wantSends == 0 {
				// A held event releases the alerter's claim and commits.
				switch tc.eventType {
				case "created":
					mock.ExpectExec(`DELETE FROM alert_notification_states`).
						WithArgs(alertID, channelID, claimedAt).WillReturnResult(sqlmock.NewResult(0, 1))
				case "suspected_cause":
					mock.ExpectExec(`SET suspected_cause_sent_at = NULL`).
						WithArgs(alertID, channelID, claimedAt).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}

			c := &Consumer{logger: logger.New("test", "error"), db: &db.Client{DB: sqlDB}, encryptor: secrets.NoOpEncryptor{}}
			raw, _ := json.Marshal(notifications.DispatchEnvelope{
				V: 1, ChannelID: channelID.String(), ChannelType: supersededStubType,
				AlertID: alertID.String(), EventType: tc.eventType, StopOnAck: tc.stopOnAck,
				Event: notifications.AlertEvent{Type: tc.eventType, Timestamp: claimedAt},
			})
			if err := c.handle(context.Background(), &queue.Message{Data: raw}); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if len(stub.sends) != tc.wantSends {
				t.Fatalf("sends = %v, want %d", stub.sends, tc.wantSends)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
