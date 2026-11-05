//go:build !unit

package reportschedules

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	fail map[string]bool
}

func (m *fakeMailer) SendMessage(_ context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail[msg.To[0]] {
		return errors.New("550 mailbox unavailable")
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *fakeMailer) take() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sent
	m.sent = nil
	return out
}

// TestReportSchedules covers the schedule lifecycle end to end: the mail
// requirement, issuing at period close, one message per recipient with
// attachments and unsubscribe headers, no resend, SKIP LOCKED across
// concurrent runners, unsubscribe links, tenant isolation and soft-deleted
// workspaces.
func TestReportSchedules(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reports")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "checkout")
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "reports-other")

	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, 'up', 'result', $3, $4)
	`, tenantID, monitorID, aug1, nov1)
	require.NoError(t, err)

	clock := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	// The SLA service keeps the real clock: September 2026 is closed by it.
	reports := slas.NewService(dbClient, sharedanalytics.NewRepository(dbClient), true)
	sla, err := reports.Create(ctx, tenantID, &models.CreateSLARequest{Name: "Checkout", TargetPct: 99.9, MonitorIDs: []string{monitorID.String()}})
	require.NoError(t, err)

	t.Run("enabling needs SMTP and APP_BASE_URL", func(t *testing.T) {
		noMail := NewService(dbClient, reports, nil, "https://ops.example.com", nil, nil)
		_, err := noMail.Create(ctx, tenantID, &CreateRequest{SLAID: sla.ID.String(), Recipients: []string{"a@example.com"}})
		require.ErrorAs(t, err, new(Invalid))
		require.Contains(t, err.Error(), "SMTP_HOST")
		noBase := NewService(dbClient, reports, &fakeMailer{}, "", nil, nil)
		_, err = noBase.Create(ctx, tenantID, &CreateRequest{SLAID: sla.ID.String(), Recipients: []string{"a@example.com"}})
		require.Contains(t, err.Error(), "APP_BASE_URL")

		off := false
		sc, err := noMail.Create(ctx, tenantID, &CreateRequest{SLAID: sla.ID.String(), Recipients: []string{"a@example.com"}, Enabled: &off})
		require.NoError(t, err, "a disabled schedule can be kept without mail")
		list, err := noMail.List(ctx, tenantID, sla.ID)
		require.NoError(t, err)
		require.False(t, list.MailReady)
		require.NotEmpty(t, list.MailProblem)
		require.NoError(t, noMail.Delete(ctx, tenantID, sc.ID))
	})

	mailer := &fakeMailer{fail: map[string]bool{}}
	svc := NewService(dbClient, reports, mailer, "https://ops.example.com/", nil, nil)
	svc.now = now
	sc, err := svc.Create(ctx, tenantID, &CreateRequest{
		SLAID: sla.ID.String(), Recipients: []string{"ops@example.com", "Customer <cust@example.org>"}, Formats: []string{"csv", "pdf"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com", "cust@example.org"}, sc.Recipients)
	require.Equal(t, []string{"pdf", "csv"}, sc.Formats)
	require.True(t, sc.AutoIssue)
	require.Equal(t, time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC), sc.NextRunAt, "September's close plus the grace")

	t.Run("tenant isolation", func(t *testing.T) {
		_, err := svc.Get(ctx, otherTenant, sc.ID)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = svc.Create(ctx, otherTenant, &CreateRequest{SLAID: sla.ID.String(), Recipients: []string{"x@example.com"}})
		require.ErrorIs(t, err, ErrSLANotFound)
		require.ErrorIs(t, svc.Delete(ctx, otherTenant, sc.ID), ErrNotFound)
		_, err = svc.SendNow(ctx, otherTenant, sc.ID)
		require.ErrorIs(t, err, ErrNotFound)
		list, err := svc.List(ctx, otherTenant, uuid.Nil)
		require.NoError(t, err)
		require.Empty(t, list.Items)
	})

	t.Run("nothing is due before the close", func(t *testing.T) {
		n, err := svc.RunDue(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
		require.Empty(t, mailer.take())
	})

	var token string
	t.Run("period close issues and sends once", func(t *testing.T) {
		clock = time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC)
		// Two runners at once: SKIP LOCKED hands the row to one of them.
		var wg sync.WaitGroup
		ran := make([]int, 2)
		errs := make([]error, 2)
		for i := range ran {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ran[i], errs[i] = svc.RunDue(ctx)
			}(i)
		}
		wg.Wait()
		require.NoError(t, errors.Join(errs...))
		require.Equal(t, 1, ran[0]+ran[1])

		sent := mailer.take()
		require.Len(t, sent, 2, "one message per recipient")
		for _, msg := range sent {
			require.Len(t, msg.To, 1)
			require.Contains(t, msg.Subject, "Checkout, 2026-09: Met")
			require.Len(t, msg.Attachments, 2)
			require.Equal(t, "sla-checkout-2026-09.pdf", msg.Attachments[0].Filename)
			require.Equal(t, "sla-checkout-2026-09.csv", msg.Attachments[1].Filename)
			require.True(t, strings.HasPrefix(string(msg.Attachments[0].Data), "%PDF"))
			require.Contains(t, msg.Text, "This is the issued report")
			require.Contains(t, msg.Text, "https://ops.example.com/slas/"+sla.ID.String()+"?period=2026-09")
			require.Equal(t, "List-Unsubscribe=One-Click", msg.Headers["List-Unsubscribe-Post"])
		}
		token = UnsubscribeToken(sc.ID, mustKey(t, svc, sc.ID), "cust@example.org")
		require.Contains(t, sent[0].Text+sent[1].Text, "/report-unsubscribe/"+token)

		issued, err := reports.ListReports(ctx, tenantID, sla.ID)
		require.NoError(t, err)
		require.Len(t, issued, 1)
		require.Equal(t, "2026-09", issued[0].PeriodKey)
		require.Equal(t, "Report schedule", issued[0].IssuedBy)

		got, err := svc.Get(ctx, tenantID, sc.ID)
		require.NoError(t, err)
		require.Equal(t, "2026-09", got.LastPeriodKey)
		require.Equal(t, 2, got.LastSentCount)
		require.Empty(t, got.LastError)
		require.Equal(t, time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), got.NextRunAt)

		n, err := svc.RunDue(ctx)
		require.NoError(t, err)
		require.Zero(t, n, "not due again until October closes")
	})

	t.Run("unsubscribe link", func(t *testing.T) {
		st, err := svc.DescribeUnsubscribe(ctx, token)
		require.NoError(t, err)
		require.Equal(t, StateSubscribed, st.State)
		require.Equal(t, "Checkout", st.SLAName)
		require.Equal(t, "c***@example.org", st.Recipient)

		bad, err := svc.DescribeUnsubscribe(ctx, token[:len(token)-2]+"AA")
		require.NoError(t, err)
		require.Equal(t, StateInvalid, bad.State)
		bad, err = svc.DescribeUnsubscribe(ctx, "not-a-token")
		require.NoError(t, err)
		require.Equal(t, StateInvalid, bad.State)

		req := httptest.NewRequest("POST", "/api/v1/report-unsubscribe/x", nil)
		for i := 0; i < 2; i++ {
			st, err = svc.Unsubscribe(ctx, req, token)
			require.NoError(t, err)
			require.Equal(t, StateUnsubscribed, st.State, "idempotent")
		}
		got, err := svc.Get(ctx, tenantID, sc.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"cust@example.org"}, got.Unsubscribed)

		// Send now: the unsubscribed recipient is skipped, nothing is issued
		// again, a failing recipient is reported.
		mailer.fail["ops@example.com"] = true
		out, err := svc.SendNow(ctx, tenantID, sc.ID)
		require.NoError(t, err)
		require.Equal(t, "2026-09", out.PeriodKey)
		require.True(t, out.Issued, "the issued copy is reused")
		require.Equal(t, 0, out.Sent)
		require.Equal(t, 1, out.Skipped)
		require.Equal(t, 1, out.Failed)
		require.Contains(t, out.Error, "1 of 1 recipients failed")
		require.Empty(t, mailer.take())
		delete(mailer.fail, "ops@example.com")
	})

	t.Run("re-enabling skips the periods it slept through", func(t *testing.T) {
		off, on := false, true
		_, err := svc.Update(ctx, tenantID, sc.ID, &UpdateRequest{Enabled: &off})
		require.NoError(t, err)
		clock = time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)
		n, err := svc.RunDue(ctx)
		require.NoError(t, err)
		require.Zero(t, n, "a disabled schedule never runs")
		got, err := svc.Update(ctx, tenantID, sc.ID, &UpdateRequest{Enabled: &on})
		require.NoError(t, err)
		require.Equal(t, time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC), got.NextRunAt)
	})

	t.Run("a deleted workspace's schedules and links stop", func(t *testing.T) {
		clock = time.Date(2027, 1, 1, 2, 0, 0, 0, time.UTC)
		_, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantID)
		require.NoError(t, err)
		n, err := svc.RunDue(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
		st, err := svc.DescribeUnsubscribe(ctx, token)
		require.NoError(t, err)
		require.Equal(t, StateInvalid, st.State)
	})
}

func mustKey(t *testing.T, svc *Service, id uuid.UUID) []byte {
	t.Helper()
	var key []byte
	require.NoError(t, svc.db.QueryRowContext(context.Background(), `SELECT unsubscribe_key FROM sla_report_schedules WHERE id = $1`, id).Scan(&key))
	return key
}
