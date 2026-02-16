package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Client wraps a PostgreSQL database connection
type Client struct {
	*sql.DB
}

// NewClient creates a new PostgreSQL client
func NewClient(postgresURL string) (*Client, error) {
	if postgresURL == "" {
		return nil, fmt.Errorf("postgres URL is required")
	}

	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Client{DB: db}, nil
}

// HealthCheck performs a simple health check query
func (c *Client) HealthCheck(ctx context.Context) error {
	var result int
	err := c.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}

// Close closes the database connection
func (c *Client) Close() error {
	return c.DB.Close()
}
