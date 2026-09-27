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
				q := mock.ExpectQuery(regexp.QuoteMeta(`SELECT status FROM alerts WHERE id = $1 FOR SHARE`)).WithArgs(alertID)
				if tc.alertStatus == "" {
					q.WillReturnRows(sqlmock.NewRows([]string{"status"}))
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(tc.alertStatus))
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
