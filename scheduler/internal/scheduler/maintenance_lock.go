package scheduler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// maintenanceLock reserves one database session for the whole job. Work may
// use the pool, but acquiring and releasing a session advisory lock must use
// the same connection, and no other job may borrow it while the lock is held.
type maintenanceLock struct {
	conn *sql.Conn
	id   int64
}

func acquireMaintenanceLock(ctx context.Context, database *sql.DB, id int64) (*maintenanceLock, error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve maintenance lock connection: %w", err)
	}
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", id).Scan(&locked); err != nil {
		// A lost response does not prove that PostgreSQL failed to acquire the
		// lock. Discard this session instead of returning it to the pool.
		discardMaintenanceConnection(conn)
		return nil, fmt.Errorf("acquire maintenance advisory lock: %w", err)
	}
	if !locked {
		return nil, conn.Close()
	}
	return &maintenanceLock{conn: conn, id: id}, nil
}

func (l *maintenanceLock) release() error {
	// Cancellation of the job must not prevent cleanup, nor may an
	// unreachable database keep the scheduler's job marked running forever.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := l.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", l.id).Scan(&unlocked); err != nil {
		discardMaintenanceConnection(l.conn)
		return fmt.Errorf("release maintenance advisory lock: %w", err)
	}
	if !unlocked {
		discardMaintenanceConnection(l.conn)
		return fmt.Errorf("maintenance advisory lock %d was not held by its reserved session", l.id)
	}
	return l.conn.Close()
}

func discardMaintenanceConnection(conn *sql.Conn) {
	_ = conn.Raw(func(interface{}) error { return driver.ErrBadConn })
	_ = conn.Close()
}
