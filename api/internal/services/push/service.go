package push

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// Service handles push monitor business logic
type Service struct {
	db        *sql.DB
	publisher *statusupdates.Publisher
}

// NewService creates a new push service
func NewService(db *sql.DB, publisher *statusupdates.Publisher) *Service {
	return &Service{db: db, publisher: publisher}
}

// ProcessPush processes incoming push data and stores it as a check result
func (s *Service) ProcessPush(ctx context.Context, token string, payload PushPayload) error {
	// Look up the monitor by push token
	var monitorID uuid.UUID
	var tenantID uuid.UUID
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, enabled FROM monitors 
		 WHERE push_token = $1 AND type = 'push'`,
		token,
	).Scan(&monitorID, &tenantID, &enabled)
	if err == sql.ErrNoRows {
		return fmt.Errorf("push monitor not found for token: %s", token)
	}
	if err != nil {
		return fmt.Errorf("failed to lookup push monitor: %w", err)
	}

	if !enabled {
		return fmt.Errorf("push monitor is disabled")
	}

	// Determine status - default to "success" (up) if not specified
	status := "success"
	if payload.Status != "" {
		switch payload.Status {
		case "up":
			status = "success"
		case "down":
			status = "failure"
		case "error":
			status = "error"
		default:
			status = "success"
		}
	}

	// Prepare error message if provided
	var errorMessage *string
	if payload.Error != "" {
		errorMessage = &payload.Error
	}

	// Marshal metrics to JSON
	var metricsJSON []byte
	if len(payload.Metrics) > 0 {
		metricsJSON, err = json.Marshal(payload.Metrics)
		if err != nil {
			return fmt.Errorf("failed to marshal metrics: %w", err)
		}
	}

	// Generate a job ID for this push
	jobID := uuid.New()
	now := time.Now()

	// Insert check result
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO check_results 
		 (id, monitor_id, tenant_id, job_id, status, error_message, metrics_data, created_at, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New(),
		monitorID,
		tenantID,
		jobID,
		status,
		errorMessage,
		metricsJSON,
		now,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to insert check result: %w", err)
	}

	s.publishStatusUpdate(monitorID, tenantID)
	return nil
}

func (s *Service) publishStatusUpdate(monitorID, tenantID uuid.UUID) {
	if s.publisher == nil {
		return
	}
	event := statusupdates.Event{
		Type:      "check_result",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	}
	if err := s.publisher.Publish(event); err != nil {
		// Best-effort; ignore publish errors.
	}
}

// GetMonitorByPushToken retrieves a monitor by its push token
func (s *Service) GetMonitorByPushToken(ctx context.Context, token string) (*models.Monitor, error) {
	var monitor models.Monitor
	var alertPolicyID sql.NullString
	var agentID sql.NullString
	var pushToken sql.NullString
	var nextRunAt sql.NullTime

	err := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, type, config, interval_seconds, timeout_seconds, 
		        alert_policy_id, enabled, tags, next_run_at, agent_id, push_token, created_at, updated_at
		 FROM monitors 
		 WHERE push_token = $1 AND type = 'push'`,
		token,
	).Scan(
		&monitor.ID, &monitor.TenantID, &monitor.Name, &monitor.Type, &monitor.Config,
		&monitor.IntervalSeconds, &monitor.TimeoutSeconds, &alertPolicyID, &monitor.Enabled,
		&monitor.Tags, &nextRunAt, &agentID, &pushToken, &monitor.CreatedAt, &monitor.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("push monitor not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get push monitor: %w", err)
	}

	if alertPolicyID.Valid {
		id, _ := uuid.Parse(alertPolicyID.String)
		monitor.AlertPolicyID = &id
	}
	if agentID.Valid {
		monitor.AgentID = &agentID.String
	}
	if pushToken.Valid {
		monitor.PushToken = &pushToken.String
	}
	if nextRunAt.Valid {
		monitor.NextRunAt = &nextRunAt.Time
	}

	return &monitor, nil
}

// GeneratePushToken generates a unique token for a push monitor
func (s *Service) GeneratePushToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// GetPushInfo returns webhook URL and usage instructions for a push monitor
func (s *Service) GetPushInfo(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL string) (*models.PushInfo, error) {
	var pushToken sql.NullString
	var intervalSeconds int
	var config json.RawMessage

	err := s.db.QueryRowContext(ctx,
		`SELECT push_token, interval_seconds, config FROM monitors 
		 WHERE id = $1 AND tenant_id = $2 AND type = 'push'`,
		monitorID, tenantID,
	).Scan(&pushToken, &intervalSeconds, &config)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("push monitor not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get push monitor: %w", err)
	}

	if !pushToken.Valid {
		return nil, fmt.Errorf("push monitor does not have a token")
	}

	// Parse config to get grace period
	var pushConfig models.PushConfig
	if err := json.Unmarshal(config, &pushConfig); err != nil {
		// Use default grace period if config parsing fails
		pushConfig.GracePeriodSeconds = intervalSeconds
	}

	webhookURL := fmt.Sprintf("%s/api/v1/push/%s", backendURL, pushToken.String)
	exampleCurl := fmt.Sprintf(`# Simple heartbeat (marks as UP)
curl -X POST "%s"

# With custom metrics (JSON body)
curl -X POST "%s" \
  -H "Content-Type: application/json" \
  -d '{"status": "up", "latency": 45, "cpu_percent": 23.5}'

# With query parameters
curl "%s?status=up&latency=45&cpu=23"

# Mark as DOWN explicitly
curl -X POST "%s?status=down&error=Service%%20unavailable"`,
		webhookURL, webhookURL, webhookURL, webhookURL)

	return &models.PushInfo{
		PushToken:       pushToken.String,
		WebhookURL:      webhookURL,
		IntervalSeconds: intervalSeconds,
		GracePeriodSecs: pushConfig.GracePeriodSeconds,
		ExampleCurl:     exampleCurl,
	}, nil
}
