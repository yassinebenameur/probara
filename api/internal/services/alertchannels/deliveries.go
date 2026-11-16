package alertchannels

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// attachHealth sets each channel's last delivery times and failing flag from
// the delivery history. Best effort: the history is an annotation, so a
// failed read is logged and the channels go out without it (a PATCH that
// already committed must not answer 500 over it).
func (s *Service) attachHealth(ctx context.Context, tenantID uuid.UUID, channels ...*models.AlertChannel) {
	if len(channels) == 0 || s.db == nil {
		return
	}
	ids := make([]uuid.UUID, len(channels))
	for i, ch := range channels {
		ids[i] = ch.ID
	}
	health, err := deliverylog.ChannelHealth(ctx, s.db, tenantID, ids)
	if err != nil {
		if s.logger != nil {
			s.logger.WithError(err).WithField("tenant_id", tenantID).Warn("Failed to load alert channel delivery health")
		}
		return
	}
	for _, ch := range channels {
		h := health[ch.ID]
		ch.LastDeliveredAt, ch.LastFailedAt, ch.Failing = h.LastDeliveredAt, h.LastFailedAt, h.Failing
	}
}

// recordTestSend appends a test send to the channel's delivery history,
// marked as a test and tied to no alert. Best effort: the caller gets the
// send's own result whatever this returns.
func (s *Service) recordTestSend(ctx context.Context, channelID uuid.UUID, manifest plugin.Manifest, config map[string]any, started time.Time, sendErr error) error {
	if s.db == nil {
		return nil
	}
	outcome, reason := deliverylog.SendOutcome(sendErr)
	return deliverylog.Record(ctx, s.db, deliverylog.Attempt{
		ChannelID: channelID,
		EventType: "created",
		Path:      deliverylog.PathSync,
		Number:    1,
		Test:      true,
		Outcome:   outcome,
		Reason:    reason,
		Err:       sendErr,
		Secrets:   deliverylog.SecretValues(manifest, config),
		Addresses: deliverylog.RecipientValues(manifest, config),
		Duration:  time.Since(started),
	})
}
