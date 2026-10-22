package airca

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// The hardening both AI job consumers share (AI_RCA incident analyses and
// AI_TRIAGE alert triage): stream/durable provisioning with durable recovery,
// per-job advisory locking, and the mapping of a run's outcome onto the
// queue (ack, delayed retry, or a recorded failure on the final delivery).

// errJobBusy reports that another delivery of the same job holds its advisory
// lock. It is the one handler error that is not a failure of this run.
var errJobBusy = errors.New("job already being processed")

// errAnalysisBusy is the AI_RCA name for errJobBusy.
var errAnalysisBusy = errJobBusy

// streamSpec names one AI job stream and its durable consumer.
type streamSpec struct {
	Stream, Subject, Consumer string
	Label                     string        // "AI RCA" / "AI triage", for logs and errors
	MaxAge                    time.Duration // AI_RCA 24h, AI_TRIAGE 1h
	Duplicates                time.Duration // Nats-Msg-Id window; 0 = server default (AI_RCA)
}

// ensureStream creates or updates spec's work-queue stream. Every caller of a
// stream goes through it with the same spec, so a restart of one side never
// resets settings the other relies on (the AI_TRIAGE Duplicates window).
func ensureStream(ctx context.Context, q *queue.Client, spec streamSpec) (jetstream.Stream, error) {
	stream, err := q.EnsureWorkQueueStreamWithOptions(ctx, spec.Stream, []string{spec.Subject},
		queue.WorkQueueStreamOptions{MaxAge: spec.MaxAge, Duplicates: spec.Duplicates})
	if err != nil {
		return nil, fmt.Errorf("ensure %s stream: %w", spec.Label, err)
	}
	return stream, nil
}

// runConsumer ensures the work-queue stream and its durable exist, then blocks
// on Consume until ctx is cancelled. It returns on any Consume failure, or
// when the durable disappears from the server; callers run it under
// RunWithRestart.
func runConsumer(ctx context.Context, q *queue.Client, log *logger.Logger, localAITimeout, checkEvery time.Duration,
	spec streamSpec, handle func(context.Context, *queue.Message) error) error {
	streamHandle, err := ensureStream(ctx, q, spec)
	if err != nil {
		return err
	}

	// AckWait must outlast a single run (local AI round-trip) plus headroom so
	// JetStream doesn't redeliver while we're still calling the model. The
	// handler's InProgress heartbeat also resets it while a job is running.
	ackWait := localAITimeout + ackWaitHeadroom
	opts := queue.ConsumerOptions{
		AckWait:    ackWait,
		MaxDeliver: maxDeliver,
		BackOff:    []time.Duration{ackWait, 2 * ackWait},
	}
	consumer, err := q.CreateConsumerWithOptions(ctx, spec.Stream, spec.Consumer, opts)
	if err != nil {
		return fmt.Errorf("create %s consumer: %w", spec.Label, err)
	}
	// queue.Client caches consumer handles and hands the cached one back
	// without asking the server, so after the durable was deleted (nats
	// consumer rm, a broker that lost its store) a restart would pull from a
	// consumer that no longer exists. Those pulls fail only with statuses the
	// iterator retries silently, so Consume would block forever. Confirm the
	// durable on the server and re-create it with the same options if it is
	// gone; the handle addresses the consumer by name, so the cached one
	// works again once the durable exists.
	if _, err := consumer.Info(ctx); isConsumerMissing(err) {
		log.WithField("consumer", spec.Consumer).Warn(spec.Label + " durable missing on the server; re-creating it")
		consumer, err = streamHandle.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
			Name:       spec.Consumer,
			Durable:    spec.Consumer,
			AckPolicy:  jetstream.AckExplicitPolicy,
			AckWait:    opts.AckWait,
			MaxDeliver: opts.MaxDeliver,
			BackOff:    opts.BackOff,
		})
		if err != nil {
			return fmt.Errorf("re-create %s consumer: %w", spec.Label, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect %s consumer: %w", spec.Label, err)
	}

	log.WithFields(logrus.Fields{
		"stream":   spec.Stream,
		"subject":  spec.Subject,
		"consumer": spec.Consumer,
	}).Info(spec.Label + " consumer started")

	// The run context also ends when watchConsumer finds the durable gone, so
	// RunWithRestart re-runs the consumer (which re-creates it). Handlers keep
	// the outer ctx: only a real shutdown may cut a job short.
	runCtx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	go watchConsumer(runCtx, stopRun, consumer, checkEvery, spec.Label)
	err = q.Consume(runCtx, consumer, func(msg *queue.Message) error {
		return handle(ctx, msg)
	})
	if ctx.Err() == nil {
		if cause := context.Cause(runCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return cause
		}
	}
	return err
}

// errConsumerMissing ends a run whose durable vanished from the server.
var errConsumerMissing = errors.New("consumer no longer exists on the server")

// watchConsumer polls the durable's info and stops the run once the server
// reports it (or its stream) missing. A broker that restarts with an empty
// store sends no ErrConsumerDeleted, and pulls against a missing consumer are
// retried silently by the iterator, so without this check Consume would block
// forever on a queue nobody serves. Other errors (reconnects, timeouts) are
// left to the next tick.
func watchConsumer(ctx context.Context, stop context.CancelCauseFunc, consumer jetstream.Consumer, every time.Duration, label string) {
	if every <= 0 {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := consumer.Info(ctx); isConsumerMissing(err) {
				stop(fmt.Errorf("%s %w: %w", label, errConsumerMissing, err))
				return
			}
		}
	}
}

func isConsumerMissing(err error) bool {
	return errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound)
}

// jobLockKeySQL is the advisory-lock key for one job, given a SQL expression
// yielding its id as canonical uuid text. A consumer (session lock, pinned
// connection) and the stale sweep (transaction lock) must use the same key:
// session and transaction advisory locks on one key conflict, which is what
// keeps the sweep off rows a consumer is processing. Analysis and triage ids
// are distinct UUIDs, so the two job kinds share the key space safely.
func jobLockKeySQL(idText string) string {
	return "hashtextextended(" + idText + ",0)"
}

// withJobLock runs fn while holding the job's session advisory lock on one
// pinned connection (CLAUDE.md: acquire and release on the same connection).
// It returns errJobBusy without running fn when another session holds it. If
// the unlock fails, the connection is discarded so the session (and its lock)
// ends instead of returning to the pool still locked.
func withJobLock(ctx context.Context, dbc *db.Client, id uuid.UUID, entry *logrus.Entry, fn func() error) error {
	conn, err := dbc.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var claimed bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(`+jobLockKeySQL("$1")+`)`, id.String()).Scan(&claimed); err != nil {
		return err
	}
	if !claimed {
		return errJobBusy
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(`+jobLockKeySQL("$1")+`)`, id.String()).Scan(&unlocked); err != nil || !unlocked {
			entry.WithError(err).Warn("Failed to release job lock; discarding database session")
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return fn()
}

// settleDelivery maps a run's error onto the queue outcome. finalize records
// the failure when JetStream will not redeliver; it reports whether a pending
// row was updated (false = another run or the sweep/relay finalized it).
func settleDelivery(ctx context.Context, entry *logrus.Entry, msg *queue.Message, err error, finalize func(context.Context) (bool, error)) error {
	if err == nil {
		return nil
	}
	final := msg.NumDelivered >= maxDeliver
	if ctx.Err() != nil {
		// Worker shutdown, not a failure of the job: never record one. A
		// non-final delivery is redelivered to another replica; on the final
		// one the row stays pending and the stale-pending backstop (sweep or
		// relay) fails it with its timeout message instead of "repeated
		// errors".
		entry.WithError(err).Info("Worker shutting down mid-job; leaving it to redelivery or the stale-pending backstop")
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}
	if errors.Is(err, errJobBusy) {
		if final {
			// The lock holder (another delivery, or the stale sweep) finalizes
			// the row; the stale-pending backstop covers a holder that dies.
			entry.Info("Final delivery found the job locked by another run; dropping")
			return nil
		}
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}
	if !final {
		entry.WithError(err).Warn("Job hit a transient error; retrying")
		return queue.RetryAfter(err, deliveryRetryDelay(msg.NumDelivered, err))
	}

	// JetStream will not redeliver after this attempt. Record the failure so
	// the UI stops showing the job as running. ctx is live here (shutdown
	// returned above).
	markCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	entry.WithError(err).Error("Job failed on its final delivery; marking failed")
	updated, merr := finalize(markCtx)
	if merr != nil {
		// Dropped with the row still pending: the stale-pending backstop fails it.
		entry.WithError(merr).Error("Failed to persist final-delivery failure; leaving it to the stale-pending backstop")
		return merr
	}
	if !updated {
		logLostRace(entry, "failed")
	}
	return nil
}

// retryDelay spaces transient retries: a quick second attempt, then a longer
// pause so a database blip has time to clear before the last delivery.
func retryDelay(numDelivered uint64) time.Duration {
	if numDelivered <= 1 {
		return 10 * time.Second
	}
	return 30 * time.Second
}

// unavailableRetryMax caps the wait before retrying a job the local AI could
// not take (ai.ErrUnavailable: queue full, model loading, pod restarting).
// The relay's triageFailAfter guard and the RCA sweep's staleAfter guard
// (unavailableRetryWindow) each count (maxDeliver-1) of these.
const unavailableRetryMax = 2 * time.Minute

// unavailableRetryDelay spaces retries of ai.ErrUnavailable longer than
// retryDelay, since a model load outlasts a database blip: 1 minute, then 2,
// or the service's Retry-After when longer, never past unavailableRetryMax.
func unavailableRetryDelay(numDelivered uint64, hint time.Duration) time.Duration {
	d := time.Minute
	if numDelivered > 1 {
		d = 2 * time.Minute
	}
	return min(max(d, hint), unavailableRetryMax)
}

// deliveryRetryDelay is the NAK delay for a run that failed with err.
func deliveryRetryDelay(numDelivered uint64, err error) time.Duration {
	if errors.Is(err, ai.ErrUnavailable) {
		return unavailableRetryDelay(numDelivered, ai.RetryAfterHint(err))
	}
	return retryDelay(numDelivered)
}

// startHeartbeat calls msg.InProgress every interval until ctx ends or the
// returned stop function is called. stop waits for the ticker goroutine to
// exit, so no InProgress can race the Ack/Nak that Consume sends after the
// handler.
func startHeartbeat(ctx context.Context, msg *queue.Message, interval time.Duration, entry *logrus.Entry) (stop func()) {
	if msg.InProgress == nil || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := msg.InProgress(); err != nil {
					entry.WithError(err).Debug("Failed to extend AI job ack deadline")
				}
			}
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// logLostRace records a guarded write that matched no pending row: another
// delivery or a stale-pending backstop finalized it first. Not an error — the
// row already holds a terminal status.
func logLostRace(entry *logrus.Entry, wanted string) {
	entry.WithField("wanted_status", wanted).Info("Job was already finalized (another run or the stale-pending backstop); discarding this result")
}
