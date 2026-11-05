package notifications

import (
	"context"
	"encoding/json"
	"fmt"
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
// provider incident nothing ever closes. Likewise anything published in an
// earlier paging episode once the alert has reopened: a stale resolve would
// close the relapse's provider incident while the outage is live.
func TestHandle_DropsEventsSupersededByResolve(t *testing.T) {
	cases := []struct {
		eventType   string
		alertStatus string // "" = alert row gone
		envRelapse  int    // the envelope's Alert.RelapseCount
		rowRelapse  int    // alerts.relapse_count now
		wantSend    bool
	}{
		{"created", "resolved", 0, 0, false},
		{"reminder", "resolved", 0, 0, false},
		{"acknowledged", "resolved", 0, 0, false},
		{"created", "", 0, 0, false},
		{"created", "active", 0, 0, true},
		{"acknowledged", "acknowledged", 0, 0, true},
		{"acknowledged", "active", 0, 0, false}, // unacknowledged since publish
		{"suspected_cause", "resolved", 0, 0, false},
		{"suspected_cause", "acknowledged", 0, 0, true},
		{"suspected_cause", "active", 0, 0, true},
		{"resolved", "resolved", 0, 0, true},
		{"resolved", "", 0, 0, true},          // a deleted alert's resolve still goes out
		{"resolved", "active", 0, 1, false},   // reopened since: the relapse is live
		{"resolved", "resolved", 1, 2, false}, // an older episode's resolve
		{"resolved", "resolved", 1, 1, true},  // the relapse's own resolve
		{"created", "active", 0, 1, false},    // the previous episode's trigger
		{"reminder", "active", 0, 1, false},
		{"created", "active", 1, 1, true}, // the relapse's page
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s/%d-%d", tc.eventType, tc.alertStatus, tc.envRelapse, tc.rowRelapse), func(t *testing.T) {
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
			mock.ExpectBegin()
			q := mock.ExpectQuery(`FROM alerts al WHERE al.id = \$1 FOR SHARE`).WithArgs(alertID)
			if tc.alertStatus == "" {
				q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow(tc.alertStatus, false, tc.rowRelapse))
			}
			if tc.eventType == "resolved" && !tc.wantSend {
				// A resolve dropped for a reopen hands its claim back
				// (releaseSupersededResolveClaim; a no-op without the
				// claim's timestamp) and commits.
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}

			c := &Consumer{logger: logger.New("test", "error"), db: &db.Client{DB: sqlDB}, encryptor: secrets.NoOpEncryptor{}}
			raw, _ := json.Marshal(notifications.DispatchEnvelope{
				V: 1, ChannelID: channelID.String(), ChannelType: supersededStubType,
				AlertID: alertID.String(), EventType: tc.eventType,
				Event: notifications.AlertEvent{Type: tc.eventType, Alert: notifications.AlertDetails{RelapseCount: tc.envRelapse}},
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
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow(tc.alertStatus, false, 0))
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
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow(tc.status, tc.snoozed, 0))
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
