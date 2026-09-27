package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
)

func testConsumer() *Consumer {
	return &Consumer{logger: logger.New("test", "error")}
}

func TestConsumerAckDeadlineExceedsEntireDispatch(t *testing.T) {
	opts := consumerOptions()
	if opts.AckWait <= DispatchTimeout || len(opts.BackOff) != 0 {
		t.Fatalf("consumer can redeliver during Send: %+v", opts)
	}
	if opts.MaxDeliver != len(DefaultBackOff)+1 {
		t.Fatalf("retry schedule does not match attempt limit: %+v", opts)
	}
}

func TestDeliveryAttempt_MissingHeader_DefaultsToOne(t *testing.T) {
	if got := deliveryAttempt(&queue.Message{}); got != 1 {
		t.Errorf("want 1, got %d", got)
	}
}

func TestDeliveryAttempt_UsesMetadata(t *testing.T) {
	msg := &queue.Message{NumDelivered: 4, Headers: map[string][]string{"Nats-Num-Delivered": {"99"}}}
	if got := deliveryAttempt(msg); got != 4 {
		t.Errorf("want 4, got %d", got)
	}
}

func TestDeliveryAttempt_MalformedHeader_DefaultsToOne(t *testing.T) {
	msg := &queue.Message{Headers: map[string][]string{"Nats-Num-Delivered": {"not-a-number"}}}
	if got := deliveryAttempt(msg); got != 1 {
		t.Errorf("want 1, got %d", got)
	}
}

func TestHandle_DropsUnknownVersion(t *testing.T) {
	c := testConsumer()
	env := notifications.DispatchEnvelope{V: 999, ChannelID: "c", ChannelType: "x", AlertID: "a", EventType: "created"}
	raw, _ := json.Marshal(env)

	err := c.handle(context.Background(), &queue.Message{Data: raw})
	if err != nil {
		t.Fatalf("unknown version must be dropped (return nil), got %v", err)
	}
}

func TestHandle_DropsInvalidJSON(t *testing.T) {
	c := testConsumer()
	err := c.handle(context.Background(), &queue.Message{Data: []byte("not json")})
	if err != nil {
		t.Fatalf("invalid JSON must be dropped (return nil), got %v", err)
	}
}

func TestHandle_RetriesOnUnknownPlugin(t *testing.T) {
	c := testConsumer()
	env := notifications.DispatchEnvelope{
		V:           1,
		ChannelID:   "c",
		ChannelType: "definitely-not-registered",
		AlertID:     "a",
		EventType:   "created",
	}
	raw, _ := json.Marshal(env)

	err := c.handle(context.Background(), &queue.Message{Data: raw})
	if err == nil {
		t.Fatal("unknown plugin should return an error so JetStream retries; got nil")
	}
}

func TestRetryDelay_HonoursProviderRetryAfter(t *testing.T) {
	transient := errors.New("503")
	if got := retryDelay(transient, 1); got != DefaultBackOff[0] {
		t.Errorf("first retry = %v, want %v", got, DefaultBackOff[0])
	}
	if got := retryDelay(transient, 99); got != DefaultBackOff[len(DefaultBackOff)-1] {
		t.Errorf("late retry = %v, want the last backoff step", got)
	}
	if got := retryDelay(plugin.RetryAfter(transient, 42*time.Second), 1); got != 42*time.Second {
		t.Errorf("Retry-After = %v, want 42s", got)
	}
}
