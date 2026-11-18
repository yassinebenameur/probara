package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

// Subject and payload mismatches are verdicts on the message: acked, never
// retried.
func TestVerifyLocationResultMismatchesAreRejections(t *testing.T) {
	i := &Ingest{config: &config.SchedulerConfig{CheckResultSubject: "results"}}
	for name, tc := range map[string]struct {
		subject string
		m       models.CheckResultMessage
	}{
		"location payload on platform subject": {"results", models.CheckResultMessage{LocationID: "location-a"}},
		"signature without location":           {"results", models.CheckResultMessage{LocationSignature: "sig"}},
		"another location's subject":           {"results.loc.location-a", models.CheckResultMessage{LocationID: "location-b"}},
		"unexpected subject":                   {"unexpected", models.CheckResultMessage{}},
		"unparsable location id":               {"results.loc.not-a-uuid", models.CheckResultMessage{LocationID: "not-a-uuid"}},
	} {
		err := i.verifyLocationResult(context.Background(), tc.subject, tc.m)
		if err == nil || !isRejected(err) {
			t.Errorf("%s: err = %v, want a rejection", name, err)
		}
	}
}

// A database the ingest cannot query says nothing about the message: the
// result is NAKed for redelivery, never acked as unauthenticated.
func TestHandleMessageRetriesWhenVerificationCannotReachTheDatabase(t *testing.T) {
	closed, err := sql.Open("postgres", "postgres://probara@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	cfg := &config.SchedulerConfig{CheckResultSubject: models.CheckResultSubject}
	i := New(cfg, logger.New("ingest-test", "error"), metrics.NewRegistry("ingest_verify_retry_test"), &db.Client{DB: closed}, nil, nil)

	locationID := uuid.New()
	m := models.CheckResultMessage{
		Version:           "v1",
		JobID:             uuid.New().String(),
		MonitorID:         uuid.New().String(),
		TenantID:          uuid.New().String(),
		LocationID:        locationID.String(),
		Status:            "success",
		ResultSource:      string(models.ResultSourceMonitor),
		LocationSignature: "signature",
	}
	msg := testMessage(mustMarshal(t, m))
	msg.Subject = models.CheckResultSubjectForLocation(models.CheckResultSubject, m.LocationID)
	msg.NumDelivered = 1

	if verr := i.verifyLocationResult(context.Background(), msg.Subject, m); verr == nil || isRejected(verr) {
		t.Fatalf("verify with the database gone = %v, want a platform error", verr)
	}
	if err := i.handleMessage(context.Background(), msg); err == nil {
		t.Fatal("handleMessage acked a result it could not verify")
	}

	// Mesh results take the same verification path.
	m.MonitorID = ""
	m.Mesh = &models.MeshResultInfo{TargetLocationID: uuid.New().String()}
	msg = testMessage(mustMarshal(t, m))
	msg.Subject = models.CheckResultSubjectForLocation(models.CheckResultSubject, m.LocationID)
	if err := i.handleMessage(context.Background(), msg); err == nil {
		t.Fatal("handleMessage acked a mesh result it could not verify")
	}
}

func TestRetryDelayFollowsTheConsumerBackoff(t *testing.T) {
	for delivered, want := range map[uint64]time.Duration{
		0:  time.Second,
		1:  time.Second,
		2:  5 * time.Second,
		3:  15 * time.Second,
		4:  time.Minute,
		50: time.Minute,
	} {
		if got := retryDelay(delivered); got != want {
			t.Errorf("retryDelay(%d) = %v, want %v", delivered, got, want)
		}
	}
}

// failingEncryptor stands for a configured platform key that cannot open a
// stored credential.
type failingEncryptor struct{}

func (failingEncryptor) Encrypt(string) (string, error) { return "", errors.New("unused") }
func (failingEncryptor) Decrypt(string) (string, error) {
	return "", errors.New("cipher: message authentication failed")
}

// Retries are bounded: a delayed NAK keeps the message in the consumer's
// ack-pending window, so one that can never succeed must be terminated.
func TestRetryOrGiveUpBoundsEveryRetry(t *testing.T) {
	i := New(&config.SchedulerConfig{CheckResultSubject: models.CheckResultSubject}, logger.New("ingest-test", "error"),
		metrics.NewRegistry("ingest_give_up_test"), nil, nil, nil)
	entry := i.logger.WithField("test", t.Name())
	answer := func(err error, delivered uint64) error {
		return i.retryOrGiveUp(entry, &queue.Message{NumDelivered: delivered, Timestamp: time.Now().Add(-time.Minute)}, err)
	}

	// Database and schema errors: the long budget, ~15 minutes of backoff.
	if platformMaxDeliveries != 18 {
		t.Fatalf("platformMaxDeliveries = %d, want 18 (1s+5s+15s+14x60s fit in 15 min)", platformMaxDeliveries)
	}
	dbErr := errors.New(`pq: column "deleted_at" does not exist`)
	for n := uint64(1); n < platformMaxDeliveries; n++ {
		if err := answer(dbErr, n); err == nil || queue.IsTerminal(err) {
			t.Fatalf("delivery %d of a platform failure: %v, want a NAK", n, err)
		}
	}
	if err := answer(dbErr, platformMaxDeliveries); !queue.IsTerminal(err) {
		t.Fatalf("delivery %d of a platform failure: %v, want TERM", platformMaxDeliveries, err)
	}

	// An undecryptable credential without a configured key is a fixable
	// configuration error: the long budget too.
	credErr := fmt.Errorf("%w: %w", errUndecryptableCredential, errors.New("no key"))
	if err := answer(credErr, credentialMaxDeliveries); queue.IsTerminal(err) {
		t.Fatalf("no key configured: gave up after %d deliveries", credentialMaxDeliveries)
	}
	// With a key configured it is usually permanent: a few deliveries.
	i.ConfigureEncryption(failingEncryptor{})
	if err := answer(credErr, credentialMaxDeliveries-1); queue.IsTerminal(err) {
		t.Fatal("undecryptable credential terminated before its budget")
	}
	if err := answer(credErr, credentialMaxDeliveries); !queue.IsTerminal(err) {
		t.Fatalf("undecryptable credential with a key: %v, want TERM after %d deliveries", err, credentialMaxDeliveries)
	}
	for reason, want := range map[string]float64{"platform": 1, "credential": 1} {
		if got := promtest.ToFloat64(i.abandoned.With(prometheus.Labels{"reason": reason})); got != want {
			t.Errorf("result_ingest_abandoned_total{reason=%q} = %v, want %v", reason, got, want)
		}
	}
}

// End to end through handleMessage: the database gone past the budget ends
// in a TERM, not an endless NAK.
func TestHandleMessageTerminatesAfterThePlatformBudget(t *testing.T) {
	closed, err := sql.Open("postgres", "postgres://probara@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	i := New(&config.SchedulerConfig{CheckResultSubject: models.CheckResultSubject}, logger.New("ingest-test", "error"),
		metrics.NewRegistry("ingest_terminate_test"), &db.Client{DB: closed}, nil, nil)
	m := models.CheckResultMessage{
		Version: "v1", JobID: uuid.New().String(), MonitorID: uuid.New().String(), TenantID: uuid.New().String(),
		LocationID: uuid.New().String(), Status: "success", ResultSource: string(models.ResultSourceMonitor), LocationSignature: "sig",
	}
	msg := testMessage(mustMarshal(t, m))
	msg.Subject = models.CheckResultSubjectForLocation(models.CheckResultSubject, m.LocationID)
	msg.NumDelivered = platformMaxDeliveries
	if err := i.handleMessage(context.Background(), msg); !queue.IsTerminal(err) {
		t.Fatalf("last delivery within the budget: %v, want TERM", err)
	}
}
