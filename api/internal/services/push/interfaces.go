package push

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

var ErrInvalidStatus = errors.New("status must be up, down, or error")

// ValidateStatus also accepts the omitted status used by simple heartbeats.
func ValidateStatus(status string) error {
	switch status {
	case "", "up", "down", "error":
		return nil
	default:
		return ErrInvalidStatus
	}
}

// PushPayload represents the data received from a push request
type PushPayload struct {
	Status  string                 `json:"status"` // "up", "down", or "error"
	Error   string                 `json:"error,omitempty"`
	Metrics map[string]interface{} `json:"metrics,omitempty"` // Auto-detected metrics
}

// PushService defines the interface for push monitor operations
type PushService interface {
	// ProcessPush processes incoming push data and stores it as a check result
	ProcessPush(ctx context.Context, token string, payload PushPayload) error

	// GetMonitorByPushToken retrieves a monitor by its push token
	GetMonitorByPushToken(ctx context.Context, token string) (*models.Monitor, error)

	// GeneratePushToken generates a unique token for a push monitor
	GeneratePushToken() string

	// GetPushInfo returns webhook URL and usage instructions for a push monitor
	GetPushInfo(ctx context.Context, monitorID, tenantID uuid.UUID, backendURL string) (*models.PushInfo, error)
}

// Ensure Service implements PushService
var _ PushService = (*Service)(nil)
