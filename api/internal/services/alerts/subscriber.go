package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	ID            string     `json:"id"`
	MonitorID     string     `json:"monitor_id"`
	MonitorName   string     `json:"monitor_name"`
	AlertPolicyID string     `json:"alert_policy_id"`
	PolicyName    string     `json:"policy_name"`
	Status        string     `json:"status"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	FailureCount  int        `json:"failure_count"`
	LastError     *string    `json:"last_error,omitempty"`
}

// Subscriber listens for alert events from NATS and broadcasts to SSE clients
type Subscriber struct {
	nats         *queue.Client
	hub          *Hub
	config       *config.APIConfig
	logger       *logger.Logger
	cancel       context.CancelFunc
	consumerName string
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

	consumerName := buildConsumerName(s.config.AlertConsumerName)

	// Ensure the ALERTS stream exists
	_, err := s.nats.EnsureStream(subCtx, s.config.AlertStream, []string{s.config.AlertSubject + ".*"})
	if err != nil {
		s.logger.WithError(err).Warn("Failed to ensure ALERTS stream (continuing anyway)")
	}

	// Create consumer for the API
	consumer, err := s.nats.CreateConsumer(subCtx, s.config.AlertStream, consumerName)
	if err != nil {
		return err
	}
	s.consumerName = consumerName

	s.logger.WithFields(map[string]interface{}{
		"stream":   s.config.AlertStream,
		"consumer": consumerName,
		"subject":  s.config.AlertSubject + ".*",
	}).Info("Starting alert event subscription")

	// Start consuming in a goroutine
	go func() {
		for {
			select {
			case <-subCtx.Done():
				s.logger.Info("Alert subscription stopped")
				return
			default:
				if err := s.nats.Consume(subCtx, consumer, s.handleMessage); err != nil {
					if subCtx.Err() != nil {
						// Context cancelled, exit gracefully
						return
					}
					s.logger.WithError(err).Error("Error consuming alert events, retrying...")
					time.Sleep(5 * time.Second)
				}
			}
		}
	}()

	return nil
}

// Stop stops the subscription
func (s *Subscriber) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.nats != nil && s.consumerName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.nats.DeleteConsumer(ctx, s.config.AlertStream, s.consumerName); err != nil {
			s.logger.WithError(err).Warn("Failed to delete alert consumer")
		}
	}
}

func buildConsumerName(base string) string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return base
	}

	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, hostname)

	return fmt.Sprintf("%s-%s", base, sanitized)
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
	monitorID, _ := uuid.Parse(event.Alert.MonitorID)
	policyID, _ := uuid.Parse(event.Alert.AlertPolicyID)

	return &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            alertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatus(event.Alert.Status),
			TriggeredAt:   event.Alert.TriggeredAt,
			ResolvedAt:    event.Alert.ResolvedAt,
			FailureCount:  event.Alert.FailureCount,
			LastError:     event.Alert.LastError,
			CreatedAt:     event.Alert.TriggeredAt,
			UpdatedAt:     event.Timestamp,
		},
		MonitorName: event.Alert.MonitorName,
		PolicyName:  event.Alert.PolicyName,
	}
}
