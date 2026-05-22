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

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO check_results
		 (id, monitor_id, tenant_id, job_id, status, result_source, error_message, created_at, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`)).
		WithArgs(
			sqlmock.AnyArg(),
			monitorID,
			tenantID,
			sqlmock.AnyArg(),
			string(sharedmodels.ResultStatusFailure),
			string(sharedmodels.ResultSourceMonitor),
			"Agent has not reported metrics within twice the expected interval",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

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
