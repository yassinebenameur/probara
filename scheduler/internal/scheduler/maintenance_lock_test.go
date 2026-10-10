package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestMaintenanceLock_DiscardsSessionWhenAcquisitionIsUncertain(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(retentionCleanupAdvisoryLock).
		WillReturnError(errors.New("connection lost after request"))
	mock.ExpectClose()
	lock, err := acquireMaintenanceLock(context.Background(), database, retentionCleanupAdvisoryLock)
	require.Error(t, err)
	require.Nil(t, lock)
	require.Zero(t, database.Stats().OpenConnections, "uncertain session must not return to pool")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMaintenanceLock_DiscardsSessionWhenReleaseFails(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-held", true: "lost-response"}[lostResponse], func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = database.Close() })
			mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(retentionCleanupAdvisoryLock).
				WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			unlock := mock.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(retentionCleanupAdvisoryLock)
			if lostResponse {
				unlock.WillReturnError(errors.New("connection lost"))
			} else {
				unlock.WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(false))
			}
			mock.ExpectClose()
			lock, err := acquireMaintenanceLock(context.Background(), database, retentionCleanupAdvisoryLock)
			require.NoError(t, err)
			require.Error(t, lock.release())
			require.Zero(t, database.Stats().OpenConnections, "failed unlock must not leak a session back into the pool")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
