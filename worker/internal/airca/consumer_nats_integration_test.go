package airca

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

func startJetStream(t *testing.T) string {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

// Deleting the durable behind a running consumer (nats consumer rm, a broker
// that lost its store) must not leave the queue unserved: the supervised
// Start re-creates it although queue.Client still caches the old handle.
func TestConsumerRecoversFromDeletedDurable(t *testing.T) {
	url := startJetStream(t)
	qc, err := queue.NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(qc.Close)
	// An independent connection deletes the durable behind the client's back,
	// so queue.Client's cache is never told.
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkerConfig{AIRCAStream: "AI_RCA_TEST", AIRCASubject: "ai.rca.test", AIRCAConsumerName: "ai-rca-test", LocalAI: ai.Config{Timeout: time.Second}}
	// No database: the jobs below carry an unsupported version, which handle
	// acks and drops before touching it — enough to prove delivery.
	c := New(cfg, logger.New("test", "error"), nil, qc, nil)
	c.consumerCheckEvery = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunWithRestart(ctx, logger.New("test", "error"), "ai-rca-test", RestartPolicy{InitialBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond}, c.Start)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("consumer did not stop on cancel")
		}
	})

	consumed := func(label string) {
		t.Helper()
		if err := qc.Publish(ctx, cfg.AIRCASubject, []byte(`{"v":999}`), nil); err != nil {
			t.Fatalf("%s: publish: %v", label, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			stream, err := js.Stream(ctx, cfg.AIRCAStream)
			if err == nil {
				info, err := stream.Info(ctx)
				if err == nil && info.State.Msgs == 0 {
					return // work-queue stream: acked messages are removed
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: job was never consumed", label)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Wait for the first Start to create stream + durable, then prove delivery.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := js.Consumer(ctx, cfg.AIRCAStream, cfg.AIRCAConsumerName); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable never created")
		}
		time.Sleep(20 * time.Millisecond)
	}
	consumed("before delete")

	if err := js.DeleteConsumer(ctx, cfg.AIRCAStream, cfg.AIRCAConsumerName); err != nil {
		t.Fatal(err)
	}
	consumed("after delete")
	if _, err := js.Consumer(ctx, cfg.AIRCAStream, cfg.AIRCAConsumerName); err != nil {
		t.Fatalf("durable not re-created: %v", err)
	}
}
