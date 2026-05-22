package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

const (
	agentStaleCheckInterval = 10 * time.Second
	agentStaleWindowFactor  = 2
)

const agentStaleMonitorsQuery = `
		SELECT m.id, m.tenant_id, m.name, m.interval_seconds, m.created_at,
		       latest.status, latest.created_at AS last_check
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT status, created_at
			FROM check_results
			WHERE monitor_id = m.id
			  AND tenant_id = m.tenant_id
			  AND result_source = $1
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) latest ON true
		WHERE m.type = 'agent' AND m.enabled = true`

// StaleWorker monitors passive agent monitors and emits a failure result when
// an enabled agent has not reported within twice its expected interval.
type StaleWorker struct {
	db        *sql.DB
	log       *logger.Logger
	publisher *statusupdates.Publisher
	interval  time.Duration
	stop      chan struct{}
	wg        sync.WaitGroup
}

// NewStaleWorker creates a new stale worker.
func NewStaleWorker(db *sql.DB, log *logger.Logger, publisher ...*statusupdates.Publisher) *StaleWorker {
	var statusPublisher *statusupdates.Publisher
	if len(publisher) > 0 {
		statusPublisher = publisher[0]
	}
	return &StaleWorker{
		db:        db,
		log:       log,
		publisher: statusPublisher,
		interval:  agentStaleCheckInterval,
		stop:      make(chan struct{}),
	}
}

// Start begins the stale check loop.
func (w *StaleWorker) Start() {
	w.wg.Add(1)
	go w.run()
	w.log.Info("Agent stale worker started")
}

// Stop gracefully stops the worker.
func (w *StaleWorker) Stop() {
	close(w.stop)
	w.wg.Wait()
	w.log.Info("Agent stale worker stopped")
}

func (w *StaleWorker) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			w.checkStaleMonitors()
		}
	}
}

func (w *StaleWorker) checkStaleMonitors() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w.checkStaleMonitorsAt(ctx, time.Now().UTC())
}

func (w *StaleWorker) checkStaleMonitorsAt(ctx context.Context, now time.Time) {
	rows, err := w.db.QueryContext(ctx, agentStaleMonitorsQuery, string(sharedmodels.ResultSourceMonitor))
	if err != nil {
		w.log.WithError(err).Error("failed to query agent monitors")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID, tenantID uuid.UUID
		var name string
		var intervalSeconds int
		var createdAt time.Time
		var lastStatus sql.NullString
		var lastCheck sql.NullTime

		if err := rows.Scan(&monitorID, &tenantID, &name, &intervalSeconds, &createdAt, &lastStatus, &lastCheck); err != nil {
			w.log.WithError(err).Error("failed to scan agent monitor row")
			continue
		}

		if !shouldMarkAgentStale(now, intervalSeconds, createdAt, lastStatus, lastCheck) {
			continue
		}

		if err := w.insertStaleResult(ctx, monitorID, tenantID, now); err != nil {
			w.log.WithError(err).WithFields(map[string]interface{}{
				"monitor_id": monitorID,
				"name":       name,
			}).Error("failed to insert agent stale check result")
			continue
		}

		w.publishStatusUpdate(monitorID, tenantID)
		w.log.WithFields(map[string]interface{}{
			"monitor_id": monitorID,
			"name":       name,
			"last_check": lastObservedAt(createdAt, lastCheck),
		}).Warn("Agent monitor marked as stale")
	}

	if err := rows.Err(); err != nil {
		w.log.WithError(err).Error("error iterating agent monitors")
	}
}

func shouldMarkAgentStale(now time.Time, intervalSeconds int, createdAt time.Time, lastStatus sql.NullString, lastCheck sql.NullTime) bool {
	if intervalSeconds <= 0 {
		return false
	}
	if lastStatus.Valid && lastStatus.String != string(sharedmodels.ResultStatusSuccess) {
		return false
	}

	staleAfter := lastObservedAt(createdAt, lastCheck).Add(time.Duration(intervalSeconds*agentStaleWindowFactor) * time.Second)
	return now.After(staleAfter)
}

func lastObservedAt(createdAt time.Time, lastCheck sql.NullTime) time.Time {
	if lastCheck.Valid {
		return lastCheck.Time
	}
	return createdAt
}

func (w *StaleWorker) insertStaleResult(ctx context.Context, monitorID, tenantID uuid.UUID, now time.Time) error {
	const errorMessage = "Agent has not reported metrics within twice the expected interval"

	_, err := w.db.ExecContext(ctx,
		`INSERT INTO check_results
		 (id, monitor_id, tenant_id, job_id, status, result_source, error_message, created_at, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New(),
		monitorID,
		tenantID,
		uuid.New(),
		string(sharedmodels.ResultStatusFailure),
		string(sharedmodels.ResultSourceMonitor),
		errorMessage,
		now,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert stale result: %w", err)
	}
	return nil
}

func (w *StaleWorker) publishStatusUpdate(monitorID, tenantID uuid.UUID) {
	if w.publisher == nil {
		return
	}
	_ = w.publisher.Publish(statusupdates.Event{
		Type:      "check_result",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	})
}
