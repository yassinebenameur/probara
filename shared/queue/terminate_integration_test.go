//go:build !unit

package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// A delayed NAK keeps a message in the consumer's ack-pending set, so at
// MaxAckPending a message that keeps failing blocks every message behind it.
// Terminating it after its budget releases the window: the healthy message
// is delivered and the failing one never comes back.
func TestTerminateReleasesTheAckPendingWindow(t *testing.T) {
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	client, err := NewClient(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := client.EnsureWorkQueueStream(ctx, "TERM_TEST", []string{"term.test"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	consumer, err := client.CreateConsumerWithOptions(ctx, "TERM_TEST", "term-test",
		ConsumerOptions{AckWait: 5 * time.Second, MaxDeliver: -1, MaxAckPending: 1})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := consumer.Info(ctx); err != nil || info.Config.MaxAckPending != 1 {
		t.Fatalf("MaxAckPending not applied: %+v %v", info, err)
	}
	for _, body := range []string{"poison", "healthy"} {
		if err := client.Publish(ctx, "term.test", []byte(body), nil); err != nil {
			t.Fatal(err)
		}
	}

	const budget = 3
	poisonDeliveries := 0
	healthy := make(chan time.Time, 1)
	done := make(chan error, 1)
	go func() {
		done <- client.Consume(ctx, consumer, func(msg *Message) error {
			if string(msg.Data) == "healthy" {
				healthy <- msg.Timestamp
				return nil
			}
			poisonDeliveries++
			if msg.NumDelivered >= budget {
				return Terminate(errors.New("poison: giving up"))
			}
			return RetryAfter(errors.New("poison: transient"), 50*time.Millisecond)
		})
	}()

	select {
	case ts := <-healthy:
		if ts.IsZero() {
			t.Error("message timestamp not populated")
		}
	case err := <-done:
		t.Fatalf("consumer ended: %v", err)
	case <-ctx.Done():
		t.Fatalf("healthy message never delivered (poison delivered %d times)", poisonDeliveries)
	}
	// Give a wrongly redelivered poison message time to show up.
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	if poisonDeliveries != budget {
		t.Fatalf("poison delivered %d times, want exactly %d", poisonDeliveries, budget)
	}
	info, err := consumer.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending != 0 || info.NumRedelivered != 0 {
		t.Fatalf("after TERM: ack pending %d, redelivered %d, want 0", info.NumAckPending, info.NumRedelivered)
	}
}
