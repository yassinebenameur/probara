package alertchannels

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// attachLastDeliveries sets each listed channel's LastDelivery, the newest
// row of its delivery history, so the channel list can flag a channel whose
// last delivery failed.
func (s *Service) attachLastDeliveries(ctx context.Context, tenantID uuid.UUID, items []models.AlertChannel) error {
	if len(items) == 0 || s.db == nil {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	last, err := deliverylog.LastForChannels(ctx, s.db, tenantID, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if d, ok := last[items[i].ID]; ok {
			items[i].LastDelivery = &d
		}
	}
	return nil
}
