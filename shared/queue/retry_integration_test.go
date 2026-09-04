package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
)

func TestConsumeDelayedRetryUsesMetadata(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	container, url := startNatsContainer(ctx, t)
	defer container.Terminate(context.Background())
	client, err := NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.EnsureWorkQueueStream(ctx, "RETRIES", []string{"retry.test"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	consumer, err := client.CreateConsumerWithOptions(ctx, "RETRIES", "retry-test", ConsumerOptions{AckWait: time.Second, MaxDeliver: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, "retry.test", []byte("test"), map[string][]string{"Nats-Num-Delivered": {"99"}}); err != nil {
		t.Fatal(err)
	}
	type delivery struct {
		at    time.Time
		count uint64
	}
	delivered := make(chan delivery, 3)
	done := make(chan error, 1)
	go func() {
		done <- client.Consume(ctx, consumer, func(msg *Message) error {
			delivered <- delivery{time.Now(), msg.NumDelivered}
			if msg.NumDelivered == 1 {
				return RetryAfter(errors.New("transient"), 250*time.Millisecond)
			}
			return nil
		})
	}()
	read := func() delivery {
		t.Helper()
		select {
		case d := <-delivered:
			return d
		case err := <-done:
			t.Fatalf("consumer ended: %v", err)
		case <-ctx.Done():
			t.Fatal("timed out awaiting delivery")
		}
		return delivery{}
	}
	first, second := read(), read()
	if first.count != 1 || second.count != 2 {
		t.Fatalf("delivery metadata = %d, %d", first.count, second.count)
	}
	if elapsed := second.at.Sub(first.at); elapsed < 220*time.Millisecond {
		t.Fatalf("retry bypassed delay: %s", elapsed)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("consumer did not stop")
	}
}

func TestConsumeCancellationUnblocksIdleIterator(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	container, url := startNatsContainer(ctx, t)
	defer container.Terminate(context.Background())
	client, err := NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.EnsureWorkQueueStream(ctx, "IDLE", []string{"idle.test"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	consumer, err := client.CreateConsumerWithOptions(ctx, "IDLE", "idle-test", ConsumerOptions{AckWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- client.Consume(ctx, consumer, func(*Message) error { return nil })
	}()
	// Let Next enter its idle wait before cancelling the consumer.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("consume returned %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle consumer did not stop")
	}
}
