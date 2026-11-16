//go:build !unit

package reportschedules

// Emailed SLA report schedules against Postgres: due selection, the claim
// committed before any mail, two runners, retries, interrupted sends,
// unsubscribes and the mail-configuration gate.

import (
	"bytes"
	"context"
	"errors"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	"github.com/yassinebenameur/probara/shared/actionlink"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
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

func (f *fakeMailer) recipients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		out = append(out, m.To[0])
	}
	sort.Strings(out)
	return out
}

func (f *fakeMailer) setFail(fn func(string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fn
}

type fixture struct {
	ctx      context.Context
	db       *shareddb.Client
	tenantID uuid.UUID
	sla      *models.SLA
	reports  *slas.Service
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "reports")
	m := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "checkout")
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, 'up', 'result', $3, $4)
	`, tenantID, m, aug1, nov1)
	require.NoError(t, err)
	reports := slas.NewService(dbClient, sharedanalytics.NewRepository(dbClient), true)
	sla, err := reports.Create(ctx, tenantID, &models.CreateSLARequest{Name: "Checkout", TargetPct: 99.9, MonitorIDs: []string{m.String()}})
	require.NoError(t, err)
	return &fixture{ctx: ctx, db: dbClient, tenantID: tenantID, sla: sla, reports: reports}
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

func (f *fixture) service(mailer mail.Sender, at *time.Time) *Service {
	s := NewService(f.db, f.reports, Config{
		Mailer: mailer, Links: actionlink.New(testKeys{}), AppBaseURL: "https://probara.example.com/", Audit: &recorder{},
	})
	s.now = func() time.Time { return *at }
	return s
}

func (f *fixture) put(t *testing.T, s *Service, recipients ...string) *models.SLAReportSchedule {
	t.Helper()
	resp, err := s.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Recipients: recipients})
	require.NoError(t, err)
	return resp.Schedule
}

func (f *fixture) deliveries(t *testing.T, period string) map[string]string {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx, `SELECT address, status FROM sla_report_deliveries WHERE period_key = $1`, period)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var a, s string
		require.NoError(t, rows.Scan(&a, &s))
		out[a] = s
	}
	require.NoError(t, rows.Err())
	return out
}

var (
	sep15     = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	octClose  = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	octDue    = octClose.Add(slas.ReportIssueGrace)
	afterDue  = octDue.Add(30 * time.Minute)
	recipient = []string{"ops@example.com", "cfo@example.com"}
)

func TestScheduleValidationAndMailGate(t *testing.T) {
	f := setup(t)
	now := sep15
	s := f.service(&fakeMailer{}, &now)

	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Nil(t, resp.Schedule)
	require.True(t, resp.MailReady)
	require.Empty(t, resp.MailProblems)

	for want, req := range map[string]models.PutSLAReportScheduleRequest{
		"at least one recipient":   {Recipients: []string{" "}},
		"not a plain email":        {Recipients: []string{"Ops <ops@example.com>"}},
		`"bad" is not a plain`:     {Recipients: []string{"bad"}},
		"is not a recipient":       {Recipients: []string{"a@example.com"}, Resubscribe: []string{"b@example.com"}},
		"at most 50 recipients":    {Recipients: manyAddresses(51)},
		"quoted\"@x.example.com\"": {Recipients: []string{`"quoted"@x.example.com`}},
	} {
		_, err := s.Put(f.ctx, f.tenantID, f.sla.ID, &req)
		var bad Invalid
		require.ErrorAs(t, err, &bad, want)
		if !strings.HasPrefix(want, "quoted") {
			require.Contains(t, err.Error(), want)
		}
	}

	sc := f.put(t, s, " Ops@Example.com ", "ops@example.com", "cfo@example.com")
	require.Equal(t, []string{"ops@example.com", "cfo@example.com"}, sc.Recipients, "trimmed, lower-cased, de-duplicated")
	require.True(t, sc.Enabled)
	require.Equal(t, octDue, sc.NextRunAt, "first send: the running period's close plus the grace")
	require.Empty(t, sc.Deliveries)

	// Tenant isolation: another workspace sees no SLA.
	other := testutil.InsertTenant(f.ctx, t, f.db, "other")
	_, err = s.Get(f.ctx, other, f.sla.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.Put(f.ctx, other, f.sla.ID, &models.PutSLAReportScheduleRequest{Recipients: recipient})
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, s.Delete(f.ctx, other, f.sla.ID), ErrNotFound)

	// Without SMTP, APP_BASE_URL or PROBARA_SECRETS_KEY a schedule can be
	// kept disabled but never enabled, and the API says which is missing.
	for want, cfg := range map[string]Config{
		"SMTP":                {Links: actionlink.New(testKeys{}), AppBaseURL: "https://x.example.com"},
		"APP_BASE_URL":        {Mailer: &fakeMailer{}, Links: actionlink.New(testKeys{})},
		"PROBARA_SECRETS_KEY": {Mailer: &fakeMailer{}, AppBaseURL: "https://x.example.com"},
	} {
		off := NewService(f.db, f.reports, cfg)
		require.False(t, off.Ready())
		_, err := off.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Recipients: recipient})
		var bad Invalid
		require.ErrorAs(t, err, &bad)
		require.Contains(t, err.Error(), want)
		disabled := false
		resp, err := off.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Enabled: &disabled, Recipients: recipient})
		require.NoError(t, err, want)
		require.False(t, resp.MailReady)
		require.Len(t, resp.MailProblems, 1)
		require.Contains(t, resp.MailProblems[0], want)
		// A runner without mail never starts.
		NewRunner(off).Start()
	}

	require.NoError(t, s.Delete(f.ctx, f.tenantID, f.sla.ID))
	require.ErrorIs(t, s.Delete(f.ctx, f.tenantID, f.sla.ID), ErrNotFound)
}

func manyAddresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "r" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@example.com"
	}
	return out
}

// TestRunnerSendsAtPeriodClose: nothing before the period closes (plus the
// grace); then the report is issued by the schedule and every recipient gets
// one message with the PDF and their own unsubscribe link, once.
func TestRunnerSendsAtPeriodClose(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)

	now = octClose.Add(30 * time.Minute) // closed, but inside the grace
	require.NoError(t, s.RunOnce(f.ctx))
	require.Empty(t, mailer.recipients(), "not due before close + grace")

	now = afterDue
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, []string{"cfo@example.com", "ops@example.com"}, mailer.recipients())
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.recipients(), 2, "a period is sent once")

	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	sc := resp.Schedule
	require.Equal(t, "2026-09", sc.LastPeriodKey)
	require.Equal(t, time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), sc.NextRunAt)
	require.Empty(t, sc.LastError)
	require.Len(t, sc.Deliveries, 2)
	for _, d := range sc.Deliveries {
		require.Equal(t, models.SLAReportDeliverySent, d.Status)
		require.NotNil(t, d.SentAt)
	}

	refs, err := f.reports.ListReports(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "2026-09", refs[0].PeriodKey)
	require.Equal(t, "report schedule", refs[0].IssuedBy)

	msg := mailer.sent[0]
	require.Contains(t, msg.Subject, "SLA report: Checkout, 2026-09: met")
	require.Len(t, msg.Attachments, 1)
	require.Equal(t, "application/pdf", msg.Attachments[0].ContentType)
	require.True(t, bytes.HasPrefix(msg.Attachments[0].Data, []byte("%PDF")))
	require.Contains(t, msg.Text, "https://probara.example.com/slas?sla="+f.sla.ID.String()+"&tenant="+f.tenantID.String())
	require.Contains(t, msg.Text, "https://probara.example.com/report-unsubscribe#")
	require.Regexp(t, `^<https://probara\.example\.com/api/v1/report-unsubscribe\?token=[A-Za-z0-9_-]+>$`, msg.Headers["List-Unsubscribe"])
	require.Equal(t, "List-Unsubscribe=One-Click", msg.Headers["List-Unsubscribe-Post"])
	require.NotEqual(t, mailer.sent[0].Headers["List-Unsubscribe"], mailer.sent[1].Headers["List-Unsubscribe"], "one link per recipient")
	require.Contains(t, msg.HTML, "Open in Probara")
	require.NotContains(t, msg.HTML, "data:", "no data: images in mail")

	// The CSV rides along when asked for.
	_, err = s.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Recipients: recipient, IncludeCSV: true})
	require.NoError(t, err)
	now = time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.recipients(), 4)
	require.Len(t, mailer.sent[3].Attachments, 2)
	require.Equal(t, "text/csv; charset=utf-8", mailer.sent[3].Attachments[1].ContentType)
	require.Empty(t, f.deliveries(t, "2026-09"), "the finished period's rows are pruned when the next is claimed")
}

// TestClaimCommitsBeforeMail: planning commits the period and one pending
// delivery per recipient before anyone is mailed, and a stale or replayed
// due time never claims the same period again.
func TestClaimCommitsBeforeMail(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)

	now = afterDue
	ran, err := s.planOne(f.ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Empty(t, mailer.recipients(), "planning mails nobody")
	require.Equal(t, map[string]string{"ops@example.com": "pending", "cfo@example.com": "pending"}, f.deliveries(t, "2026-09"))

	// A replica that read the old next_run_at finds the period claimed.
	_, err = f.db.ExecContext(f.ctx, `UPDATE sla_report_schedules SET next_run_at = $1`, octDue)
	require.NoError(t, err)
	ran, err = s.planOne(f.ctx)
	require.NoError(t, err)
	require.True(t, ran)
	var n int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM sla_report_deliveries`).Scan(&n))
	require.Equal(t, 2, n)
	ran, err = s.planOne(f.ctx)
	require.NoError(t, err)
	require.False(t, ran, "re-planned to November")

	// Due early (inside the grace of a period that just closed): wait.
	_, err = f.db.ExecContext(f.ctx, `UPDATE sla_report_schedules SET next_run_at = $1`, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	now = time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	ran, err = s.planOne(f.ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Empty(t, f.deliveries(t, "2026-10"))
	var next time.Time
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT next_run_at FROM sla_report_schedules`).Scan(&next))
	require.Equal(t, time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), next.UTC())
}

// TestTwoRunnersNeverDoubleSend runs two replicas' runners at once, over
// and over: each recipient gets the period exactly once.
func TestTwoRunnersNeverDoubleSend(t *testing.T) {
	f := setup(t)
	now := sep15
	addrs := manyAddresses(20)
	a, b := &fakeMailer{}, &fakeMailer{}
	sa, sb := f.service(a, &now), f.service(b, &now)
	f.put(t, sa, addrs...)
	now = afterDue

	var wg sync.WaitGroup
	for _, s := range []*Service{sa, sb, sa, sb} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				assert.NoError(t, s.RunOnce(f.ctx))
			}
		}(s)
	}
	wg.Wait()
	got := append(a.recipients(), b.recipients()...)
	sort.Strings(got)
	want := append([]string(nil), addrs...)
	sort.Strings(want)
	require.Equal(t, want, got, "every recipient exactly once across both replicas")
	var issued int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM sla_reports`).Scan(&issued))
	require.Equal(t, 1, issued)
}

// TestInterruptedSendIsNeverRepeated: a delivery claimed by an API that
// died before recording the outcome may have been mailed, so it is closed
// as uncertain and surfaced, never sent again.
func TestInterruptedSendIsNeverRepeated(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	now = afterDue
	_, err := s.planOne(f.ctx)
	require.NoError(t, err)

	// Another replica claimed ops@ and died mid-send.
	_, err = f.db.ExecContext(f.ctx, `
		UPDATE sla_report_deliveries SET status = 'sending', attempts = 1, claimed_at = $1 WHERE address = 'ops@example.com'
	`, now)
	require.NoError(t, err)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, []string{"cfo@example.com"}, mailer.recipients(), "a claimed row is never taken twice")

	now = now.Add(staleSendingAfter + time.Minute)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, []string{"cfo@example.com"}, mailer.recipients())
	require.Equal(t, models.SLAReportDeliveryUncertain, f.deliveries(t, "2026-09")["ops@example.com"])
	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Contains(t, resp.Schedule.LastError, "2026-09: 1 uncertain of 2 recipients")

	// A batch that lost a row it had reserved (handed back or closed by
	// another replica) does not send it.
	var scheduleID uuid.UUID
	var start time.Time
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT schedule_id, period_start FROM sla_report_deliveries WHERE address = 'ops@example.com'`).Scan(&scheduleID, &start))
	held := claimedDelivery{scheduleID: scheduleID, tenantID: f.tenantID, periodStart: start, periodKey: "2026-09",
		addressKey: addressKey(scheduleID, "ops@example.com"), address: "ops@example.com", claimID: uuid.New()}
	s.sendGroup(f.ctx, []claimedDelivery{held})
	require.Equal(t, []string{"cfo@example.com"}, mailer.recipients())
	require.Equal(t, models.SLAReportDeliveryUncertain, f.deliveries(t, "2026-09")["ops@example.com"])

	// A lost connection after the data is uncertain too.
	_, err = f.db.ExecContext(f.ctx, `UPDATE sla_report_deliveries SET status = 'pending', attempts = 0 WHERE address = 'cfo@example.com'`)
	require.NoError(t, err)
	mailer.setFail(func(string) error { return mail.ErrMaybeDelivered })
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, models.SLAReportDeliveryUncertain, f.deliveries(t, "2026-09")["cfo@example.com"])
}

// TestFailedSendRetriesWithBackoff: a transient failure waits 5 minutes,
// then 30; a success clears the schedule's error; a refusal (5xx) or
// running out of attempts fails for good.
func TestFailedSendRetriesWithBackoff(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	now = afterDue
	mailer.setFail(func(to string) error {
		if to == "cfo@example.com" {
			return errors.New("451 try again later")
		}
		return nil
	})
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, []string{"ops@example.com"}, mailer.recipients())
	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Contains(t, resp.Schedule.LastError, "2026-09: 1 retrying of 2 recipients")
	require.Contains(t, resp.Schedule.LastError, "451 try again later")
	var cfo models.SLAReportDelivery
	for _, d := range resp.Schedule.Deliveries {
		if d.Address == "cfo@example.com" {
			cfo = d
		}
	}
	require.Equal(t, models.SLAReportDeliveryPending, cfo.Status)
	require.Equal(t, 1, cfo.Attempts)
	require.Equal(t, afterDue.Add(5*time.Minute), cfo.NextAttemptAt.UTC())

	now = afterDue.Add(4 * time.Minute)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.recipients(), 1, "not before the backoff")
	now = afterDue.Add(5 * time.Minute)
	require.NoError(t, s.RunOnce(f.ctx))
	var attempts int
	var nextAt time.Time
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT attempts, next_attempt_at FROM sla_report_deliveries WHERE address = 'cfo@example.com'`).Scan(&attempts, &nextAt))
	require.Equal(t, 2, attempts)
	require.Equal(t, now.Add(30*time.Minute), nextAt.UTC())

	mailer.setFail(nil)
	now = now.Add(30 * time.Minute)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, []string{"cfo@example.com", "ops@example.com"}, mailer.recipients())
	resp, err = s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Empty(t, resp.Schedule.LastError, "cleared once every recipient has it")

	// A refusal is final at once; a transient failure after the last
	// attempt is too.
	require.Equal(t, models.SLAReportDeliveryFailed, outcomeOf(t, f, s, &textproto.Error{Code: 550, Msg: "mailbox unavailable"}, 1))
	require.Equal(t, models.SLAReportDeliveryFailed, outcomeOf(t, f, s, mail.ErrInvalidMessage, 1))
	require.Equal(t, models.SLAReportDeliveryFailed, outcomeOf(t, f, s, errors.New("timeout"), maxAttempts))
	require.Equal(t, models.SLAReportDeliveryPending, outcomeOf(t, f, s, errors.New("timeout"), maxAttempts-1))
}

// outcomeOf records err for a delivery sent for its attempt'th time.
func outcomeOf(t *testing.T, f *fixture, s *Service, err error, attempt int) string {
	t.Helper()
	var scheduleID uuid.UUID
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM sla_report_schedules`).Scan(&scheduleID))
	addr := "probe-" + uuid.NewString()[:8] + "@example.com"
	d := claimedDelivery{scheduleID: scheduleID, tenantID: f.tenantID, periodStart: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		periodKey: "probe", addressKey: addressKey(scheduleID, addr), address: addr, attempts: attempt, claimID: uuid.New()}
	_, dbErr := f.db.ExecContext(f.ctx, `
		INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_start, period_key, address_key, address, status, attempts, claim_id, claimed_at)
		VALUES ($1, $2, $3, 'probe', $4, $5, 'sending', $6, $7, NOW())
	`, scheduleID, f.tenantID, d.periodStart, d.addressKey, addr, attempt, d.claimID)
	require.NoError(t, dbErr)
	s.recordOutcome(f.ctx, d, err)
	var status string
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT status FROM sla_report_deliveries WHERE address_key = $1`, d.addressKey).Scan(&status))
	return status
}

// TestUnsubscribe: the link's GET describes, its POST unsubscribes (once,
// idempotently), the address is skipped from then on and shown as
// unsubscribed, and re-adding it re-subscribes.
func TestUnsubscribe(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	now = afterDue
	require.NoError(t, s.RunOnce(f.ctx))

	var token string
	for _, m := range mailer.sent {
		if m.To[0] == "ops@example.com" {
			u, err := url.Parse(strings.Trim(m.Headers["List-Unsubscribe"], "<>"))
			require.NoError(t, err)
			token = u.Query().Get("token")
		}
	}
	require.NotEmpty(t, token)

	got, err := s.DescribeUnsubscribe(f.ctx, token)
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeResponse{State: models.ReportUnsubscribeSubscribed, SLAName: "Checkout"}, got)
	tampered := []byte(token)
	// Another base64url character in the payload: well-formed, wrong MAC.
	if tampered[10] == 'A' {
		tampered[10] = 'B'
	} else {
		tampered[10] = 'A'
	}
	for _, bad := range []string{"", "nonsense", string(tampered)} {
		got, err := s.DescribeUnsubscribe(f.ctx, bad)
		require.NoError(t, err)
		require.Equal(t, models.ReportUnsubscribeResponse{State: models.ReportUnsubscribeInvalid}, got, "nothing leaks for a bad link")
	}

	rec := s.audit.(*recorder)
	got, err = s.Unsubscribe(f.ctx, token, audit.Event{IP: "203.0.113.9"})
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeUnsubscribed, got.State)
	got, err = s.Unsubscribe(f.ctx, token, audit.Event{})
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeUnsubscribed, got.State, "repeating it is a no-op")
	require.Len(t, rec.events, 2, "the runner's send, then one unsubscribe")
	require.Equal(t, "sla-report-schedule.unsubscribe", rec.events[1].Action)
	require.Equal(t, f.tenantID, *rec.events[1].TenantID)
	for _, e := range rec.events {
		require.Equal(t, f.sla.ID.String(), e.ResourceID, "every schedule event names the SLA")
	}

	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com"}, resp.Schedule.Unsubscribed)

	now = time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Equal(t, models.SLAReportDeliverySkipped, f.deliveries(t, "2026-10")["ops@example.com"])
	require.Equal(t, models.SLAReportDeliverySent, f.deliveries(t, "2026-10")["cfo@example.com"])

	// Keeping the address listed keeps it unsubscribed; naming it in
	// resubscribe, or removing and re-adding it, re-subscribes.
	f.put(t, s, recipient...)
	resp, err = s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com"}, resp.Schedule.Unsubscribed)
	_, err = s.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Recipients: recipient, Resubscribe: []string{"OPS@example.com"}})
	require.NoError(t, err)
	got, err = s.DescribeUnsubscribe(f.ctx, token)
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeSubscribed, got.State)
	_, err = s.Unsubscribe(f.ctx, token, audit.Event{})
	require.NoError(t, err)
	f.put(t, s, "cfo@example.com")
	f.put(t, s, recipient...)
	resp, err = s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Empty(t, resp.Schedule.Unsubscribed, "re-adding the address re-subscribes it")

	// A token naming a schedule of another workspace is invalid, as is one
	// for an expired link and one after the schedule is deleted.
	var scheduleID uuid.UUID
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM sla_report_schedules`).Scan(&scheduleID))
	key, _ := keyID(addressKey(scheduleID, "ops@example.com"))
	signer := actionlink.New(testKeys{})
	foreign, err := signer.ActionToken(actionlink.ActionUnsubscribeReport, uuid.NewString(), scheduleID.String(), key.String(), time.Now())
	require.NoError(t, err)
	got, err = s.DescribeUnsubscribe(f.ctx, foreign)
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeInvalid, got.State)
	stale, err := signer.ActionToken(actionlink.ActionUnsubscribeReport, f.tenantID.String(), scheduleID.String(), key.String(),
		time.Now().Add(-actionlink.UnsubscribeTTL-time.Hour))
	require.NoError(t, err)
	got, err = s.Unsubscribe(f.ctx, stale, audit.Event{})
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeResponse{State: models.ReportUnsubscribeExpired}, got)
	require.NoError(t, s.Delete(f.ctx, f.tenantID, f.sla.ID))
	got, err = s.DescribeUnsubscribe(f.ctx, token)
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeInvalid, got.State, "deleting the schedule revokes its links")
}

// TestDisableAndRemoveCancelPending: what waits for a retry is cancelled
// when the schedule is disabled or its recipient removed, and re-enabling
// does not send the periods missed meanwhile.
func TestDisableAndRemoveCancelPending(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	now = afterDue
	_, err := s.planOne(f.ctx)
	require.NoError(t, err)

	f.put(t, s, "ops@example.com")
	require.Equal(t, models.SLAReportDeliveryCancelled, f.deliveries(t, "2026-09")["cfo@example.com"])
	off := false
	_, err = s.Put(f.ctx, f.tenantID, f.sla.ID, &models.PutSLAReportScheduleRequest{Enabled: &off, Recipients: []string{"ops@example.com"}})
	require.NoError(t, err)
	require.Equal(t, models.SLAReportDeliveryCancelled, f.deliveries(t, "2026-09")["ops@example.com"])
	require.NoError(t, s.RunOnce(f.ctx))
	require.Empty(t, mailer.recipients())

	now = time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)
	sc := f.put(t, s, "ops@example.com")
	require.Equal(t, time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC), sc.NextRunAt, "re-enabled: the next close, not the missed ones")
	// Even when due at once, October and November (closed while it was
	// off) are not sent.
	_, err = f.db.ExecContext(f.ctx, `UPDATE sla_report_schedules SET next_run_at = $1`, now)
	require.NoError(t, err)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Empty(t, mailer.recipients())
	require.Empty(t, f.deliveries(t, "2026-10"))
	require.Empty(t, f.deliveries(t, "2026-11"))
}
