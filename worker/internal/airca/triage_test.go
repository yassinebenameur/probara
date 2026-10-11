package airca

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// The relay's timing invariants (also guarded at compile time in relay.go).
func TestTriageRelayTimingInvariants(t *testing.T) {
	// A republish must not fall inside the stream's Duplicates window (2m
	// server default), or JetStream drops it as a duplicate Nats-Msg-Id.
	if triageRepublishAfter <= 2*time.Minute {
		t.Fatalf("triageRepublishAfter %s must exceed the 2m Duplicates window", triageRepublishAfter)
	}
	if RelayInterval >= triageRepublishAfter {
		t.Fatalf("RelayInterval %s must be shorter than triageRepublishAfter %s", RelayInterval, triageRepublishAfter)
	}
	// A row republished for the last time must still have room for a full
	// delivery chain before the relay fails it.
	chain := time.Duration(maxTriageAttempts-1)*triageRepublishAfter + time.Duration(maxDeliver)*(maxLocalAITimeout+ackWaitHeadroom)
	for n := uint64(1); n < maxDeliver; n++ {
		chain += retryDelay(n)
	}
	if triageFailAfter <= chain {
		t.Fatalf("triageFailAfter %s must exceed the longest pending life (%s)", triageFailAfter, chain)
	}
	if triageLookback <= triageFailAfter {
		t.Fatalf("triageLookback %s should exceed triageFailAfter %s", triageLookback, triageFailAfter)
	}
}

func TestTriageSpecUsesTriageStream(t *testing.T) {
	cfg := &config.WorkerConfig{AIRCAStream: "AI_RCA", AIRCASubject: "ai.rca.jobs", AIRCAConsumerName: "ai-rca-workers",
		AITriageStream: "AI_TRIAGE", AITriageSubject: "ai.triage.jobs", AITriageConsumerName: "ai-triage-workers"}
	s := triageSpec(cfg)
	if s.Stream != "AI_TRIAGE" || s.Subject != "ai.triage.jobs" || s.Consumer != "ai-triage-workers" || s.MaxAge != time.Hour {
		t.Fatalf("triage spec %+v", s)
	}
	if r := rcaSpec(cfg); r.Stream != "AI_RCA" || r.MaxAge != 24*time.Hour {
		t.Fatalf("rca spec %+v", r)
	}
}

// Undecodable and unknown-version jobs are acked and dropped without touching
// the database (nil db would panic).
func TestTriageHandleDropsBadJobs(t *testing.T) {
	c := NewTriageConsumer(&config.WorkerConfig{}, logger.New("test", "error"), nil, nil, nil)
	for _, data := range [][]byte{[]byte(`not json`), []byte(`{"v":999}`), []byte(`{"v":0}`)} {
		if err := c.handle(context.Background(), &queue.Message{Data: data, NumDelivered: 1}); err != nil {
			t.Fatalf("%s: want ack, got %v", data, err)
		}
	}
	b, err := json.Marshal(TriageJob{V: TriageJobVersion, TriageID: uuid.New(), AlertID: uuid.New(), TenantID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"v", "triage_id", "alert_id", "tenant_id"} {
		if _, ok := round[k]; !ok {
			t.Fatalf("TriageJob JSON lacks %q: %s", k, b)
		}
	}
}

// settleDelivery through the triage consumer's finalize: shutdown and busy
// never record a failure (finalize would fail the test), transient errors
// retry until the final delivery, which finalizes.
func TestTriageSettleDeliveryPaths(t *testing.T) {
	entry := logger.New("test", "error").WithField("t", 1)
	finalizeCalls := 0
	finalize := func(context.Context) (bool, error) { finalizeCalls++; return true, nil }

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, delivery := range []uint64{1, maxDeliver} {
		err := settleDelivery(cancelled, entry, &queue.Message{NumDelivered: delivery}, context.Canceled, finalize)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown delivery %d must NAK, got %v", delivery, err)
		}
	}
	ctx := context.Background()
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: 1}, errJobBusy, finalize); !errors.Is(err, errJobBusy) {
		t.Fatalf("busy non-final must retry, got %v", err)
	}
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: maxDeliver}, errJobBusy, finalize); err != nil {
		t.Fatalf("busy final must ack, got %v", err)
	}
	transient := errors.New("db down")
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: 1}, transient, finalize); !errors.Is(err, transient) {
		t.Fatalf("transient non-final must retry, got %v", err)
	}
	if finalizeCalls != 0 {
		t.Fatalf("finalize ran %d times on non-final paths", finalizeCalls)
	}
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: maxDeliver}, transient, finalize); err != nil || finalizeCalls != 1 {
		t.Fatalf("final delivery: err=%v finalize=%d", err, finalizeCalls)
	}
	// A lost race on finalize is not an error.
	lost := func(context.Context) (bool, error) { return false, nil }
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: maxDeliver}, transient, lost); err != nil {
		t.Fatalf("lost race on finalize must ack, got %v", err)
	}
	// A failed finalize leaves the row to the relay's timeout and NAKs.
	broken := errors.New("write failed")
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: maxDeliver}, transient, func(context.Context) (bool, error) { return false, broken }); !errors.Is(err, broken) {
		t.Fatalf("failed finalize must surface, got %v", err)
	}
	if err := settleDelivery(ctx, entry, &queue.Message{NumDelivered: 1}, nil, finalize); err != nil {
		t.Fatal(err)
	}
}

// Operator strings are fixed and composed by the UI: no trailing period.
func TestTriageMessagesHaveNoTrailingPeriod(t *testing.T) {
	for _, m := range []string{triageUnavailableMessage, triageFinalDeliveryMessage, triageTimeoutMessage} {
		if m == "" || m[len(m)-1] == '.' {
			t.Fatalf("message %q", m)
		}
	}
	var meta ai.Metadata
	if err := json.Unmarshal([]byte(failedAnalysisMetadata), &meta); err != nil || meta.Outcome != "failed" {
		t.Fatalf("failed metadata %s: %v", failedAnalysisMetadata, err)
	}
}
