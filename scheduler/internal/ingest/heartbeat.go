package ingest

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

// heartbeatWriteThrottle bounds DB writes per location: workers beat every
// ~15s, and sub-10s freshness buys nothing (the UI treats <60s as connected).
const heartbeatWriteThrottle = 10 * time.Second

// startHeartbeatSubscriber fans worker liveness beacons into
// locations.last_seen_at. Core NATS (fire-and-forget): a lost heartbeat just
// delays the connected badge by one beat.
func (i *Ingest) startHeartbeatSubscriber() (*nats.Subscription, error) {
	var mu sync.Mutex
	lastWrite := make(map[string]time.Time)

	return i.queue.Subscribe(models.LocationHeartbeatSubject, func(msg *queue.Message) {
		var hb models.LocationHeartbeat
		if err := json.Unmarshal(msg.Data, &hb); err != nil {
			i.logger.WithError(err).Debug("Ignoring malformed location heartbeat")
			return
		}
		locationID, err := uuid.Parse(hb.LocationID)
		if err != nil {
			i.logger.WithField("location_id", hb.LocationID).Debug("Ignoring heartbeat with invalid location_id")
			return
		}

		mu.Lock()
		if last, ok := lastWrite[hb.LocationID]; ok && time.Since(last) < heartbeatWriteThrottle {
			mu.Unlock()
			return
		}
		lastWrite[hb.LocationID] = time.Now()
		mu.Unlock()

		ctx, cancel := context.WithTimeout(i.ctx, 5*time.Second)
		defer cancel()
		if _, err := i.db.ExecContext(ctx, `
			UPDATE locations
			SET last_seen_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL
		`, locationID); err != nil {
			i.logger.WithError(err).Warn("Failed to record location heartbeat")
		}
	})
}
