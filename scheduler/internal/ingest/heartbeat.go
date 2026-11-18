package ingest

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

// heartbeatWriteThrottle bounds DB writes per location: workers beat every
// ~15s, and sub-10s freshness buys nothing (the UI treats <60s as connected).
const heartbeatWriteThrottle = 10 * time.Second

// heartbeatMaxSkew bounds how far a heartbeat's worker timestamp may be from
// the scheduler's clock. It keeps a captured beat from being replayed later;
// the cost is that a worker whose clock drifted further is never seen, and
// its location reads disconnected, then offline (S-F6), while its checks
// keep flowing.
const heartbeatMaxSkew = 2 * time.Minute

// heartbeatSkewWarnEvery rate-limits the skew warning per location.
const heartbeatSkewWarnEvery = 5 * time.Minute

// heartbeatSkew is how far ts is from now, as a positive duration.
func heartbeatSkew(ts, now time.Time) time.Duration {
	d := now.Sub(ts)
	if d < 0 {
		d = -d
	}
	return d
}

// startHeartbeatSubscriber fans worker liveness beacons into
// locations.last_seen_at. Core NATS (fire-and-forget): a lost heartbeat just
// delays the connected badge by one beat.
func (i *Ingest) startHeartbeatSubscriber() (*nats.Subscription, error) {
	var mu sync.Mutex
	lastWrite := make(map[string]time.Time)
	lastSkewWarn := make(map[string]time.Time)

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
		if hb.Timestamp.IsZero() {
			i.logger.WithField("location_id", hb.LocationID).Debug("Ignoring heartbeat without a timestamp")
			return
		}
		ctx, cancel := context.WithTimeout(i.ctx, 5*time.Second)
		defer cancel()
		credential, err := i.locationCredential(ctx, hb.LocationID, "", "")
		if err != nil {
			// Core NATS cannot redeliver, so either way this beat is gone and
			// the next one (~15 s) retries; only the log tells a platform
			// failure apart from an unauthorized sender.
			if isRejected(err) {
				i.logger.WithError(err).WithField("location_id", hb.LocationID).Debug("Ignoring unauthorized location heartbeat")
			} else {
				i.logger.WithError(err).WithField("location_id", hb.LocationID).Warn("Could not verify location heartbeat")
			}
			return
		}
		signature := hb.Signature
		hb.Signature = ""
		if err := locationauth.VerifyJSON(credential, signature, hb); err != nil {
			i.logger.WithError(err).WithField("location_id", hb.LocationID).Debug("Ignoring unsigned location heartbeat")
			return
		}
		// Checked after the signature, so only a real location's beats can
		// produce the warning (and the per-location map stays bounded by the
		// locations that exist).
		if skew := heartbeatSkew(hb.Timestamp, time.Now()); skew > heartbeatMaxSkew {
			mu.Lock()
			warn := time.Since(lastSkewWarn[hb.LocationID]) >= heartbeatSkewWarnEvery
			if warn {
				lastSkewWarn[hb.LocationID] = time.Now()
			}
			mu.Unlock()
			if warn {
				i.logger.WithFields(map[string]interface{}{
					"location_id": hb.LocationID, "hostname": hb.Hostname,
					"skew_seconds": int(skew.Seconds()), "max_skew_seconds": int(heartbeatMaxSkew.Seconds()),
				}).Warn("Dropping location heartbeats: the worker's clock is too far from the scheduler's; the location will read offline until its clock is synced (NTP)")
			}
			return
		}

		mu.Lock()
		if last, ok := lastWrite[hb.LocationID]; ok && time.Since(last) < heartbeatWriteThrottle {
			mu.Unlock()
			return
		}
		lastWrite[hb.LocationID] = time.Now()
		mu.Unlock()

		if _, err := i.db.ExecContext(ctx, `
			UPDATE locations
			SET last_seen_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL
		`, locationID); err != nil {
			i.logger.WithError(err).Warn("Failed to record location heartbeat")
		}
	})
}
