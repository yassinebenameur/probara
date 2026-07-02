package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Client wraps a NATS JetStream client
type Client struct {
	nc *nats.Conn
	js jetstream.JetStream

	// mu guards the streams/consumers caches: multiple components sharing one
	// client (e.g. the scheduler loop and the results-ingest consumer) ensure
	// their streams concurrently at startup.
	mu        sync.Mutex
	streams   map[string]jetstream.Stream
	consumers map[string]jetstream.Consumer
}

// Message represents a message from the queue
type Message struct {
	Data       []byte
	Subject    string
	Headers    map[string][]string
	Ack        func() error
	Nak        func() error
	InProgress func() error
}

// reconnectOptions keeps the connection retrying forever. The nats.go
// default gives up after 60 attempts (~2 minutes) and leaves the connection
// permanently CLOSED, which froze every worker/alerter when the NATS pod
// moved nodes. Keep in sync with shared/statusupdates.
func reconnectOptions() []nats.Option {
	return []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("nats: disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("nats: reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			log.Printf("nats: connection permanently closed")
		}),
	}
}

// NewClient creates a new NATS JetStream client. The connection retries
// forever (initial connect and reconnects) so a NATS outage never leaves
// the client permanently disconnected.
func NewClient(natsURL string) (*Client, error) {
	nc, err := nats.Connect(natsURL, reconnectOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	return &Client{
		nc:        nc,
		js:        js,
		streams:   make(map[string]jetstream.Stream),
		consumers: make(map[string]jetstream.Consumer),
	}, nil
}

// EnsureStream ensures a stream exists with the given configuration
func (c *Client) EnsureStream(ctx context.Context, streamName string, subjects []string) (jetstream.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if stream, exists := c.streams[streamName]; exists {
		return stream, nil
	}

	cfg := jetstream.StreamConfig{
		Name:     streamName,
		Subjects: subjects,
		Storage:  jetstream.FileStorage,
		Replicas: 1,
	}

	stream, err := c.js.CreateStream(ctx, cfg)
	if err != nil {
		// Stream might already exist, try to get it
		stream, err = c.js.Stream(ctx, streamName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get stream: %w", err)
		}
	}

	c.streams[streamName] = stream
	return stream, nil
}

// EnsureWorkQueueStream ensures a stream exists with work-queue retention.
func (c *Client) EnsureWorkQueueStream(ctx context.Context, streamName string, subjects []string, maxAge time.Duration) (jetstream.Stream, error) {
	cfg := jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  subjects,
		Retention: jetstream.WorkQueuePolicy,
		Discard:   jetstream.DiscardOld,
		MaxAge:    maxAge,
		Storage:   jetstream.FileStorage,
		Replicas:  1,
	}

	stream, err := c.js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create or update work queue stream: %w", err)
	}

	c.mu.Lock()
	c.streams[streamName] = stream
	c.mu.Unlock()
	return stream, nil
}

// Publish publishes a message to the given subject
func (c *Client) Publish(ctx context.Context, subject string, data []byte, headers map[string][]string) error {
	var msgHeaders nats.Header
	if headers != nil {
		msgHeaders = make(nats.Header)
		for k, v := range headers {
			if len(v) > 0 {
				msgHeaders[k] = v
			}
		}
	}

	_, err := c.js.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  msgHeaders,
	})
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// PublishJSON publishes a JSON-encoded message
func (c *Client) PublishJSON(ctx context.Context, subject string, v interface{}, headers map[string][]string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return c.Publish(ctx, subject, data, headers)
}

// CreateConsumer creates or gets a consumer for the given stream
func (c *Client) CreateConsumer(ctx context.Context, streamName string, consumerName string) (jetstream.Consumer, error) {
	return c.CreateConsumerWithOptions(ctx, streamName, consumerName, ConsumerOptions{})
}

// ConsumerOptions tunes a JetStream consumer. Zero values fall back to the
// defaults previously hardcoded in CreateConsumer (AckWait=30s, MaxDeliver=3,
// no BackOff array, no FilterSubject).
type ConsumerOptions struct {
	AckWait       time.Duration
	MaxDeliver    int
	BackOff       []time.Duration
	FilterSubject string
}

// CreateConsumerWithOptions creates or gets a consumer with custom retry
// policy. Used by the notifications worker to install an exponential backoff
// schedule (10s, 30s, 2m, 10m, 30m) for transient webhook failures.
func (c *Client) CreateConsumerWithOptions(ctx context.Context, streamName, consumerName string, opts ConsumerOptions) (jetstream.Consumer, error) {
	key := fmt.Sprintf("%s:%s", streamName, consumerName)
	c.mu.Lock()
	if consumer, exists := c.consumers[key]; exists {
		c.mu.Unlock()
		return consumer, nil
	}
	c.mu.Unlock()

	stream, err := c.js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("stream not found: %w", err)
	}

	ackWait := opts.AckWait
	if ackWait <= 0 {
		ackWait = 30 * time.Second
	}
	maxDeliver := opts.MaxDeliver
	if maxDeliver <= 0 {
		maxDeliver = 3
	}

	cfg := jetstream.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    maxDeliver,
		BackOff:       opts.BackOff,
		FilterSubject: opts.FilterSubject,
	}

	consumer, err := stream.CreateConsumer(ctx, cfg)
	if err != nil {
		// Consumer might already exist, try to get it
		consumer, err = stream.Consumer(ctx, consumerName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get consumer: %w", err)
		}
	}

	if err := c.resetStaleConsumer(ctx, stream, consumerName, consumer); err != nil {
		return nil, err
	}

	consumer, err = stream.Consumer(ctx, consumerName)
	if err != nil {
		return nil, fmt.Errorf("failed to load consumer after validation: %w", err)
	}

	c.mu.Lock()
	c.consumers[key] = consumer
	c.mu.Unlock()
	return consumer, nil
}

func (c *Client) resetStaleConsumer(ctx context.Context, stream jetstream.Stream, consumerName string, consumer jetstream.Consumer) error {
	streamInfo, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to load stream info: %w", err)
	}

	consumerInfo, err := consumer.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to load consumer info: %w", err)
	}

	if !consumerStateRequiresReset(streamInfo, consumerInfo) {
		return nil
	}

	if err := stream.DeleteConsumer(ctx, consumerName); err != nil {
		return fmt.Errorf("failed to delete stale consumer: %w", err)
	}

	cfg := consumerInfo.Config
	if _, err := stream.CreateConsumer(ctx, cfg); err != nil {
		return fmt.Errorf("failed to recreate consumer: %w", err)
	}

	return nil
}

func consumerStateRequiresReset(streamInfo *jetstream.StreamInfo, consumerInfo *jetstream.ConsumerInfo) bool {
	if streamInfo == nil || consumerInfo == nil {
		return false
	}

	lastSeq := streamInfo.State.LastSeq
	return consumerInfo.Delivered.Stream > lastSeq || consumerInfo.AckFloor.Stream > lastSeq
}

// DeleteConsumer deletes a consumer for the given stream.
func (c *Client) DeleteConsumer(ctx context.Context, streamName string, consumerName string) error {
	stream, err := c.js.Stream(ctx, streamName)
	if err != nil {
		return fmt.Errorf("stream not found: %w", err)
	}

	if err := stream.DeleteConsumer(ctx, consumerName); err != nil {
		return fmt.Errorf("failed to delete consumer: %w", err)
	}

	c.mu.Lock()
	delete(c.consumers, fmt.Sprintf("%s:%s", streamName, consumerName))
	c.mu.Unlock()
	return nil
}

// PublishCoreJSON publishes a JSON-encoded message over core NATS (no
// JetStream stream required). Fire-and-forget fan-out — used for location
// worker heartbeats, where losing one beat is harmless.
func (c *Client) PublishCoreJSON(subject string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	return c.nc.Publish(subject, data)
}

// Request sends a core NATS request and waits for the reply (or ctx done).
// Used for ephemeral RPC-style exchanges (e.g. test-connection checks) that
// must not be persisted or retried by JetStream.
func (c *Client) Request(ctx context.Context, subject string, data []byte) ([]byte, error) {
	msg, err := c.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, err
	}
	return msg.Data, nil
}

// SubscribeRequestReply registers a queue-group subscription whose handler's
// return value is sent back to the requester. Handlers run in their own
// goroutine so a slow request (e.g. a check waiting out its timeout) never
// blocks other requests on the same subscription.
func (c *Client) SubscribeRequestReply(subject, queueGroup string, handler func(data []byte) []byte) (*nats.Subscription, error) {
	return c.nc.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		go func() {
			resp := handler(msg.Data)
			if msg.Reply != "" {
				_ = msg.Respond(resp)
			}
		}()
	})
}

// Subscribe registers a core NATS subscription for live fan-out use cases.
func (c *Client) Subscribe(subject string, handler func(*Message)) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(&Message{
			Data:    msg.Data,
			Subject: msg.Subject,
			Headers: msg.Header,
			Ack: func() error {
				return nil
			},
			Nak: func() error {
				return nil
			},
			InProgress: func() error {
				return nil
			},
		})
	})
}

// Consume consumes messages from a consumer
func (c *Client) Consume(ctx context.Context, consumer jetstream.Consumer, handler func(*Message) error) error {
	// Use FetchMaxWait to control the timeout for fetching messages
	// This allows us to periodically check for context cancellation
	msgs, err := consumer.Messages(jetstream.PullMaxMessages(1), jetstream.PullExpiry(5*time.Second))
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}
	defer msgs.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msg, err := msgs.Next()
			if err != nil {
				// Check if context was cancelled
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					// If it's a timeout (no messages available), continue waiting
					// This is normal and expected when the queue is empty
					if err == context.DeadlineExceeded || err == jetstream.ErrMsgIteratorClosed {
						continue
					}
					return fmt.Errorf("failed to get next message: %w", err)
				}
			}

			// Convert to our Message type
			queueMsg := &Message{
				Data:       msg.Data(),
				Subject:    msg.Subject(),
				Headers:    msg.Headers(),
				Ack:        msg.Ack,
				Nak:        msg.Nak,
				InProgress: msg.InProgress,
			}

			// Handle the message
			if err := handler(queueMsg); err != nil {
				// If handler fails, NAK the message for retry
				if nakErr := msg.Nak(); nakErr != nil {
					return fmt.Errorf("failed to NAK message: %w", nakErr)
				}
				continue
			}

			// Acknowledge successful processing
			if err := msg.Ack(); err != nil {
				return fmt.Errorf("failed to ACK message: %w", err)
			}
		}
	}
}

// HealthCheck performs a simple health check on the NATS connection
func (c *Client) HealthCheck(ctx context.Context) error {
	if c.nc == nil {
		return fmt.Errorf("NATS connection is nil")
	}

	if !c.nc.IsConnected() {
		return fmt.Errorf("NATS connection is not connected")
	}

	// Try to flush to ensure connection is really alive
	if err := c.nc.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("NATS flush failed: %w", err)
	}

	return nil
}

// Close closes the NATS connection
func (c *Client) Close() {
	if c.nc != nil {
		c.nc.Close()
	}
}

// Closed reports whether the underlying connection is permanently closed
// and will never reconnect. Liveness probes use this so Kubernetes restarts
// pods holding a dead connection instead of leaving them as zombies.
func (c *Client) Closed() bool {
	return c.nc == nil || c.nc.IsClosed()
}
