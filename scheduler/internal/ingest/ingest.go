// Package ingest hosts the platform-side consumer that persists check results
// published by workers over NATS. Workers have no Postgres access (remote
// location workers only ever reach NATS), so this is the single writer of
// check_results for active checks and the place monitor state advances.
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

const (
	checkResultStreamMaxAge = 24 * time.Hour
	consumerRestartBackoff  = 2 * time.Second
)

// statusPublisher is the minimal publishing surface the ingest needs. It is
// satisfied by *statusupdates.Publisher and by test spies.
type statusPublisher interface {
	Publish(event statusupdates.Event) error
}

// Ingest consumes CheckResultMessages from the results stream and persists
// them: insert the check_results row and advance the monitor state machine in
// one transaction (idempotent on job_id+result_source, so at-least-once
// delivery can never double-apply).
type Ingest struct {
	config           *config.SchedulerConfig
	logger           *logger.Logger
	db               *db.Client
	queue            *queue.Client
	status           statusPublisher
	secretsEncryptor secrets.Encryptor
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup

	resultsIngested *prometheus.CounterVec
	ingestErrors    *prometheus.CounterVec
	duplicates      prometheus.Counter
	meshIngested    prometheus.Counter
	abandoned       *prometheus.CounterVec
}

// New creates the ingest component. statusPub may be nil (status page updates
// are then skipped).
func New(cfg *config.SchedulerConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client, queueClient *queue.Client, statusPub *statusupdates.Publisher) *Ingest {
	ctx, cancel := context.WithCancel(context.Background())

	i := &Ingest{
		config:           cfg,
		logger:           log,
		db:               dbClient,
		queue:            queueClient,
		ctx:              ctx,
		cancel:           cancel,
		secretsEncryptor: secrets.NoOpEncryptor{},
	}
	if statusPub != nil {
		i.status = statusPub
	}

	i.resultsIngested = metricsRegistry.NewCounter(
		"results_ingested_total",
		"Total number of check results persisted by the ingest consumer",
		[]string{"status"},
	)
	i.ingestErrors = metricsRegistry.NewCounter(
		"result_ingest_errors_total",
		"Total number of check result ingest failures",
		[]string{},
	)
	duplicatesCounter := metricsRegistry.NewCounter(
		"result_ingest_duplicates_total",
		"Total number of redelivered check results skipped as duplicates",
		[]string{},
	)
	i.duplicates = duplicatesCounter.With(prometheus.Labels{})
	meshCounter := metricsRegistry.NewCounter(
		"mesh_results_ingested_total",
		"Total number of inter-location mesh probe results persisted",
		[]string{},
	)
	i.meshIngested = meshCounter.With(prometheus.Labels{})
	i.abandoned = metricsRegistry.NewCounter(
		"result_ingest_abandoned_total",
		"Check results terminated unprocessed after their retry budget, by reason (platform, credential)",
		[]string{"reason"},
	)

	return i
}

// ConfigureEncryption wires the platform at-rest key used to open private
// location credentials before verifying worker signatures.
func (i *Ingest) ConfigureEncryption(encryptor secrets.Encryptor) {
	if encryptor != nil {
		i.secretsEncryptor = encryptor
	}
}

// errRejected marks a location message refused on its own merits: a subject
// or payload that does not match, an identifier that does not parse, a
// location (or monitor link) that is unknown, disabled or deleted, a bad
// signature. Redelivering it cannot change the answer, so it is acked and
// dropped. Every other verification error is the platform's (a database
// error, a schema the migrations have not reached yet, an undecryptable
// stored credential) and must not cost the result: it is retried, within the
// budgets of retryOrGiveUp.
var errRejected = errors.New("rejected")

func rejectf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errRejected, fmt.Sprintf(format, args...))
}

// isRejected reports whether err is a verdict on the message itself.
func isRejected(err error) bool { return errors.Is(err, errRejected) }

// errUndecryptableCredential marks a stored location credential the
// scheduler's key cannot open. With a key configured that is usually
// permanent for the location (encrypted under a key since removed), so its
// results get a short retry budget; see retryOrGiveUp.
var errUndecryptableCredential = errors.New("decrypt location credential")

// Retry budgets, in deliveries. A delayed NAK keeps the message in the
// consumer's ack-pending set (nats-server: a NAK with a delay only moves the
// pending entry's timestamp), and at MaxAckPending the server delivers no
// new message, for any tenant. So no retry may be unbounded: past its budget
// a message is terminated (TERM), logged and counted.
//
// platformMaxDeliveries covers platformRetryBudget of backoff: database and
// schema errors, which is what a deploy produces while migrations run.
// credentialMaxDeliveries is for an undecryptable credential while a key is
// configured: a few seconds, in case a key rotation is mid-rollout.
const (
	platformRetryBudget     = 15 * time.Minute
	credentialMaxDeliveries = 3
	// resultIngestMaxAckPending must exceed the results that can be waiting
	// out a NAK at once: in-flight work plus failing traffic times its
	// budget. 20000 holds ~1300 failing results a minute for the full
	// platform budget; the server default (1000) would let one broken
	// location stall every tenant's results within minutes.
	resultIngestMaxAckPending = 20000
)

var platformMaxDeliveries = deliveriesWithin(platformRetryBudget)

// deliveriesWithin is the number of deliveries whose NAK delays
// (retryDelay) fit in budget, counting the first delivery.
func deliveriesWithin(budget time.Duration) uint64 {
	var n uint64 = 1
	var elapsed time.Duration
	for {
		d := retryDelay(n)
		if elapsed+d > budget {
			return n
		}
		elapsed += d
		n++
	}
}

// ingestBackoff is the redelivery schedule of a result the ingest could not
// process yet. The consumer is created with it, but JetStream applies a
// consumer's BackOff only to AckWait expiries, never to an explicit NAK, so
// handleMessage NAKs with retryDelay instead of an immediate redelivery that
// would busy-loop while the database is down or behind.
var ingestBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, time.Minute}

// retryDelay is the NAK delay for the numDelivered-th delivery of a message.
func retryDelay(numDelivered uint64) time.Duration {
	if numDelivered < 1 {
		numDelivered = 1
	}
	if numDelivered > uint64(len(ingestBackoff)) {
		return ingestBackoff[len(ingestBackoff)-1]
	}
	return ingestBackoff[numDelivered-1]
}

// locationCredential returns the plaintext credential of an enabled location
// of a live workspace (bound to tenantID and monitorID when given). Errors
// wrapping errRejected mean the message cannot authenticate; anything else
// is a platform failure the caller must retry.
func (i *Ingest) locationCredential(ctx context.Context, locationID, tenantID, monitorID string) (string, error) {
	locationUUID, err := uuid.Parse(locationID)
	if err != nil {
		return "", fmt.Errorf("%w: invalid location_id: %w", errRejected, err)
	}
	var stored string
	query := `
		SELECT l.worker_credential
		FROM locations l
		WHERE l.id = $1
		  AND l.enabled = TRUE AND l.deleted_at IS NULL
		  AND l.worker_credential IS NOT NULL
		  -- A deleted workspace's private locations are cut off: their
		  -- results and heartbeats are refused here, as their NATS
		  -- connections are at the authorization callout.
		  AND ` + tenancy.LiveTenantSQL("l.tenant_id")
	args := []any{locationUUID}
	if tenantID != "" {
		tenantUUID, err := uuid.Parse(tenantID)
		if err != nil {
			return "", fmt.Errorf("%w: invalid tenant_id: %w", errRejected, err)
		}
		query += " AND l.tenant_id = $2"
		args = append(args, tenantUUID)
	}
	if monitorID != "" {
		monitorUUID, err := uuid.Parse(monitorID)
		if err != nil {
			return "", fmt.Errorf("%w: invalid monitor_id: %w", errRejected, err)
		}
		query += ` AND EXISTS (
			SELECT 1 FROM monitor_locations ml
			JOIN monitors m ON m.id = ml.monitor_id
			WHERE ml.location_id = l.id AND ml.monitor_id = $3
			  AND m.tenant_id = l.tenant_id AND m.deleted_at IS NULL
		)`
		args = append(args, monitorUUID)
	}
	if err := i.db.QueryRowContext(ctx, query, args...).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", rejectf("no enabled location %s of a live workspace matches", locationID)
		}
		return "", fmt.Errorf("location credential lookup: %w", err)
	}
	credential, err := i.secretsEncryptor.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errUndecryptableCredential, err)
	}
	return credential, nil
}

func (i *Ingest) verifyLocationResult(ctx context.Context, subject string, m models.CheckResultMessage) error {
	locationPrefix := i.config.CheckResultSubject + ".loc."
	if subject == i.config.CheckResultSubject {
		if m.LocationID != "" {
			return rejectf("location result published on platform subject")
		}
		if m.LocationSignature != "" {
			return rejectf("location signature without location_id")
		}
		return nil
	}
	if !strings.HasPrefix(subject, locationPrefix) {
		return rejectf("unexpected result subject %q", subject)
	}
	subjectLocation := strings.TrimPrefix(subject, locationPrefix)
	if subjectLocation == "" || strings.Contains(subjectLocation, ".") || m.LocationID != subjectLocation {
		return rejectf("result subject location does not match payload")
	}
	monitorID := m.MonitorID
	if m.Mesh != nil {
		monitorID = ""
	}
	credential, err := i.locationCredential(ctx, m.LocationID, m.TenantID, monitorID)
	if err != nil {
		return err
	}
	signature := m.LocationSignature
	m.LocationSignature = ""
	// VerifyJSON is pure: a missing or wrong signature, or a stored
	// credential that cannot sign at all, gives the same answer every time.
	if err := locationauth.VerifyJSON(credential, signature, m); err != nil {
		return fmt.Errorf("%w: %w", errRejected, err)
	}
	return nil
}

// Start ensures the results stream, creates the durable consumer and spawns
// the ingest goroutines. It blocks until the context is cancelled.
func (i *Ingest) Start() error {
	i.logger.WithFields(logrus.Fields{
		"stream":      i.config.CheckResultStream,
		"subject":     i.config.CheckResultSubject,
		"consumer":    i.config.ResultIngestConsumerName,
		"concurrency": i.config.ResultIngestConcurrency,
	}).Info("Starting results ingest consumer")

	ctx, cancel := context.WithTimeout(i.ctx, 10*time.Second)
	defer cancel()

	_, err := i.queue.EnsureWorkQueueStream(ctx, i.config.CheckResultStream,
		[]string{i.config.CheckResultSubject, i.config.CheckResultSubject + ".loc.>"}, checkResultStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure results stream: %w", err)
	}

	// No server-side delivery limit: the handler bounds retries itself
	// (retryOrGiveUp), by reason, so a database outage or a migration in
	// progress costs no result while a message that can never succeed is
	// terminated instead of holding the ack-pending window. The backoff
	// (applied to NAKs by retryDelay) keeps a failing message from
	// busy-looping, and MaxAckPending is sized for the retries in flight.
	consumer, err := i.queue.CreateConsumerWithOptions(ctx, i.config.CheckResultStream, i.config.ResultIngestConsumerName,
		queue.ConsumerOptions{
			MaxDeliver:    -1,
			BackOff:       ingestBackoff,
			MaxAckPending: resultIngestMaxAckPending,
		})
	if err != nil {
		return fmt.Errorf("failed to create ingest consumer: %w", err)
	}

	// Location worker heartbeats → locations.last_seen_at (connected badge).
	heartbeatSub, err := i.startHeartbeatSubscriber()
	if err != nil {
		i.logger.WithError(err).Warn("Failed to subscribe to location heartbeats; location connection status will be stale")
	} else {
		defer func() { _ = heartbeatSub.Unsubscribe() }()
	}

	for n := 0; n < i.config.ResultIngestConcurrency; n++ {
		i.wg.Add(1)
		go func(id int) {
			defer i.wg.Done()
			i.consumeLoop(i.ctx, consumer, id)
		}(n)
	}

	<-i.ctx.Done()
	i.logger.Info("Ingest context cancelled, waiting for goroutines to finish")
	return nil
}

func (i *Ingest) consumeLoop(ctx context.Context, consumer jetstream.Consumer, id int) {
	handler := func(msg *queue.Message) error {
		return i.handleMessage(ctx, msg)
	}

	for {
		err := i.queue.Consume(ctx, consumer, handler)
		if err == nil || err == context.Canceled {
			break
		}

		i.logger.WithError(err).WithField("ingest_goroutine", id).Error("Ingest goroutine error")

		timer := time.NewTimer(consumerRestartBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// handleMessage persists one result message. Returning an error Naks the
// message for redelivery after retryDelay; returning nil Acks it. Poison
// messages (bad JSON, bad UUIDs, unknown monitor, a location result that
// fails authentication) are logged and acked — retrying cannot fix them.
// Platform failures (database errors, a schema behind the code) are never
// acked: they are retried, then terminated past their budget.
func (i *Ingest) handleMessage(ctx context.Context, msg *queue.Message) error {
	var m models.CheckResultMessage
	if err := json.Unmarshal(msg.Data, &m); err != nil {
		i.logger.WithError(err).Error("Failed to unmarshal check result message")
		return nil
	}

	logEntry := i.logger.WithFields(logrus.Fields{
		"job_id":     m.JobID,
		"monitor_id": m.MonitorID,
		"tenant_id":  m.TenantID,
	})

	if m.Version != "v1" {
		logEntry.WithField("version", m.Version).Error("Unsupported check result version")
		return nil
	}
	if err := i.verifyLocationResult(ctx, msg.Subject, m); err != nil {
		if isRejected(err) {
			logEntry.WithError(err).Warn("Rejecting unauthenticated location result")
			return nil
		}
		// The result may well be genuine: the database is unreachable or
		// its schema is behind this code. Acking would lose it for good.
		logEntry.WithError(err).WithField("delivery", msg.NumDelivered).Error("Could not verify location result; retrying")
		i.ingestErrors.With(prometheus.Labels{}).Inc()
		return i.retryOrGiveUp(logEntry, msg, err)
	}

	// Mesh probes are edge results, not monitor results — they carry no
	// monitor_id and never touch check_results or monitor state.
	if m.Mesh != nil {
		if err := i.handleMeshResult(ctx, m); err != nil {
			return i.retryOrGiveUp(logEntry, msg, err)
		}
		return nil
	}

	result, err := i.buildResult(m)
	if err != nil {
		logEntry.WithError(err).Error("Invalid check result message")
		return nil
	}

	outcome, err := i.persist(ctx, m, result)
	if err != nil {
		if isMonitorGone(err) {
			// The monitor was purged while its last jobs were in flight.
			logEntry.Debug("Dropping result for missing monitor")
			return nil
		}
		logEntry.WithError(err).Error("Failed to persist check result")
		i.ingestErrors.With(prometheus.Labels{}).Inc()
		return i.retryOrGiveUp(logEntry, msg, err)
	}

	if outcome.duplicate {
		i.duplicates.Inc()
		return nil
	}

	i.resultsIngested.With(prometheus.Labels{"status": m.Status}).Inc()

	// Only state transitions are published; per-result events flooded the
	// status-page service (and connected browsers) with no visible change.
	if outcome.stateChanged {
		i.publishStatusUpdate(m.MonitorID, m.TenantID, "state_change")
	}
	return nil
}

// retryOrGiveUp answers a message that failed for a reason not its own: a
// delayed NAK while its retry budget lasts, then a TERM, so a message that
// can never succeed stops holding a slot of the ack-pending window.
func (i *Ingest) retryOrGiveUp(logEntry *logrus.Entry, msg *queue.Message, err error) error {
	reason, budget := "platform", platformMaxDeliveries
	if _, noKey := i.secretsEncryptor.(secrets.NoOpEncryptor); errors.Is(err, errUndecryptableCredential) && !noKey {
		reason, budget = "credential", credentialMaxDeliveries
	}
	if msg.NumDelivered < budget {
		return queue.RetryAfter(err, retryDelay(msg.NumDelivered))
	}
	fields := logrus.Fields{"delivery": msg.NumDelivered, "reason": reason}
	if !msg.Timestamp.IsZero() {
		fields["age_seconds"] = int(time.Since(msg.Timestamp).Seconds())
	}
	logEntry.WithError(err).WithFields(fields).Warn("Dropping check result after its retry budget")
	i.abandoned.With(prometheus.Labels{"reason": reason}).Inc()
	return queue.Terminate(err)
}

func (i *Ingest) buildResult(m models.CheckResultMessage) (monitorResult, error) {
	monitorID, err := uuid.Parse(m.MonitorID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid monitor_id: %w", err)
	}
	tenantID, err := uuid.Parse(m.TenantID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid tenant_id: %w", err)
	}
	jobID, err := uuid.Parse(m.JobID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid job_id: %w", err)
	}
	var locationID *uuid.UUID
	if m.LocationID != "" {
		id, err := uuid.Parse(m.LocationID)
		if err != nil {
			return monitorResult{}, fmt.Errorf("invalid location_id: %w", err)
		}
		locationID = &id
	}
	return monitorResult{
		MonitorID:  monitorID,
		TenantID:   tenantID,
		JobID:      jobID,
		LocationID: locationID,
	}, nil
}

// monitorResult carries the parsed identifiers of one result message.
type monitorResult struct {
	MonitorID  uuid.UUID
	TenantID   uuid.UUID
	JobID      uuid.UUID
	LocationID *uuid.UUID
}

func (i *Ingest) publishStatusUpdate(monitorID, tenantID, eventType string) {
	if i.status == nil {
		return
	}
	event := statusupdates.Event{
		Type:      eventType,
		MonitorID: monitorID,
		TenantID:  tenantID,
		Timestamp: time.Now().UTC(),
	}
	if err := i.status.Publish(event); err != nil {
		i.logger.WithError(err).Warn("Failed to publish status page update")
	}
}

// Shutdown gracefully stops the ingest goroutines.
func (i *Ingest) Shutdown(ctx context.Context) error {
	i.cancel()

	done := make(chan struct{})
	go func() {
		i.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
