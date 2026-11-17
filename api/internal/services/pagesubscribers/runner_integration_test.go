//go:build !unit

package pagesubscribers

// Fan-out failure modes against Postgres: two runner replicas, a runner
// that died between claim and send (and one that died mid-send), a slow
// relay seen by a second replica, and a failing recipient. The invariant
// throughout: every confirmed subscriber gets each notice exactly once, and
// a delivery is uncertain only when its send started.

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/mail"
)

// confirmedMany inserts n confirmed subscribers directly.
func (f *fixture) confirmedMany(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for i := 0; i < n; i++ {
		a := fmt.Sprintf("sub%03d@example.com", i)
		_, err := f.db.ExecContext(f.ctx, `
			INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, state, confirmed_at)
			VALUES ($1, $2, $3, $4, 'confirmed', NOW())
		`, f.tenantID, f.pageID, a, AddressHash(a))
		require.NoError(t, err)
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func TestRunner_TwoReplicasSendOnce(t *testing.T) {
	f := setup(t)
	want := f.confirmedMany(t, 60)
	mailer := &fakeMailer{}
	a, b := f.service(mailer, nil), f.service(mailer, nil)
	f.publish(t, "Replicated")
	var wg sync.WaitGroup
	for _, s := range []*Service{a, b, a, b} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			require.NoError(t, s.RunOnce(context.Background()))
		}(s)
	}
	wg.Wait()
	run(t, a)
	require.Equal(t, want, mailer.recipients("Replicated"))
	require.Equal(t, map[string]int{"sent": 60}, f.deliveryStates(t))
	var planned int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT recipients FROM status_page_notification_outbox`).Scan(&planned))
	require.Equal(t, 60, planned)
}

// A runner that died after reserving deliveries sent nothing: they go back
// and are sent once. One that died after starting a send may have sent it:
// that one is closed as uncertain and never sent again.
func TestRunner_CrashBetweenClaimAndSend(t *testing.T) {
	f := setup(t)
	f.confirmedMany(t, 3)
	mailer := &fakeMailer{}
	now := time.Now().UTC()
	s := f.service(mailer, &now)
	f.publish(t, "Crash")
	require.NoError(t, s.reclaimStale(f.ctx))
	planned, err := s.planOne(f.ctx)
	require.NoError(t, err)
	require.True(t, planned)

	// The dead runner's claim: two reserved, one in the middle of sending.
	_, err = f.db.ExecContext(f.ctx, `
		UPDATE status_page_notification_deliveries d SET status = 'claimed', claim_id = gen_random_uuid(), claimed_at = $1
		FROM status_page_subscribers s WHERE s.id = d.subscriber_id AND s.email IN ('sub000@example.com', 'sub001@example.com')
	`, now)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `
		UPDATE status_page_notification_deliveries d SET status = 'sending', attempts = 1, claim_id = gen_random_uuid(), claimed_at = $1
		FROM status_page_subscribers s WHERE s.id = d.subscriber_id AND s.email = 'sub002@example.com'
	`, now)
	require.NoError(t, err)

	run(t, s)
	require.Empty(t, mailer.recipients("Crash"), "a live claim is not taken over")

	now = now.Add(staleClaimAfter + time.Minute)
	run(t, s)
	run(t, s)
	require.Equal(t, []string{"sub000@example.com", "sub001@example.com"}, mailer.recipients("Crash"))
	require.Equal(t, map[string]int{"sent": 2, "uncertain": 1}, f.deliveryStates(t))
}

// gateMailer holds its first message until released, announcing it.
type gateMailer struct {
	fakeMailer
	once    sync.Once
	started chan string
	release chan struct{}
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

// A slow relay holds one send while a second replica runs: the second
// never sends what the first holds, and every subscriber is mailed once.
func TestRunner_SlowRelay(t *testing.T) {
	f := setup(t)
	want := f.confirmedMany(t, 5)
	gate := &gateMailer{started: make(chan string, 1), release: make(chan struct{})}
	a := f.service(gate, nil)
	a.sendConcurrency = 1
	b := f.service(&gate.fakeMailer, nil)
	f.publish(t, "Slow")

	done := make(chan error, 1)
	go func() { done <- a.RunOnce(context.Background()) }()
	held := <-gate.started
	require.NoError(t, b.RunOnce(context.Background()))
	require.NotContains(t, gate.recipients("Slow"), held, "the second replica sent what the first holds")
	close(gate.release)
	require.NoError(t, <-done)
	require.NoError(t, b.RunOnce(context.Background()))
	require.Equal(t, want, gate.recipients("Slow"))
	require.Equal(t, map[string]int{"sent": 5}, f.deliveryStates(t))
}

// A failing recipient is retried with backoff without holding back the
// others; a relay that refuses outright (5xx) fails it for good; a message
// that may have been delivered is never sent again.
func TestRunner_FailingRecipient(t *testing.T) {
	f := setup(t)
	f.confirmedMany(t, 4)
	mailer := &fakeMailer{}
	now := time.Now().UTC()
	s := f.service(mailer, &now)
	mailer.setFail(func(to string) error {
		switch to {
		case "sub000@example.com":
			return errors.New("dial tcp: connection refused")
		case "sub001@example.com":
			return &textproto.Error{Code: 550, Msg: "mailbox unavailable"}
		case "sub002@example.com":
			return fmt.Errorf("%w: reading the reply to DATA", mail.ErrMaybeDelivered)
		}
		return nil
	})
	f.publish(t, "Flaky")
	run(t, s)
	require.Equal(t, []string{"sub003@example.com"}, mailer.recipients("Flaky"))
	require.Equal(t, map[string]int{"sent": 1, "pending": 1, "failed": 1, "uncertain": 1}, f.deliveryStates(t))

	run(t, s)
	require.Equal(t, []string{"sub003@example.com"}, mailer.recipients("Flaky"), "retried before its backoff")

	mailer.setFail(nil)
	now = now.Add(retryBackoff[0] + time.Second)
	run(t, s)
	require.Equal(t, []string{"sub000@example.com", "sub003@example.com"}, mailer.recipients("Flaky"))
	require.Equal(t, map[string]int{"sent": 2, "failed": 1, "uncertain": 1}, f.deliveryStates(t))
	var attempts int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `
		SELECT d.attempts FROM status_page_notification_deliveries d JOIN status_page_subscribers s ON s.id = d.subscriber_id
		WHERE s.email = 'sub000@example.com'`).Scan(&attempts))
	require.Equal(t, 2, attempts)
}

// A large page is fanned out in bounded rounds that interleave with other
// pages' notices.
func TestRunner_BoundedRoundsAcrossPages(t *testing.T) {
	f := setup(t)
	f.confirmedMany(t, deliveriesPerOutbox*3)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.publish(t, "Big page")
	require.NoError(t, s.reclaimStale(f.ctx))
	_, err := s.planOne(f.ctx)
	require.NoError(t, err)
	n, err := s.deliverBatch(f.ctx)
	require.NoError(t, err)
	require.Equal(t, deliveriesPerOutbox, n, "one round takes a bounded share of one row")
}
