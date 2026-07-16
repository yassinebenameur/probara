package alerts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// AlertEvent represents an alert event from NATS (matches alerter's AlertEvent)
type AlertEvent struct {
	Type      string       `json:"type"` // "created", "resolved"
	TenantID  string       `json:"tenant_id"`
	Alert     AlertDetails `json:"alert"`
	Timestamp time.Time    `json:"timestamp"`
}

// AlertDetails represents alert details from the event
type AlertDetails struct {
	ID                   string     `json:"id"`
	MonitorID            string     `json:"monitor_id"`
	MonitorName          string     `json:"monitor_name"`
	AlertPolicyID        string     `json:"alert_policy_id"`
	PolicyName           string     `json:"policy_name"`
	Status               string     `json:"status"`
	TriggeredAt          time.Time  `json:"triggered_at"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
	FailureCount         int        `json:"failure_count"`
	LastError            *string    `json:"last_error,omitempty"`
	RootCauseMonitorID   *string    `json:"root_cause_monitor_id,omitempty"`
	RootCauseMonitorName *string    `json:"root_cause_monitor_name,omitempty"`
	RootCauseDownSince   *time.Time `json:"root_cause_down_since,omitempty"`
}

// Subscriber listens for alert events from NATS and broadcasts to SSE clients
type Subscriber struct {
	nats   *queue.Client
	hub    *Hub
	config *config.APIConfig
	logger *logger.Logger
	cancel context.CancelFunc
}

// NewSubscriber creates a new alert subscriber
func NewSubscriber(natsClient *queue.Client, hub *Hub, cfg *config.APIConfig, log *logger.Logger) *Subscriber {
	return &Subscriber{
		nats:   natsClient,
		hub:    hub,
		config: cfg,
		logger: log,
	}
}

// Start begins listening for alert events
func (s *Subscriber) Start(ctx context.Context) error {
	if s.nats == nil {
		s.logger.Warn("NATS client is nil, alert subscription disabled")
		return nil
	}

	// Create a cancellable context for the subscription
	subCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	subject := s.config.AlertSubject + ".*"
	subscription, err := s.nats.Subscribe(subject, func(msg *queue.Message) {
		if err := s.handleMessage(msg); err != nil {
			s.logger.WithError(err).Error("Failed to process alert event")
		}
	})
	if err != nil {
		return err
	}

	s.logger.WithFields(map[string]interface{}{
		"subject": subject,
	}).Info("Starting alert event subscription")

	go func() {
		<-subCtx.Done()
		if err := subscription.Unsubscribe(); err != nil {
			s.logger.WithError(err).Warn("Failed to unsubscribe from alert events")
		}
		s.logger.Info("Alert subscription stopped")
	}()

	return nil
}

// Stop stops the subscription
func (s *Subscriber) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// handleMessage processes an incoming alert event
func (s *Subscriber) handleMessage(msg *queue.Message) error {
	var event AlertEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		s.logger.WithError(err).Error("Failed to unmarshal alert event")
		return nil // Don't retry malformed messages
	}

	tenantID, err := uuid.Parse(event.TenantID)
	if err != nil {
		s.logger.WithError(err).Error("Invalid tenant ID in alert event")
		return nil
	}

	// Convert to models.AlertWithDetails for the hub
	alert := s.convertToAlertWithDetails(&event)

	// Log at INFO level for visibility
	clientCount := s.hub.ClientCount(tenantID)
	s.logger.WithFields(map[string]interface{}{
		"event_type":        event.Type,
		"alert_id":          event.Alert.ID,
		"tenant_id":         event.TenantID,
		"monitor":           event.Alert.MonitorName,
		"connected_clients": clientCount,
	}).Info("Received alert event from NATS, broadcasting to SSE clients")

	// Broadcast to SSE clients based on event type
	switch event.Type {
	case "created":
		s.hub.BroadcastAlertCreated(tenantID, alert)
		s.logger.WithFields(map[string]interface{}{
			"alert_id":          event.Alert.ID,
			"connected_clients": clientCount,
		}).Info("Broadcasted alert_created event")
	case "resolved":
		s.hub.BroadcastAlertResolved(tenantID, alert)
		s.logger.WithFields(map[string]interface{}{
			"alert_id":          event.Alert.ID,
			"connected_clients": clientCount,
		}).Info("Broadcasted alert_resolved event")
	default:
		s.logger.WithFields(map[string]interface{}{
			"event_type": event.Type,
		}).Warn("Unknown alert event type")
	}

	return nil
}

// convertToAlertWithDetails converts the NATS event to models.AlertWithDetails
func (s *Subscriber) convertToAlertWithDetails(event *AlertEvent) *models.AlertWithDetails {
	alertID, _ := uuid.Parse(event.Alert.ID)
	tenantID, _ := uuid.Parse(event.TenantID)
	var monitorID *uuid.UUID
	if parsed, err := uuid.Parse(event.Alert.MonitorID); err == nil && parsed != uuid.Nil {
		monitorID = &parsed
	}
	var monitorName *string
	if event.Alert.MonitorName != "" {
		monitorName = &event.Alert.MonitorName
	}

	alert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:           alertID,
			TenantID:     tenantID,
			MonitorID:    monitorID,
			Status:       models.AlertStatus(event.Alert.Status),
			TriggeredAt:  event.Alert.TriggeredAt,
			ResolvedAt:   event.Alert.ResolvedAt,
			FailureCount: event.Alert.FailureCount,
			LastError:    event.Alert.LastError,
			CreatedAt:    event.Alert.TriggeredAt,
			UpdatedAt:    event.Timestamp,
		},
		MonitorName: monitorName,
	}

	if event.Alert.RootCauseMonitorID != nil {
		if rcID, err := uuid.Parse(*event.Alert.RootCauseMonitorID); err == nil {
			alert.RootCauseMonitorID = &rcID
		}
	}
	alert.RootCauseMonitorName = event.Alert.RootCauseMonitorName
	alert.RootCauseDownSince = event.Alert.RootCauseDownSince

	if event.Alert.AlertPolicyID != "" {
		if policyID, err := uuid.Parse(event.Alert.AlertPolicyID); err == nil {
			alert.AlertPolicyID = &policyID
		}
	}
	if event.Alert.PolicyName != "" {
		alert.PolicyName = &event.Alert.PolicyName
	}

	return alert
}
