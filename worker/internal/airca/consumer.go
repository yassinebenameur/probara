package airca

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// Max delivery covers transient persistence failures; service failures are
// recorded once with an operator retry action.
const maxDeliver = 3

// ackWaitHeadroom is added to the local AI timeout to form the consumer's
// AckWait: evidence loading and persistence run outside the model deadline.
const ackWaitHeadroom = 30 * time.Second

// consumerCheckInterval is how often a running Start confirms its durable
// still exists on the server (see watchConsumer).
const consumerCheckInterval = 30 * time.Second

// heartbeatInterval keeps JetStream from redelivering a job mid-analysis. It
// must stay well under the smallest AckWait (1s minimum timeout + headroom).
const heartbeatInterval = 15 * time.Second

// Operator-facing failure messages. Like every markFailed message they never
// carry error text, which may hold infrastructure detail.
const (
	// The web panel renders "Analysis failed: <message>. Try again.", so
	// failure messages carry no trailing period or retry instruction.
	finalDeliveryFailureMessage = "the analysis failed after repeated errors"
)

// errAnalysisBusy reports that another delivery of the same analysis holds its
// advisory lock. It is the one handler error that is not a failure of this run.
var errAnalysisBusy = errors.New("analysis already being processed")

// Consumer runs AI root cause analyses pulled from the AI_RCA stream. The
// workspace enablement is resolved per job; the installation-owned analyzer
// handles every enabled workspace.
type Consumer struct {
	cfg         *config.WorkerConfig
	logger      *logger.Logger
	db          *db.Client
	queue       *queue.Client
	envAnalyzer ai.RootCauseAnalyzer // may be nil when no env local AI configured

	heartbeatEvery     time.Duration // heartbeatInterval; shortened by tests
	deliveryHeadroom   time.Duration // ackWaitHeadroom; shortened by tests
	consumerCheckEvery time.Duration // consumerCheckInterval; shortened by tests
}

// New constructs the consumer. A nil installation analyzer produces failed
// analyses with a retry action while check execution continues.
func New(cfg *config.WorkerConfig, log *logger.Logger, dbClient *db.Client, queueClient *queue.Client, envAnalyzer ai.RootCauseAnalyzer) *Consumer {
	return &Consumer{cfg: cfg, logger: log, db: dbClient, queue: queueClient, envAnalyzer: envAnalyzer,
		heartbeatEvery: heartbeatInterval, deliveryHeadroom: ackWaitHeadroom, consumerCheckEvery: consumerCheckInterval}
}

// Start ensures the AI_RCA stream + consumer exist, then blocks on Consume
// until ctx is cancelled. It returns on any Consume failure; callers run it
// under RunWithRestart.
func (c *Consumer) Start(ctx context.Context) error {
	stream := c.cfg.AIRCAStream
	subject := c.cfg.AIRCASubject
	consumerName := c.cfg.AIRCAConsumerName

	streamHandle, err := c.queue.EnsureWorkQueueStream(ctx, stream, []string{subject}, 24*time.Hour)
	if err != nil {
		return fmt.Errorf("ensure ai rca stream: %w", err)
	}

	// AckWait must outlast a single analysis (local AI round-trip) plus headroom so
	// JetStream doesn't redeliver while we're still calling the model. The
	// handler's InProgress heartbeat also resets it while a job is running.
	ackWait := c.cfg.LocalAI.Timeout + ackWaitHeadroom
	opts := queue.ConsumerOptions{
		AckWait:    ackWait,
		MaxDeliver: maxDeliver,
		BackOff:    []time.Duration{ackWait, 2 * ackWait},
	}
	consumer, err := c.queue.CreateConsumerWithOptions(ctx, stream, consumerName, opts)
	if err != nil {
		return fmt.Errorf("create ai rca consumer: %w", err)
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
		c.logger.WithField("consumer", consumerName).Warn("AI RCA durable missing on the server; re-creating it")
		consumer, err = streamHandle.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
			Name:       consumerName,
			Durable:    consumerName,
			AckPolicy:  jetstream.AckExplicitPolicy,
			AckWait:    opts.AckWait,
			MaxDeliver: opts.MaxDeliver,
			BackOff:    opts.BackOff,
		})
		if err != nil {
			return fmt.Errorf("re-create ai rca consumer: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect ai rca consumer: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"stream":   stream,
		"subject":  subject,
		"consumer": consumerName,
	}).Info("AI root cause consumer started")

	// The run context also ends when watchConsumer finds the durable gone, so
	// RunWithRestart re-runs Start (which re-creates it). Handlers keep the
	// outer ctx: only a real shutdown may cut an analysis short.
	runCtx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	go c.watchConsumer(runCtx, stopRun, consumer)
	err = c.queue.Consume(runCtx, consumer, func(msg *queue.Message) error {
		return c.handle(ctx, msg)
	})
	if ctx.Err() == nil {
		if cause := context.Cause(runCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return cause
		}
	}
	return err
}

// errConsumerMissing ends a Start run whose durable vanished from the server.
var errConsumerMissing = errors.New("ai rca consumer no longer exists on the server")

// watchConsumer polls the durable's info and stops the run once the server
// reports it (or its stream) missing. A broker that restarts with an empty
// store sends no ErrConsumerDeleted, and pulls against a missing consumer are
// retried silently by the iterator, so without this check Consume would block
// forever on a queue nobody serves. Other errors (reconnects, timeouts) are
// left to the next tick.
func (c *Consumer) watchConsumer(ctx context.Context, stop context.CancelCauseFunc, consumer jetstream.Consumer) {
	if c.consumerCheckEvery <= 0 {
		return
	}
	ticker := time.NewTicker(c.consumerCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := consumer.Info(ctx); isConsumerMissing(err) {
				stop(fmt.Errorf("%w: %w", errConsumerMissing, err))
				return
			}
		}
	}
}

func isConsumerMissing(err error) bool {
	return errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound)
}

// handle decodes one job, runs it under an InProgress heartbeat, and turns the
// outcome into an ack (nil), a delayed retry, or — on the final delivery — a
// recorded failure, so no row is left pending once JetStream stops retrying.
func (c *Consumer) handle(ctx context.Context, msg *queue.Message) error {
	var job ai.AnalysisJob
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		c.logger.WithError(err).WithField("subject", msg.Subject).Error("Failed to unmarshal AI RCA job; dropping")
		return nil
	}
	if job.V != ai.AnalysisJobVersion {
		c.logger.WithField("version", job.V).Error("Unsupported AI RCA job version; dropping")
		return nil
	}

	entry := c.logger.WithFields(logrus.Fields{
		"analysis_id": job.AnalysisID,
		"incident_id": job.IncidentID,
		"delivery":    msg.NumDelivered,
	})

	// One deadline for the whole delivery, not just the model call: the
	// advisory lock, evidence queries and persistence all run under it. This
	// is what makes "a delivery holds the row for at most LocalAI.Timeout +
	// ackWaitHeadroom" true, which the sweep's staleAfter window relies on.
	// The heartbeat stops at the same deadline, so even a driver call that
	// outlives cancellation (lib/pq waits for the server to confirm a cancel
	// request) cannot keep the message in progress: JetStream redelivers it
	// after AckWait.
	procCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout+c.deliveryHeadroom)
	stopHeartbeat := startHeartbeat(procCtx, msg, c.heartbeatEvery, entry)
	err := c.process(procCtx, entry, job)
	stopHeartbeat()
	cancel()
	return c.settle(ctx, entry, msg, job, err)
}

// settle maps a process error onto the queue outcome.
func (c *Consumer) settle(ctx context.Context, entry *logrus.Entry, msg *queue.Message, job ai.AnalysisJob, err error) error {
	if err == nil {
		return nil
	}
	final := msg.NumDelivered >= maxDeliver
	if ctx.Err() != nil {
		// Worker shutdown, not a failure of the analysis: never record one.
		// A non-final delivery is redelivered to another replica; on the
		// final one the row stays pending and the stale-pending sweep fails
		// it with its timeout message instead of "repeated errors".
		entry.WithError(err).Info("Worker shutting down mid-analysis; leaving it to redelivery or the stale-pending sweep")
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}
	if errors.Is(err, errAnalysisBusy) {
		if final {
			// The lock holder (another delivery, or the stale sweep) finalizes
			// the row; the sweep is the backstop if that holder dies.
			entry.Info("Final delivery found analysis locked by another run; dropping")
			return nil
		}
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}
	if !final {
		entry.WithError(err).Warn("Analysis hit a transient error; retrying")
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}

	// JetStream will not redeliver after this attempt. Record the failure so
	// the panel stops polling. ctx is live here (shutdown returned above).
	markCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	entry.WithError(err).Error("Analysis failed on its final delivery; marking failed")
	updated, merr := c.markFailed(markCtx, job, finalDeliveryFailureMessage)
	if merr != nil {
		// Dropped with the row still pending: the stale-pending sweep fails it.
		entry.WithError(merr).Error("Failed to persist final-delivery failure; leaving it to the stale-pending sweep")
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
					entry.WithError(err).Debug("Failed to extend AI RCA ack deadline")
				}
			}
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// process runs one analysis. Any returned error is transient (retry) except
// errAnalysisBusy; terminal outcomes are recorded on the row and return nil.
func (c *Consumer) process(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob) error {
	// Bind queued IDs to one tenant/incident before gathering any evidence.
	// Serialize duplicate deliveries with an advisory lock on one pinned connection.
	// The stale-pending sweep takes the same lock key (analysisLockKeySQL), so
	// it can never fail a row while a run holds it.
	conn, err := c.db.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var claimed bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(`+analysisLockKeySQL("$1")+`)`, job.AnalysisID.String()).Scan(&claimed); err != nil {
		return err
	}
	if !claimed {
		return errAnalysisBusy
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(`+analysisLockKeySQL("$1")+`)`, job.AnalysisID.String()).Scan(&unlocked); err != nil || !unlocked {
			entry.WithError(err).Warn("Failed to release analysis lock; discarding database session")
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	status, requestedAt, err := c.loadStatus(ctx, job)
	if errors.Is(err, sql.ErrNoRows) {
		entry.Warn("Analysis row no longer exists; dropping")
		return nil
	}
	if err != nil {
		entry.WithError(err).Error("Failed to load analysis status")
		return err // infra error — retry
	}
	if status != string(pendingStatus) {
		entry.WithField("status", status).Debug("Analysis already processed; dropping")
		return nil
	}

	// Resolve shared workspace enablement before gathering any evidence.
	analyzer, err := c.resolveAnalyzer(ctx, job.TenantID)
	if err != nil {
		entry.WithError(err).Error("Failed to resolve analyzer config")
		return err // transient database error — retry
	}
	if analyzer == nil {
		entry.Warn("No local AI configured for tenant; marking analysis failed")
		return c.finishFailed(ctx, entry, job, "Local AI is disabled for this workspace or installation")
	}

	input, err := buildAnalysisInput(ctx, c.db, job.TenantID, job.IncidentID, requestedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Incident or evidence vanished — terminal, record and ack.
			entry.WithError(err).Warn("Incident not found while building analysis context")
			return c.finishFailed(ctx, entry, job, "incident not found")
		}
		entry.WithError(err).Error("Failed to build analysis context")
		return err // likely transient DB error — retry
	}

	analysisCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout)
	defer cancel()
	result, err := analyzer.Analyze(analysisCtx, input)
	if err != nil {
		// Analyzer failures (service outage, queue saturation, invalid responses) are terminal
		// for this run; the operator retries from the UI.
		if ctx.Err() != nil {
			// Shutdown or the delivery deadline cut the call short; that says
			// nothing about the local AI, so retry rather than record a verdict.
			return ctx.Err()
		}
		entry.WithError(err).Warn("Analysis failed")
		return c.finishFailed(ctx, entry, job, "Local AI service unavailable or returned invalid decisions; check service readiness")
	}

	updated, err := c.markReady(ctx, job, result)
	if err != nil {
		entry.WithError(err).Error("Failed to persist analysis result")
		return err // retry the persist
	}
	if !updated {
		logLostRace(entry, "ready")
		return nil
	}
	entry.WithField("confidence", result.Confidence).Info("Analysis completed")
	return nil
}

// finishFailed records a terminal failure and acks (returns nil). If the write
// itself fails it returns the error so JetStream retries.
func (c *Consumer) finishFailed(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob, message string) error {
	updated, err := c.markFailed(ctx, job, message)
	if err != nil {
		entry.WithError(err).Error("Failed to persist analysis failure")
		return err
	}
	if !updated {
		logLostRace(entry, "failed")
	}
	return nil
}

// logLostRace records a guarded write that matched no pending row: another
// delivery or the stale-pending sweep finalized it first. Not an error — the
// row already holds a terminal status.
func logLostRace(entry *logrus.Entry, wanted string) {
	entry.WithField("wanted_status", wanted).Info("Analysis was already finalized (another run or the stale-pending sweep); discarding this result")
}
