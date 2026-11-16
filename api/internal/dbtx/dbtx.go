// Package dbtx carries one database transaction through a context, so a save
// that spans several services (a monitor update, its group members and
// dependencies, its configuration version) commits or rolls back as one.
//
// A service method that writes calls Run: inside a transaction already
// carried by ctx it joins it (no commit of its own), otherwise it opens and
// commits its own. Reads and single statements use Conn, which is the carried
// transaction when there is one, so a save reads its own uncommitted writes.
package dbtx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yassinebenameur/probara/shared/db"
)

type txKey struct{}

// Conn is what a statement needs: *sql.Tx and db.DB both satisfy it.
type Conn interface {
	db.Querier
	db.Executer
}

// With returns ctx carrying tx.
func With(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// From returns the transaction ctx carries, if any.
func From(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok && tx != nil
}

// Of returns the carried transaction, or fallback outside one.
func Of(ctx context.Context, fallback Conn) Conn {
	if tx, ok := From(ctx); ok {
		return tx
	}
	return fallback
}

// Run calls fn inside a transaction: the one ctx carries, or a new one begun
// on database and committed when fn returns nil (rolled back otherwise). The
// ctx passed to fn carries the transaction.
func Run(ctx context.Context, database db.Transactor, fn func(ctx context.Context, tx *sql.Tx) error) error {
	if tx, ok := From(ctx); ok {
		return fn(ctx, tx)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(With(ctx, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Savepoint runs fn inside a savepoint of tx, so a failed statement in fn
// rolls back fn's work only instead of aborting the whole transaction.
func Savepoint(ctx context.Context, tx *sql.Tx, name string, fn func() error) error {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name); rbErr != nil {
			return fmt.Errorf("%w (and rollback to savepoint failed: %v)", err, rbErr)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("release savepoint: %w", err)
	}
	return nil
}
