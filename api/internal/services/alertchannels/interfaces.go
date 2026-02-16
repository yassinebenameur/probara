package alertchannels

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// AlertChannelService defines the interface for alert channel operations
type AlertChannelService interface {
	CreateAlertChannel(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertChannelRequest) (*models.AlertChannel, error)
	GetAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) (*models.AlertChannel, error)
	ListAlertChannels(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.AlertChannelListResponse, error)
	UpdateAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID, req *models.UpdateAlertChannelRequest) (*models.AlertChannel, error)
	DeleteAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error
	TestAlertChannel(ctx context.Context, tenantID, channelID uuid.UUID) error
}

// Ensure Service implements AlertChannelService
var _ AlertChannelService = (*Service)(nil)
