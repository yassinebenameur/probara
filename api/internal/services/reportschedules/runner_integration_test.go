package reportschedules

// Runner failure modes against Postgres: a batch spanning schedules with a
// failure or a stop in the middle, a slow relay seen by two replicas,
// catch-up after missed closes, a period carried over a definition change,
// and a shutdown mid-send. The invariant throughout: a row is uncertain only
// when its send started, and no recipient gets a period twice.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/mail"
)

// flakyReports fails Get for the SLAs marked, like a dropped connection.
type flakyReports struct {
	Reports
	mu   sync.Mutex
	fail map[uuid.UUID]bool
}

func (r *flakyReports) Get(ctx context.Context, tenantID, id uuid.UUID) (*models.SLA, error) {
	r.mu.Lock()
	failing := r.fail[id]
	r.mu.Unlock()
	if failing {
		return nil, errors.New("read tcp: connection reset by peer")
	}
	return r.Reports.Get(ctx, tenantID, id)
}

func (r *flakyReports) set(id uuid.UUID, failing bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail[id] = failing
}

// gateMailer holds its first message until released or the context ends,
// announcing it on started; every other message goes straight through.
type gateMailer struct {
	fakeMailer
	once    sync.Once
	started chan string
	release chan struct{}
}

func newGateMailer() *gateMailer {
	return &gateMailer{started: make(chan string, 1), release: make(chan struct{})}
}

func (g *gateMailer) SendMessage(ctx context.Context, m mail.Message) error {
	hold := false
	g.once.Do(func() { hold = true })
	if hold {
		g.started <- m.To[0]
		select {
		case <-g.release:
		case <-ctx.Done():
			return &mail.DeliveryError{Failed: []mail.RecipientFailure{{Recipient: m.To[0], Err: ctx.Err()}}}
		}
	}
	return g.fakeMailer.SendMessage(ctx, m)
}

func (f *fixture) serviceWith(reports Reports, mailer mail.Sender, at *time.Time) *Service {
	s := NewService(f.db, reports, Config{
		Mailer: mailer, Links: actionlink.New(testKeys{}), AppBaseURL: "https://probara.example.com", Audit: &recorder{},
	})
	s.now = func() time.Time { return *at }
	return s
}

// rowState is every delivery's status, attempts and whether a claim holds it.
type rowState struct {
	status   string
	attempts int
	held     bool
}

func (f *fixture) rows(t *testing.T) map[string]rowState {
	t.Helper()
	rows, err := f.db.QueryContext(f.ctx, `SELECT address, status, attempts, claim_id IS NOT NULL FROM sla_report_deliveries`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]rowState{}
	for rows.Next() {
		var a string
		var st rowState
		require.NoError(t, rows.Scan(&a, &st.status, &st.attempts, &st.held))
		out[a] = st
	}
	require.NoError(t, rows.Err())
	return out
}

func (f *fixture) moreSLAs(t *testing.T, names ...string) []*models.SLA {
	t.Helper()
	var out []*models.SLA
	for _, n := range names {
		sla, err := f.reports.Create(f.ctx, f.tenantID, &models.CreateSLARequest{Name: n, TargetPct: 99.9, MonitorIDs: []string{f.sla.MonitorIDs[0].String()}})
		require.NoError(t, err)
		out = append(out, sla)
	}
	return out
}

func planAll(t *testing.T, s *Service) {
	t.Helper()
	for {
		ran, err := s.planOne(context.Background())
		require.NoError(t, err)
		if !ran {
			return
		}
	}
}

// TestBatchSurvivesAFailingSchedule: one batch reserves the deliveries of
// three schedules and the middle one's report cannot be prepared (a dropped
// connection). The others are sent; its rows wait for a retry, uncounted,
// never uncertain, and go out once it recovers.
func TestBatchSurvivesAFailingSchedule(t *testing.T) {
	f := setup(t)
	now := sep15
	reports := &flakyReports{Reports: f.reports, fail: map[uuid.UUID]bool{}}
	mailer := &fakeMailer{}
	s := f.serviceWith(reports, mailer, &now)
	extra := f.moreSLAs(t, "Search", "Payments")
	for i, sla := range append([]*models.SLA{f.sla}, extra...) {
		_, err := s.Put(f.ctx, f.tenantID, sla.ID, &models.PutSLAReportScheduleRequest{
			Recipients: []string{"a" + string(rune('0'+i)) + "@example.com", "b" + string(rune('0'+i)) + "@example.com"}})
		require.NoError(t, err)
	}
	now = afterDue
	planAll(t, s)

	reports.set(extra[0].ID, true)
	n, err := s.deliverBatch(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 6, n, "one batch spans the three schedules")
	require.Equal(t, []string{"a0@example.com", "a2@example.com", "b0@example.com", "b2@example.com"}, mailer.recipients())
	rows := f.rows(t)
	for _, a := range []string{"a1@example.com", "b1@example.com"} {
		require.Equal(t, rowState{status: models.SLAReportDeliveryPending}, rows[a], a)
	}
	resp, err := f.service(nil, &now).Get(f.ctx, f.tenantID, extra[0].ID)
	require.NoError(t, err)
	require.Contains(t, resp.Schedule.LastError, "2026-09: 2 retrying of 2 recipients: prepare the report")

	reports.set(extra[0].ID, false)
	now = now.Add(planRetryDelay)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.recipients(), 6)
	resp, err = s.Get(f.ctx, f.tenantID, extra[0].ID)
	require.NoError(t, err)
	require.Empty(t, resp.Schedule.LastError)
}

// TestStopMidBatchHandsTheRestBack: the run is cut short after its first
// schedule; every row it reserved and did not send is pending again,
// uncounted and unheld, and the next run sends each exactly once.
func TestStopMidBatchHandsTheRestBack(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	extra := f.moreSLAs(t, "Search", "Payments")
	for i, sla := range append([]*models.SLA{f.sla}, extra...) {
		_, err := s.Put(f.ctx, f.tenantID, sla.ID, &models.PutSLAReportScheduleRequest{
			Recipients: []string{"a" + string(rune('0'+i)) + "@example.com", "b" + string(rune('0'+i)) + "@example.com"}})
		require.NoError(t, err)
	}
	now = afterDue
	planAll(t, s)

	ctx, cancel := context.WithCancel(f.ctx)
	s.afterGroup = cancel
	_, err := s.deliverBatch(ctx)
	require.NoError(t, err)
	require.Len(t, mailer.recipients(), 2, "only the first schedule went out")
	sent := 0
	for a, st := range f.rows(t) {
		switch st.status {
		case models.SLAReportDeliverySent:
			sent++
		case models.SLAReportDeliveryPending:
			require.Equal(t, rowState{status: models.SLAReportDeliveryPending}, st, a)
		default:
			t.Fatalf("%s left %s", a, st.status)
		}
	}
	require.Equal(t, 2, sent)

	s.afterGroup = nil
	require.NoError(t, s.RunOnce(f.ctx))
	got := mailer.recipients()
	require.Len(t, got, 6)
	for i := 1; i < len(got); i++ {
		require.NotEqual(t, got[i-1], got[i], "nobody twice")
	}
}

// TestSlowRelayWithTwoReplicas: replica A's relay hangs on its first message
// while the rest of its batch waits; replica B, 16 minutes later, hands A's
// waiting rows back and sends them, and closes A's in-flight send as
// uncertain. Only that send, which really started, is uncertain, A sends
// nothing else, and nobody gets the report twice.
func TestSlowRelayWithTwoReplicas(t *testing.T) {
	f := setup(t)
	nowA := sep15
	gate := newGateMailer()
	a := f.service(gate, &nowA)
	addrs := manyAddresses(5)
	f.put(t, a, addrs...)
	nowA = afterDue
	planAll(t, a)

	done := make(chan error, 1)
	go func() {
		_, err := a.deliverBatch(context.Background())
		done <- err
	}()
	inFlight := <-gate.started

	nowB := afterDue.Add(staleClaimAfter + time.Minute)
	mailerB := &fakeMailer{}
	b := f.service(mailerB, &nowB)
	require.NoError(t, b.RunOnce(f.ctx))
	close(gate.release)
	require.NoError(t, <-done)

	require.Equal(t, []string{inFlight}, gate.recipients(), "A sent only what it had started")
	var rest []string
	for _, x := range addrs {
		if x != inFlight {
			rest = append(rest, x)
		}
	}
	sort.Strings(rest)
	require.Equal(t, rest, mailerB.recipients())
	for addr, st := range f.rows(t) {
		want := models.SLAReportDeliverySent
		if addr == inFlight {
			want = models.SLAReportDeliveryUncertain
		}
		require.Equal(t, want, st.status, addr)
		require.False(t, st.held, addr)
	}
}

// TestCatchUpAfterMissedCloses: an API down from June to October sends the
// last three closed periods once each, oldest first; June is skipped.
func TestCatchUpAfterMissedCloses(t *testing.T) {
	f := setup(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)

	now = afterDue
	require.NoError(t, s.RunOnce(f.ctx))
	var periods []string
	for _, e := range s.audit.(*recorder).events {
		periods = append(periods, e.Details["period"].(string))
	}
	require.Equal(t, []string{"2026-07", "2026-08", "2026-09"}, periods, "oldest first, the last three")
	perPeriod := map[string]int{}
	for _, m := range mailer.sent {
		for _, p := range []string{"2026-06", "2026-07", "2026-08", "2026-09"} {
			if strings.Contains(m.Subject, ", "+p+":") {
				perPeriod[p]++
			}
		}
	}
	require.Equal(t, map[string]int{"2026-07": 2, "2026-08": 2, "2026-09": 2}, perPeriod)

	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.sent, 6, "each once")
	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Equal(t, "2026-09", resp.Schedule.LastPeriodKey)
	require.Equal(t, time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), resp.Schedule.NextRunAt)
}

// TestCarriedPeriodIsSentAsDefined: the SLA went from monthly to weekly in
// the grace hour after September closed (the SLA service carries
// September over; TestSLAService covers that). September still goes out
// once, frozen as monthly, and weekly periods closed before the change do
// not.
func TestCarriedPeriodIsSentAsDefined(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	changed := octClose.Add(30 * time.Minute)
	_, err := f.db.ExecContext(f.ctx, `UPDATE slas SET period = 'weekly' WHERE id = $1`, f.sla.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `
		UPDATE sla_report_schedules SET carry_period_kind = 'monthly', carry_timezone = 'UTC', carry_period_key = '2026-09',
			sends_after = $1, next_run_at = $2
	`, changed, octDue)
	require.NoError(t, err)

	now = changed.Add(10 * time.Minute)
	require.NoError(t, s.RunOnce(f.ctx))
	require.Empty(t, mailer.sent, "the carried period waits for its own grace")

	now = afterDue
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.sent, 2)
	for _, m := range mailer.sent {
		require.Contains(t, m.Subject, "Checkout, 2026-09:")
	}
	r, err := f.reports.IssuedReportStarting(f.ctx, f.tenantID, f.sla.ID, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "monthly", r.Definition.Period, "frozen as it was defined")
	require.Equal(t, "report schedule", r.Issued.IssuedBy)

	resp, err := s.Get(f.ctx, f.tenantID, f.sla.ID)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC), resp.Schedule.NextRunAt, "then the running week's close")
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.sent, 2)
}

// TestShutdownMidSend: stopping the runner while a message is being sent
// cuts it short before the relay took it; that row and the rest of the
// batch go back to pending with no attempt counted, nothing is uncertain,
// and the claimed period's send event was recorded.
func TestShutdownMidSend(t *testing.T) {
	f := setup(t)
	now := sep15
	gate := newGateMailer()
	s := f.service(gate, &now)
	f.put(t, s, recipient...)
	now = afterDue

	r := NewRunner(s)
	r.Start()
	<-gate.started
	r.Stop()

	require.Empty(t, gate.recipients())
	for addr, st := range f.rows(t) {
		require.Equal(t, rowState{status: models.SLAReportDeliveryPending}, st, addr)
	}
	events := s.audit.(*recorder).events
	require.Len(t, events, 1)
	require.Equal(t, "sla-report-schedule.send", events[0].Action)
}
