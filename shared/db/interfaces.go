package db

import (
	"context"
	"database/sql"
)

// Querier defines read operations on the database
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Executer defines write operations on the database
type Executer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Transactor defines transaction operations
type Transactor interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// DB combines all database operations into a single interface
type DB interface {
	Querier
	Executer
	Transactor
	HealthCheck(ctx context.Context) error
	Close() error
}

// Ensure Client implements DB interface
var _ DB = (*Client)(nil)
