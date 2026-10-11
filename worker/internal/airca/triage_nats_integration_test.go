package airca

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

func triageNATSConfig() *config.WorkerConfig {
	return &config.WorkerConfig{
		AIRCAStream: "AI_RCA_TEST", AIRCASubject: "ai.rca.test", AIRCAConsumerName: "ai-rca-test",
		AITriageStream: "AI_TRIAGE_TEST", AITriageSubject: "ai.triage.test", AITriageConsumerName: "ai-triage-test",
		AITriageEnabled: true, LocalAI: ai.Config{Timeout: time.Second},
	}
}

// The stream the relay publishes into keeps a Duplicates window long enough to
// absorb every republish of a row (so a job still queued is never copied),
// expires stale jobs after an hour, and keeps that config whichever side
// (relay or consumer) ensured it last.
func TestTriageStreamDuplicatesWindow(t *testing.T) {
	url := startJetStream(t)
	qc, err := queue.NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(qc.Close)
	cfg := triageNATSConfig()
	ctx := context.Background()
	if err := ensureTriageStream(ctx, qc, cfg); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, cfg.AITriageStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.Duplicates != triageDuplicatesWindow || info.Config.Duplicates <= (maxTriageAttempts-1)*triageRepublishAfter {
		t.Fatalf("Duplicates window %s, want %s (longer than every republish of a row)", info.Config.Duplicates, triageDuplicatesWindow)
	}
	if info.Config.MaxAge != time.Hour {
		t.Fatalf("MaxAge %s, want 1h", info.Config.MaxAge)
	}
	if info.Config.Retention != jetstream.WorkQueuePolicy || len(info.Config.Subjects) != 1 || info.Config.Subjects[0] != cfg.AITriageSubject {
		t.Fatalf("stream config %+v", info.Config)
	}

	// The consumer side ensures the same stream through triageSpec and must
	// not reset the window.
	if _, err := ensureStream(ctx, qc, triageSpec(cfg)); err != nil {
		t.Fatal(err)
	}
	if info, err = stream.Info(ctx); err != nil || info.Config.Duplicates != triageDuplicatesWindow {
		t.Fatalf("consumer-side ensure left Duplicates %s (%v)", info.Config.Duplicates, err)
	}

	// Every publish of one triage id — the claim and both republishes —
	// stores one message while it is queued.
	r := &Relay{cfg: cfg, log: logger.New("test", "error"), queue: qc, pub: qc, interval: RelayInterval}
	ref := triageRef{}
	ref.id[0], ref.alert[0], ref.tenant[0] = 1, 2, 3
	for i := 0; i < maxTriageAttempts; i++ {
		if err := r.publish(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	if info, err = stream.Info(ctx); err != nil || info.State.Msgs != 1 {
		t.Fatalf("%d publishes of one triage stored %d messages (%v)", maxTriageAttempts, info.State.Msgs, err)
	}
}

// Deleting the triage durable behind a running consumer must not leave the
// queue unserved (same recovery as the AI_RCA consumer).
func TestTriageConsumerRecoversFromDeletedDurable(t *testing.T) {
	url := startJetStream(t)
	qc, err := queue.NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(qc.Close)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	cfg := triageNATSConfig()
	// No database: unsupported-version jobs are acked before touching it.
	c := NewTriageConsumer(cfg, logger.New("test", "error"), nil, qc, nil)
	c.consumerCheckEvery = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunWithRestart(ctx, logger.New("test", "error"), "ai-triage-test", RestartPolicy{InitialBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond}, c.Start)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("triage consumer did not stop on cancel")
		}
	})

	consumed := func(label string) {
		t.Helper()
		if err := qc.Publish(ctx, cfg.AITriageSubject, []byte(`{"v":999}`), nil); err != nil {
			t.Fatalf("%s: publish: %v", label, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			stream, err := js.Stream(ctx, cfg.AITriageStream)
			if err == nil {
				info, err := stream.Info(ctx)
				if err == nil && info.State.Msgs == 0 {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: job was never consumed", label)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := js.Consumer(ctx, cfg.AITriageStream, cfg.AITriageConsumerName); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable never created")
		}
		time.Sleep(20 * time.Millisecond)
	}
	consumed("before delete")
	if err := js.DeleteConsumer(ctx, cfg.AITriageStream, cfg.AITriageConsumerName); err != nil {
		t.Fatal(err)
	}
	consumed("after delete")
	if _, err := js.Consumer(ctx, cfg.AITriageStream, cfg.AITriageConsumerName); err != nil {
		t.Fatalf("durable not re-created: %v", err)
	}
}
