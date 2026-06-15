package agent

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

func TestStaleWorkerMarksAgentStaleAfterMissedMetricWindow(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	monitorID := uuid.New()
	tenantID := uuid.New()
	lastSuccess := now.Add(-2*time.Minute - time.Second)

	mock.ExpectQuery(regexp.QuoteMeta(agentStaleMonitorsQuery)).
		WithArgs(string(sharedmodels.ResultSourceMonitor)).
		WillReturnRows(agentStaleMonitorRows().
			AddRow(monitorID, tenantID, "local agent", 60, now.Add(-10*time.Minute), sql.NullString{String: "success", Valid: true}, sql.NullTime{Time: lastSuccess, Valid: true}))

	// monitorstate.Record wraps the insert in a transaction that locks the
	// monitor row and advances its state machine.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT current_state, consecutive_failures, consecutive_failures_threshold")).
		WithArgs(monitorID).
		WillReturnRows(sqlmock.NewRows([]string{"current_state", "consecutive_failures", "consecutive_failures_threshold"}).
			AddRow("up", 0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO check_results")).
		WithArgs(
			sqlmock.AnyArg(), // id
			monitorID,
			tenantID,
			sqlmock.AnyArg(), // job_id
			string(sharedmodels.ResultStatusFailure),
			string(sharedmodels.ResultSourceMonitor),
			sqlmock.AnyArg(), // http_status
			sqlmock.AnyArg(), // latency_ms
			"Agent has not reported metrics within twice the expected interval",
			sqlmock.AnyArg(), // metrics_data
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // started_at
			sqlmock.AnyArg(), // completed_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE monitors")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), monitorID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	worker := NewStaleWorker(sqlDB, logger.New("agent-test", "fatal"))
	worker.checkStaleMonitorsAt(context.Background(), now)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestStaleWorkerDoesNotDuplicateExistingStaleFailure(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	monitorID := uuid.New()
	tenantID := uuid.New()
	lastFailure := now.Add(-5 * time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(agentStaleMonitorsQuery)).
		WithArgs(string(sharedmodels.ResultSourceMonitor)).
		WillReturnRows(agentStaleMonitorRows().
			AddRow(monitorID, tenantID, "local agent", 60, now.Add(-10*time.Minute), sql.NullString{String: "failure", Valid: true}, sql.NullTime{Time: lastFailure, Valid: true}))

	worker := NewStaleWorker(sqlDB, logger.New("agent-test", "fatal"))
	worker.checkStaleMonitorsAt(context.Background(), now)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func agentStaleMonitorRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"tenant_id",
		"name",
		"interval_seconds",
		"created_at",
		"last_status",
		"last_check",
	})
}
