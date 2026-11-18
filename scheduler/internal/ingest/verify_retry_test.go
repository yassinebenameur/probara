package ingest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
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
