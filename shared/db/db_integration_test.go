package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestNewClientAndHealthCheck_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	container, dsn := startPostgresContainer(ctx, t)
	defer func() {
		_ = container.Terminate(ctx)
	}()

	client, err := newClientWithRetry(ctx, dsn, 20*time.Second)
	if err != nil {
		t.Fatalf("failed to create db client: %v", err)
	}
	defer client.Close()

	if err := client.HealthCheck(ctx); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func startPostgresContainer(ctx context.Context, t *testing.T) (testcontainers.Container, string) {
	t.Helper()

	const (
		dbUser     = "uptime"
		dbPassword = "uptime"
		dbName     = "uptime"
	)

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     dbUser,
			"POSTGRES_PASSWORD": dbPassword,
			"POSTGRES_DB":       dbName,
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("5432/tcp"),
			wait.ForLog("database system is ready to accept connections"),
		).WithDeadline(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get postgres host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get postgres port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	return container, dsn
}

func newClientWithRetry(ctx context.Context, dsn string, timeout time.Duration) (*Client, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		client, err := NewClient(dsn)
		if err == nil {
			return client, nil
		}
		lastErr = err

		select {
		case <-deadlineCtx.Done():
			return nil, fmt.Errorf("timed out waiting for db readiness: %w", lastErr)
		case <-ticker.C:
		}
	}
}
