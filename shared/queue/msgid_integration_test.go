//go:build !unit

package queue

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

func TestMsgIDHeaders(t *testing.T) {
	h := MsgIDHeaders("abc")
	if len(h) != 1 || len(h["Nats-Msg-Id"]) != 1 || h["Nats-Msg-Id"][0] != "abc" {
		t.Fatalf("MsgIDHeaders = %#v", h)
	}
	if MsgIDHeader != "Nats-Msg-Id" {
		t.Fatalf("MsgIDHeader = %q", MsgIDHeader)
	}
}

// Two publishes carrying the same message id inside the stream's Duplicates
// window store one message; a different id stores another.
func TestPublishWithMsgIDDeduplicates(t *testing.T) {
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)

	c, err := NewClient(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	stream, err := c.EnsureWorkQueueStream(ctx, "DEDUP_TEST", []string{"dedup.test"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "one", "two"} {
		if err := c.Publish(ctx, "dedup.test", []byte(`{}`), MsgIDHeaders(id)); err != nil {
			t.Fatal(err)
		}
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 2 {
		t.Fatalf("stored %d messages, want 2 (one per distinct id)", info.State.Msgs)
	}
}

// A configured Duplicates window is applied, and an id stays de-duplicated
// after its message was consumed and removed from the work queue: the window
// tracks ids, not stored messages. (The AI triage relay relies on both.)
func TestWorkQueueDuplicatesWindowOutlivesConsumption(t *testing.T) {
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)

	c, err := NewClient(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	stream, err := c.EnsureWorkQueueStreamWithOptions(ctx, "DEDUP_WINDOW", []string{"dedup.window"},
		WorkQueueStreamOptions{MaxAge: time.Hour, Duplicates: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.Duplicates != 30*time.Minute || info.Config.MaxAge != time.Hour {
		t.Fatalf("stream config duplicates=%s maxAge=%s", info.Config.Duplicates, info.Config.MaxAge)
	}
	if err := c.Publish(ctx, "dedup.window", []byte(`{}`), MsgIDHeaders("job-1")); err != nil {
		t.Fatal(err)
	}
	consumer, err := c.CreateConsumer(ctx, "DEDUP_WINDOW", "dedup-window-workers")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := consumer.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err := msg.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	if info, err = stream.Info(ctx); err != nil || info.State.Msgs != 0 {
		t.Fatalf("after ack: %d messages (%v)", info.State.Msgs, err)
	}
	if err := c.Publish(ctx, "dedup.window", []byte(`{}`), MsgIDHeaders("job-1")); err != nil {
		t.Fatal(err)
	}
	if info, err = stream.Info(ctx); err != nil || info.State.Msgs != 0 {
		t.Fatalf("republish of a consumed id stored %d messages (%v)", info.State.Msgs, err)
	}
}
