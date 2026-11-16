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
		// rowRelapse is the alert's relapse_count now; the envelope was
		// published in episode 0.
		rowRelapse int
	}{
		{"created", "resolved", false, false, 0},
		{"reminder", "resolved", false, false, 0},
		{"acknowledged", "resolved", false, false, 0},
		{"created", "", false, false, 0},
		{"created", "active", false, true, 0},
		{"reminder", "active", false, true, 0},
		{"acknowledged", "acknowledged", false, true, 0},
		{"suspected_cause", "resolved", false, false, 0},
		{"suspected_cause", "active", false, true, 0},
		{"resolved", "resolved", false, true, 0},
		{"resolved", "", false, true, 0}, // alert history deleted: the recovery still goes out
		// Open again after a relapse since publish: the resolve is dropped
		// (its claim handed back when it reached nobody, a no-op here: the
		// envelope carries no claim time), and so is anything else from the
		// episode that recovered. Once the alert is resolved again, an
		// earlier episode's resolve still goes out: a channel that never
		// heard the relapse got no other resolve.
		{"resolved", "active", false, false, 1},
		{"resolved", "resolved", false, true, 1},
		{"created", "active", false, false, 1},
		{"reminder", "active", false, false, 1},
		// Acknowledged or snoozed since publish: the mute holds the page and
		// the reminder, never the follow-up or the provider acknowledgement.
		{"created", "acknowledged", false, false, 0},
		{"reminder", "acknowledged", false, false, 0},
		{"suspected_cause", "acknowledged", false, true, 0},
		{"created", "active", true, false, 0},
		{"reminder", "active", true, false, 0},
		{"suspected_cause", "active", true, true, 0},
		{"acknowledged", "acknowledged", true, true, 0},
		// Unacknowledged since publish: the provider must not be told to stop.
		{"acknowledged", "active", false, false, 0},
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
			mock.ExpectBegin()
			q := mock.ExpectQuery(regexp.QuoteMeta(alertStatusQuery)).WithArgs(alertID)
			if tc.alertStatus == "" {
				q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow(tc.alertStatus, tc.snoozed, tc.rowRelapse))
			}
			muted := tc.alertStatus != "" && tc.alertStatus != "resolved" && tc.rowRelapse == 0 &&
				tc.eventType != "acknowledged" && tc.eventType != "resolved" && !tc.wantSend
			if droppedResolve := tc.eventType == "resolved" && !tc.wantSend; muted || droppedResolve {
				// The held event's claim is handed back (a no-op here:
				// the envelope carries no claim time) and committed.
				mock.ExpectCommit()
			} else {
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
				WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow(tc.alertStatus, false, 0))
			if tc.alertStatus == "acknowledged" {
				mock.ExpectCommit()
			} else {
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

// A paging provider keys its incident per page episode, so a resolve queued
// before a relapse closes only the earlier incident: it is delivered even
// while the alert is open again.
func TestHandle_PagingResolveDeliveredWhileRelapsed(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	pagingStub.sends = nil

	channelID, alertID := uuid.New(), uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM alert_channels`)).WithArgs(channelID).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "config", "is_active"}).
			AddRow("pager", pagingStubType, []byte(`{}`), true))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(alertStatusQuery)).WithArgs(alertID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "snoozed", "relapse_count"}).AddRow("active", false, 1))
	mock.ExpectRollback()

	c := &Consumer{logger: logger.New("test", "error"), db: &db.Client{DB: sqlDB}, encryptor: secrets.NoOpEncryptor{}}
	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelID: channelID.String(), ChannelType: pagingStubType,
		AlertID: alertID.String(), EventType: "resolved",
		Event: notifications.AlertEvent{Type: "resolved"},
	})
	if err := c.handle(context.Background(), &queue.Message{Data: raw}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(pagingStub.sends) != 1 || pagingStub.sends[0] != "resolved" {
		t.Fatalf("sends = %v, want the resolve", pagingStub.sends)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
