package alerter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/webhook"
	"github.com/yassinebenameur/probara/shared/secrets"
)

type ackTestKeyring struct{}

var ackTestKey = bytes.Repeat([]byte{5}, 32)

func (ackTestKeyring) CurrentKey() ([]byte, int, error)  { return ackTestKey, 1, nil }
func (ackTestKeyring) KeyForVersion(int) ([]byte, error) { return ackTestKey, nil }

// The events the alerter really builds (buildAlertEvent, status "active" on
// pages) reach a channel with a working acknowledge link on created and
// reminder, bound to this tenant, alert and channel, and with none on
// resolve. Goes through sendChannelNotification and the real webhook plugin.
func TestSendChannelNotification_AckLinkEndToEnd(t *testing.T) {
	signer := actionlink.New(ackTestKeyring{})
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	// httptest listens on loopback, which the default egress policy refuses.
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: signer})

	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := &Alerter{
		config:    &config.AlerterConfig{},
		logger:    logger.New("alerter-test", "error"),
		encryptor: secrets.NoOpEncryptor{},
	}
	cfg, _ := json.Marshal(map[string]any{"url": srv.URL})
	channel := alertChannel{ID: uuid.New(), Name: "Ops webhook", Type: "generic_webhook", Config: cfg, IsActive: true}
	binding := alertSubject{MonitorID: uuid.New(), TenantID: uuid.New(), MonitorName: "Checkout API"}
	now := time.Now().UTC().Truncate(time.Second)
	alert := &alertRecord{ID: uuid.New(), MonitorID: binding.MonitorID, TriggeredAt: now.Add(-10 * time.Minute), FailureCount: 3}

	for _, tc := range []struct {
		eventType string
		wantLink  bool
	}{
		{"created", true},
		{"reminder", true},
		{"resolved", false},
	} {
		t.Run(tc.eventType, func(t *testing.T) {
			bodies = nil
			if err := a.sendChannelNotification(context.Background(), channel, tc.eventType, binding, alert, nil, now); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(bodies) != 1 {
				t.Fatalf("deliveries = %d", len(bodies))
			}
			var doc struct {
				Alert struct {
					Status string `json:"status"`
					Links  struct {
						Acknowledge string `json:"acknowledge"`
					} `json:"links"`
				} `json:"alert"`
			}
			if err := json.Unmarshal(bodies[0], &doc); err != nil {
				t.Fatal(err)
			}
			link := doc.Alert.Links.Acknowledge
			if !tc.wantLink {
				if link != "" || strings.Contains(string(bodies[0]), "alert-action") {
					t.Fatalf("%s carries an acknowledge link: %s", tc.eventType, bodies[0])
				}
				return
			}
			const prefix = "https://probara.example.com/alert-action#"
			if !strings.HasPrefix(link, prefix) {
				t.Fatalf("%s (status %q) link = %q", tc.eventType, doc.Alert.Status, link)
			}
			claims, err := signer.Verify(strings.TrimPrefix(link, prefix), actionlink.ActionAcknowledge)
			if err != nil {
				t.Fatalf("link does not verify: %v", err)
			}
			if claims.TenantID != binding.TenantID || claims.AlertID != alert.ID || claims.ChannelID != channel.ID {
				t.Fatalf("link bound to %+v, want tenant %s alert %s channel %s", claims, binding.TenantID, alert.ID, channel.ID)
			}
			if want := now.Add(actionlink.TTL); !claims.ExpiresAt.Equal(want) {
				t.Fatalf("expires %v, want 72 h after the event (%v)", claims.ExpiresAt, want)
			}
		})
	}
}
