package queue

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Publisher defines message publishing operations
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, headers map[string][]string) error
	PublishJSON(ctx context.Context, subject string, v interface{}, headers map[string][]string) error
}

// Consumer defines message consuming operations
type Consumer interface {
	Consume(ctx context.Context, consumer jetstream.Consumer, handler func(*Message) error) error
}

// StreamManager defines stream and consumer management operations
type StreamManager interface {
	EnsureStream(ctx context.Context, streamName string, subjects []string) (jetstream.Stream, error)
	EnsureWorkQueueStream(ctx context.Context, streamName string, subjects []string, maxAge time.Duration) (jetstream.Stream, error)
	CreateConsumer(ctx context.Context, streamName string, consumerName string) (jetstream.Consumer, error)
}

// Queue combines all queue operations into a single interface
type Queue interface {
	Publisher
	Consumer
	StreamManager
	HealthCheck(ctx context.Context) error
	Close()
}

// Ensure Client implements Queue interface
var _ Queue = (*Client)(nil)
