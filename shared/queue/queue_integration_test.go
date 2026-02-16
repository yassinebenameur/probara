package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestClient_PublishAndConsume_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	container, natsURL := startNatsContainer(ctx, t)
	defer func() {
		_ = container.Terminate(ctx)
	}()

	client, err := NewClient(natsURL)
	if err != nil {
		t.Fatalf("failed to create NATS client: %v", err)
	}
	defer client.Close()

	streamName := "TEST_STREAM"
	subject := "test.subject"
	consumerName := "test-consumer"

	if _, err := client.EnsureStream(ctx, streamName, []string{subject}); err != nil {
		t.Fatalf("failed to ensure stream: %v", err)
	}

	consumer, err := client.CreateConsumer(ctx, streamName, consumerName)
	if err != nil {
		t.Fatalf("failed to create consumer: %v", err)
	}

	received := make(chan *Message, 1)
	consumeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- client.Consume(consumeCtx, consumer, func(msg *Message) error {
			received <- msg
			return nil
		})
	}()

	payload := map[string]string{"hello": "world"}
	if err := client.PublishJSON(ctx, subject, payload, nil); err != nil {
		t.Fatalf("failed to publish message: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Subject != subject {
			t.Fatalf("unexpected subject: %s", msg.Subject)
		}
		var decoded map[string]string
		if err := json.Unmarshal(msg.Data, &decoded); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}
		if decoded["hello"] != "world" {
			t.Fatalf("unexpected payload: %v", decoded)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for message")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Fatalf("consume returned unexpected error: %v", err)
		}
	default:
	}
}

func startNatsContainer(ctx context.Context, t *testing.T) (testcontainers.Container, string) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "nats:2.10-alpine",
		ExposedPorts: []string{"4222/tcp"},
		Cmd:          []string{"-js"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("4222/tcp"),
			wait.ForLog("Server is ready"),
		).WithDeadline(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start NATS container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get NATS host: %v", err)
	}

	port, err := container.MappedPort(ctx, "4222/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get NATS port: %v", err)
	}

	url := "nats://" + host + ":" + port.Port()
	return container, url
}
