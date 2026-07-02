package worker

import (
	"context"
	"math/rand"
	"os"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// heartbeatInterval is how often a location worker beacons its liveness. The
// platform treats a location as connected while last_seen_at is under a
// minute old, so several beats fit inside the window.
const heartbeatInterval = 15 * time.Second

// heartbeatLoop publishes LocationHeartbeat beacons over core NATS until the
// context ends. Jittered so a fleet of workers at one location doesn't beat
// in lockstep.
func (w *Worker) heartbeatLoop(ctx context.Context) {
	hostname, _ := os.Hostname()

	publish := func() {
		hb := models.LocationHeartbeat{
			LocationID: w.config.LocationID,
			Hostname:   hostname,
			Timestamp:  time.Now().UTC(),
		}
		if err := w.queue.PublishCoreJSON(models.LocationHeartbeatSubject, hb); err != nil {
			w.logger.WithError(err).Debug("Failed to publish location heartbeat")
		}
	}

	publish()
	for {
		jitter := time.Duration(rand.Int63n(int64(2 * time.Second)))
		timer := time.NewTimer(heartbeatInterval + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			publish()
		}
	}
}
