package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Client wraps a NATS JetStream client
type Client struct {
	nc        *nats.Conn
	js        jetstream.JetStream
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

// NewClient creates a new NATS JetStream client
func NewClient(natsURL string) (*Client, error) {
	nc, err := nats.Connect(natsURL)
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
	key := fmt.Sprintf("%s:%s", streamName, consumerName)
	if consumer, exists := c.consumers[key]; exists {
		return consumer, nil
	}

	stream, err := c.js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("stream not found: %w", err)
	}

	cfg := jetstream.ConsumerConfig{
		Name:       consumerName,
		Durable:    consumerName,
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    30 * time.Second,
		MaxDeliver: 3,
	}

	consumer, err := stream.CreateConsumer(ctx, cfg)
	if err != nil {
		// Consumer might already exist, try to get it
		consumer, err = stream.Consumer(ctx, consumerName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get consumer: %w", err)
		}
	}

	c.consumers[key] = consumer
	return consumer, nil
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
