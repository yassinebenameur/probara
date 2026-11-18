//go:build !unit

package pagesubscribers

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/incidents"
	"github.com/yassinebenameur/probara/shared/actionlink"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// fakeMailer records every accepted message; fail decides per recipient.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	fail func(to string) error
}

func (f *fakeMailer) SendMessage(_ context.Context, m mail.Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		if err := f.fail(m.To[0]); err != nil {
			return &mail.DeliveryError{Failed: []mail.RecipientFailure{{Recipient: m.To[0], Err: err}}}
		}
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailer) setFail(fn func(string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fn
}

// to returns the messages sent to address whose subject contains part.
func (f *fakeMailer) to(address, part string) []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mail.Message
	for _, m := range f.sent {
		if m.To[0] == address && strings.Contains(m.Subject, part) {
			out = append(out, m)
		}
	}
	return out
}

// recipients lists who got a message whose subject contains part, sorted,
// once per message.
func (f *fakeMailer) recipients(part string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		if strings.Contains(m.Subject, part) {
			out = append(out, m.To[0])
		}
	}
	sort.Strings(out)
	return out
}

type recorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recorder) Record(e audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

const testInternalToken = "internal-token-0123456789abcdef0123456789"

type fixture struct {
	ctx       context.Context
	db        *shareddb.Client
	tenantID  uuid.UUID
	monitorID uuid.UUID
	pageID    uuid.UUID
	incidents *incidents.Service
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "subscribers")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "checkout-internal")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "acme", "Acme")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorID, 0)
	_, err := dbClient.ExecContext(ctx, `UPDATE status_page_monitors SET display_name = 'Checkout' WHERE status_page_id = $1`, pageID)
	require.NoError(t, err)
	f := &fixture{ctx: ctx, db: dbClient, tenantID: tenantID, monitorID: monitorID, pageID: pageID, incidents: incidents.NewService(dbClient, nil)}
	f.setting(t, "enable_email_subscriptions", true)
	return f
}

func (f *fixture) setting(t *testing.T, key string, on bool) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, `UPDATE status_pages SET settings = settings || jsonb_build_object($2::text, $3::boolean) WHERE id = $1`, f.pageID, key, on)
	require.NoError(t, err)
}

func (f *fixture) service(mailer mail.Sender, at *time.Time) *Service {
	s := NewService(f.db, Config{
		Mailer: mailer, Links: actionlink.New(testKeys{}), AppBaseURL: "https://app.example.com/",
		StatusPageBaseURL: "https://status.example.com", InternalToken: testInternalToken, Audit: &recorder{},
	})
	if at != nil {
		s.now = func() time.Time { return *at }
	}
	return s
}

func run(t *testing.T, s *Service) {
	t.Helper()
	require.NoError(t, s.RunOnce(context.Background()))
}

var fragmentToken = regexp.MustCompile(`#([A-Za-z0-9_-]{20,})`)

// linkToken is the token in the first fragment link of m's text part.
func linkToken(t *testing.T, m mail.Message) string {
	t.Helper()
	got := fragmentToken.FindStringSubmatch(m.Text)
	require.NotNil(t, got, "no link in %q", m.Text)
	return got[1]
}

// oneClickToken is the token of m's List-Unsubscribe URL.
func oneClickToken(t *testing.T, m mail.Message) string {
	t.Helper()
	raw := strings.Trim(m.Headers["List-Unsubscribe"], "<>")
	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, OneClickUnsubscribePath, u.Path)
	return u.Query().Get("token")
}

// confirmed subscribes address and follows its confirmation link.
func (f *fixture) confirmed(t *testing.T, s *Service, mailer *fakeMailer, address string) {
	t.Helper()
	require.NoError(t, s.Subscribe(f.ctx, "acme", address))
	run(t, s)
	mails := mailer.to(address, "Confirm your subscription")
	require.NotEmpty(t, mails)
	resp, err := s.Confirm(f.ctx, linkToken(t, mails[len(mails)-1]), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, models.StatusPageSubscriptionConfirmed, resp.State)
}

// publish creates an incident on the fixture's monitor and publishes it.
func (f *fixture) publish(t *testing.T, title string) uuid.UUID {
	t.Helper()
	inc, err := f.incidents.CreateIncident(f.ctx, f.tenantID, &models.CreateIncidentRequest{Title: title, Summary: "Checkout requests fail.", MonitorID: f.monitorID.String()})
	require.NoError(t, err)
	_, err = f.incidents.PublishIncidentToStatusPage(f.ctx, f.tenantID, inc.ID, f.pageID, &models.UpsertIncidentPublicationRequest{MonitorIDs: []string{f.monitorID.String()}})
	require.NoError(t, err)
	return inc.ID
}

func (f *fixture) outbox(t *testing.T, kind string) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM status_page_notification_outbox WHERE event_kind = $1`, kind).Scan(&n))
	return n
}

func (f *fixture) deliveryStates(t *testing.T) map[string]int {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx, `SELECT status, COUNT(*) FROM status_page_notification_deliveries GROUP BY status`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		require.NoError(t, rows.Scan(&s, &n))
		out[s] = n
	}
	return out
}
