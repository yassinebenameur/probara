package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// StaleWorker monitors push monitors and marks them as down if stale
type StaleWorker struct {
	db       *sql.DB
	log      *logger.Logger
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup

	// Track which monitors have already been marked as stale to avoid duplicate down results
	staleMonitors map[uuid.UUID]time.Time
	mu            sync.RWMutex
}

// NewStaleWorker creates a new stale worker
func NewStaleWorker(db *sql.DB, log *logger.Logger) *StaleWorker {
	return &StaleWorker{
		db:            db,
		log:           log,
		interval:      10 * time.Second,
		stop:          make(chan struct{}),
		staleMonitors: make(map[uuid.UUID]time.Time),
	}
}

// Start begins the stale check loop
func (w *StaleWorker) Start() {
	w.wg.Add(1)
	go w.run()
	w.log.Info("Push stale worker started")
}

// Stop gracefully stops the worker
func (w *StaleWorker) Stop() {
	close(w.stop)
	w.wg.Wait()
	w.log.Info("Push stale worker stopped")
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

	// Get all enabled push monitors
	rows, err := w.db.QueryContext(ctx,
		`SELECT m.id, m.tenant_id, m.name, m.interval_seconds, m.config,
		        COALESCE(
		            (SELECT MAX(created_at) FROM check_results WHERE monitor_id = m.id),
		            m.created_at
		        ) as last_check
		 FROM monitors m
		 WHERE m.type = 'push' AND m.enabled = true`)
	if err != nil {
		w.log.WithError(err).Error("failed to query push monitors")
		return
	}
	defer rows.Close()

	now := time.Now()

	for rows.Next() {
		var monitorID, tenantID uuid.UUID
		var name string
		var intervalSeconds int
		var configJSON []byte
		var lastCheck time.Time

		if err := rows.Scan(&monitorID, &tenantID, &name, &intervalSeconds, &configJSON, &lastCheck); err != nil {
			w.log.WithError(err).Error("failed to scan push monitor row")
			continue
		}

		// Parse config to get grace period
		var config models.PushConfig
		if err := json.Unmarshal(configJSON, &config); err != nil {
			// Default grace period to interval if config parsing fails
			config.GracePeriodSeconds = intervalSeconds
		}

		// Calculate total allowed gap
		totalGapSeconds := intervalSeconds + config.GracePeriodSeconds
		staleThreshold := lastCheck.Add(time.Duration(totalGapSeconds) * time.Second)

		if now.After(staleThreshold) {
			// Monitor is stale
			w.handleStaleMonitor(ctx, monitorID, tenantID, name, lastCheck, now)
		} else {
			// Monitor is healthy - remove from stale tracking if present
			w.clearStaleState(monitorID)
		}
	}

	if err := rows.Err(); err != nil {
		w.log.WithError(err).Error("error iterating push monitors")
	}
}

func (w *StaleWorker) handleStaleMonitor(ctx context.Context, monitorID, tenantID uuid.UUID, name string, lastCheck, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if we've already marked this monitor as stale recently
	// This implements the debounce - only one "down" result per stale period
	if lastMarked, exists := w.staleMonitors[monitorID]; exists {
		// If we marked it within the last minute, skip
		if now.Sub(lastMarked) < time.Minute {
			return
		}
	}

	// Insert a failure check result
	jobID := uuid.New()
	errorMessage := "No push received within expected interval + grace period"

	_, err := w.db.ExecContext(ctx,
		`INSERT INTO check_results 
		 (id, monitor_id, tenant_id, job_id, status, result_source, error_message, created_at, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New(),
		monitorID,
		tenantID,
		jobID,
		"failure",
		string(sharedmodels.ResultSourceMonitor),
		errorMessage,
		now,
		now,
		now,
	)
	if err != nil {
		w.log.WithError(err).WithFields(map[string]interface{}{
			"monitor_id": monitorID,
			"name":       name,
		}).Error("failed to insert stale check result")
		return
	}

	// Track that we've marked this monitor as stale
	w.staleMonitors[monitorID] = now

	w.log.WithFields(map[string]interface{}{
		"monitor_id": monitorID,
		"name":       name,
		"last_check": lastCheck,
	}).Warn("Push monitor marked as stale")
}

func (w *StaleWorker) clearStaleState(monitorID uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.staleMonitors, monitorID)
}

// ClearStaleStateForMonitor clears the stale state when a push is received
// This is called by the push service when a successful push is processed
func (w *StaleWorker) ClearStaleStateForMonitor(monitorID uuid.UUID) {
	w.clearStaleState(monitorID)
}
