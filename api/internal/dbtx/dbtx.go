// Package dbtx carries one database transaction through a context, so a save
// that spans several services (a monitor update, its group members and
// dependencies, its configuration version) commits or rolls back as one.
//
// A service method that writes calls Run: inside a transaction already
// carried by ctx it joins it (no commit of its own), otherwise it opens and
// commits its own. Reads and single statements use Of, which is the carried
// transaction when there is one, so a save reads its own uncommitted writes
// and never needs a second pool connection while it holds one.
//
// Lock order. Every transaction that locks more than one monitor takes its
// locks in this order, so two of them can never wait on each other in a
// cycle:
//
//  1. monitor rows, in ascending id order — FOR UPDATE for a monitor the
//     transaction changes, FOR KEY SHARE for one it only points at (a new
//     dependency or group member edge) — all before the first write
//     (api/internal/services/monitors LockMonitorsTx; location delete locks
//     its monitors the same way);
//  2. then the configuration-version advisory locks of those monitors, in
//     ascending id order (monitors versions_store.go).
//
// A deadlock or serialization failure that still happens (an order a path
// does not control) is retried once by RunRetry and otherwise reported with
// IsConflict, never as a bare server error.
package dbtx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
)

// ErrConflict wraps a deadlock or serialization failure that survived the
// retry: another change to the same monitors won. Nothing was written.
var ErrConflict = errors.New("another change to the same monitors was in progress; nothing was saved, try again")

// IsConflict reports a deadlock (40P01) or serialization failure (40001),
// raw or already wrapped in ErrConflict.
func IsConflict(err error) bool {
	if errors.Is(err, ErrConflict) {
		return true
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "40P01" || pqErr.Code == "40001"
	}
	return false
}

// Retry runs fn, a whole transaction of its own, once more after a deadlock
// or serialization failure; a second conflict is wrapped in ErrConflict.
func Retry(ctx context.Context, fn func() error) error {
	err := fn()
	if !IsConflict(err) || ctx.Err() != nil {
		return err
	}
	if err = fn(); IsConflict(err) {
		return fmt.Errorf("%w (%v)", ErrConflict, err)
	}
	return err
}

// RunRetry is Run that, when it opens the transaction itself, runs fn once
// more after a deadlock or serialization failure (fn must be safe to re-run:
// it only writes through the transaction). A second conflict is returned
// wrapped in ErrConflict. Inside a carried transaction it is Run: the owner
// of that transaction decides.
func RunRetry(ctx context.Context, database db.Transactor, fn func(ctx context.Context, tx *sql.Tx) error) error {
	if tx, ok := From(ctx); ok {
		return fn(ctx, tx)
	}
	err := Run(ctx, database, fn)
	if !IsConflict(err) || ctx.Err() != nil {
		return err
	}
	if err = Run(ctx, database, fn); IsConflict(err) {
		return fmt.Errorf("%w (%v)", ErrConflict, err)
	}
	return err
}

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
func Savepoint(ctx context.Context, tx db.Executer, name string, fn func() error) error {
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
