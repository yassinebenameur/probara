package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// offlineLocation is one location_offline alert's subject.
type offlineLocation struct {
	alertID          uuid.UUID
	tenantID         uuid.UUID
	locationID       uuid.UUID
	name             string
	lastSeenAt       sql.NullTime
	monitorCount     int
	onlyMonitorCount int
	triggeredAt      time.Time
	resolveReason    string
}

func (l offlineLocation) record(triggeredAt time.Time) alertRecord {
	locationID, name := l.locationID, l.name
	record := alertRecord{
		ID: l.alertID, TenantID: l.tenantID,
		Kind: notifications.KindLocationOffline, TriggeredAt: triggeredAt,
		SourceLocationID:         &locationID,
		SourceLocationName:       &name,
		LocationMonitorCount:     l.monitorCount,
		LocationOnlyMonitorCount: l.onlyMonitorCount,
		ResolveReason:            l.resolveReason,
	}
	if l.lastSeenAt.Valid {
		seen := l.lastSeenAt.Time
		record.LocationLastSeenAt = &seen
	}
	return record
}

// binding names the location as the subject, so a template or receiver that
// only reads monitor_name still shows what went offline (present.Build titles
// it "Location Offline: <name>").
func (l offlineLocation) binding() alertSubject {
	return alertSubject{TenantID: l.tenantID, MonitorName: l.name}
}

// locationMonitorCountsSQL selects two columns for the location aliased by
// locAlias: the live, enabled monitors that run from it, and how many of
// those have no other enabled location, so they get no fresh checks at all
// while it is offline (they go unknown, S-F2).
func locationMonitorCountsSQL(locAlias string) string {
	runsHere := `FROM monitor_locations lml
		JOIN monitors lm ON lm.id = lml.monitor_id AND lm.deleted_at IS NULL AND lm.enabled
		WHERE lml.location_id = ` + locAlias + `.id`
	return `(SELECT COUNT(*) ` + runsHere + `),
		(SELECT COUNT(*) ` + runsHere + `
			AND NOT EXISTS (
				SELECT 1 FROM monitor_locations oml
				JOIN locations ol ON ol.id = oml.location_id AND ol.enabled AND ol.deleted_at IS NULL
				WHERE oml.monitor_id = lml.monitor_id AND oml.location_id <> lml.location_id))`
}

// fleetSilenceMinLocations is how many locations seen within the alert
// horizon must exist platform-wide before "none of them reports" reads as a
// platform fault rather than dead sites. With one location the two look the
// same, and a single alert is no storm.
const fleetSilenceMinLocations = 2

// locationFleet is the platform-wide liveness tally one tick reads.
type locationFleet struct {
	online, offline, neverSeen, disabled int
	// recent counts enabled locations seen within the alert horizon.
	recent int
	// heartbeatSinceStart: some location's heartbeat landed after this
	// process started, so the heartbeat ingest is writing right now.
	heartbeatSinceStart bool
}

// fleetSilent: several recently-seen locations and none of them online.
// Heartbeats reach last_seen_at through the broker and the scheduler's
// ingest, so every location going quiet at once is that path failing (or
// the database lagging behind it), not every site dying together.
func (f locationFleet) fleetSilent() bool {
	return f.recent >= fleetSilenceMinLocations && f.online == 0
}

// evaluateLocationOffline runs the location_offline lifecycle (rule S-F6):
// open one alert per offline location, resolve it once the location reports
// again or is disabled or deleted, and dispatch to the tenant default
// channels through deliverNotification. It runs before evaluateMeshEdges, so
// a location's alert opens in the same tick its mesh edges go quiet
// (alertrouting.SuppressedByOfflineLocationPredicate).
func (a *Alerter) evaluateLocationOffline(ctx context.Context) error {
	fleet, err := a.loadLocationFleet(ctx)
	if err != nil {
		return err
	}
	a.setFleetGauges(fleet)
	if err := a.openLocationOfflineAlerts(ctx, fleet); err != nil {
		return err
	}
	if err := a.resolveLocationOfflineAlerts(ctx); err != nil {
		return err
	}
	return a.dispatchOpenLocationOfflineAlerts(ctx)
}

func (a *Alerter) loadLocationFleet(ctx context.Context) (locationFleet, error) {
	var f locationFleet
	offline := monitorstate.LocationOfflinePredicate("l")
	if err := a.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE l.enabled AND l.last_seen_at IS NOT NULL AND NOT `+offline+`),
			COUNT(*) FILTER (WHERE `+offline+`),
			COUNT(*) FILTER (WHERE l.enabled AND l.last_seen_at IS NULL),
			COUNT(*) FILTER (WHERE NOT l.enabled),
			COUNT(*) FILTER (WHERE l.enabled AND `+monitorstate.LocationWithinOfflineHorizonPredicate("l")+`),
			COALESCE(BOOL_OR(l.enabled AND l.last_seen_at > $1), FALSE)
		FROM locations l
		WHERE l.deleted_at IS NULL
	`, a.startedAt).Scan(&f.online, &f.offline, &f.neverSeen, &f.disabled, &f.recent, &f.heartbeatSinceStart); err != nil {
		return locationFleet{}, fmt.Errorf("query location fleet: %w", err)
	}
	return f, nil
}

// locationOfflineHold says whether new location_offline alerts must wait,
// and why: fleet_silent (see locationFleet.fleetSilent), or startup. An
// alerter that just started may be judging timestamps the heartbeat ingest
// has not refreshed yet (the scheduler restarting in the same rollout), so
// it waits until a heartbeat lands after its start, or one full offline
// threshold has passed. Resolution and dispatch of open alerts never wait.
func (a *Alerter) locationOfflineHold(f locationFleet) string {
	if f.fleetSilent() {
		return "fleet_silent"
	}
	if !a.startedAt.IsZero() && time.Since(a.startedAt) < monitorstate.LocationOfflineAfter && !f.heartbeatSinceStart {
		return "startup"
	}
	return ""
}

func (a *Alerter) setFleetGauges(f locationFleet) {
	if a.locationsGauge != nil {
		a.locationsGauge.With(prometheus.Labels{"state": "online"}).Set(float64(f.online))
		a.locationsGauge.With(prometheus.Labels{"state": "offline"}).Set(float64(f.offline))
		a.locationsGauge.With(prometheus.Labels{"state": "never_seen"}).Set(float64(f.neverSeen))
		a.locationsGauge.With(prometheus.Labels{"state": "disabled"}).Set(float64(f.disabled))
	}
	if a.locationGuardGauge != nil {
		hold := a.locationOfflineHold(f)
		for _, reason := range []string{"fleet_silent", "startup"} {
			v := 0.0
			if hold == reason {
				v = 1
			}
			a.locationGuardGauge.With(prometheus.Labels{"reason": reason}).Set(v)
		}
	}
}

// openLocationOfflineAlerts opens an alert for each offline location seen
// within monitorstate.LocationOfflineAlertHorizon that has none open, unless
// locationOfflineHold says to wait.
func (a *Alerter) openLocationOfflineAlerts(ctx context.Context, fleet locationFleet) error {
	if hold := a.locationOfflineHold(fleet); hold != "" {
		if fleet.offline > 0 && hold != a.lastLocationHold {
			a.logger.WithFields(map[string]interface{}{
				"reason": hold, "offline_locations": fleet.offline, "recent_locations": fleet.recent,
			}).Warn("Holding location offline alerts: no location heartbeat can be trusted right now (heartbeat ingest, broker or database lag, or alerter startup)")
		}
		a.lastLocationHold = hold
		return nil
	}
	a.lastLocationHold = ""

	rows, err := a.db.QueryContext(ctx, `
		SELECT l.tenant_id, l.id, l.name, l.last_seen_at, `+locationMonitorCountsSQL("l")+`
		FROM locations l
		WHERE `+monitorstate.LocationOfflinePredicate("l")+`
		  AND `+monitorstate.LocationWithinOfflineHorizonPredicate("l")+`
		  AND NOT EXISTS (
			SELECT 1 FROM alerts al
			WHERE al.kind = 'location_offline'
			  AND al.status IN ('active', 'acknowledged')
			  AND al.source_location_id = l.id)
	`)
	if err != nil {
		return fmt.Errorf("query offline locations: %w", err)
	}
	defer rows.Close()

	var offline []offlineLocation
	for rows.Next() {
		var l offlineLocation
		if err := rows.Scan(&l.tenantID, &l.locationID, &l.name, &l.lastSeenAt, &l.monitorCount, &l.onlyMonitorCount); err != nil {
			return fmt.Errorf("scan offline location: %w", err)
		}
		offline = append(offline, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, l := range offline {
		if err := a.openLocationOfflineAlert(ctx, l); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"location_id": l.locationID}).Error("Failed to open location offline alert")
		}
	}
	return nil
}

// openLocationOfflineAlert inserts the alert only if the location is still
// offline at insert time (it may have reported since the candidate query),
// and only once across replicas: idx_alerts_one_open_location_offline makes
// the loser's INSERT a no-op.
func (a *Alerter) openLocationOfflineAlert(ctx context.Context, l offlineLocation) error {
	err := a.db.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status,
			triggered_at, failure_count, source_location_id, created_at, updated_at)
		SELECT $1, l.tenant_id, NULL, 'location_offline', 'active', NOW(), 0, l.id, NOW(), NOW()
		FROM locations l
		WHERE l.id = $2 AND `+monitorstate.LocationOfflinePredicate("l")+`
		ON CONFLICT (source_location_id)
			WHERE kind = 'location_offline' AND status IN ('active', 'acknowledged') DO NOTHING
		RETURNING id, triggered_at
	`, uuid.New(), l.locationID).Scan(&l.alertID, &l.triggeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // another replica won, or the location reported meanwhile
	}
	if err != nil {
		return fmt.Errorf("insert location offline alert: %w", err)
	}
	record := l.record(l.triggeredAt)
	a.publishAlertEvent(ctx, "created", l.binding(), &record, nil)
	return nil
}

// resolveLocationOfflineAlerts resolves open alerts whose location is no
// longer offline: it reports again, or was disabled or deleted (locations
// are soft-deleted, so the FK cascade never fires and resolving here is
// load-bearing). The reason is recorded so the closing notification does not
// claim a disabled location came back.
func (a *Alerter) resolveLocationOfflineAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, COALESCE(l.name, 'unknown'),
			l.last_seen_at, al.triggered_at,
			CASE
				WHEN l.id IS NULL OR l.deleted_at IS NOT NULL THEN '`+notifications.ResolveReasonLocationDeleted+`'
				WHEN NOT l.enabled THEN '`+notifications.ResolveReasonLocationDisabled+`'
				ELSE '`+notifications.ResolveReasonRecovered+`'
			END
		FROM alerts al
		LEFT JOIN locations l ON l.id = al.source_location_id
		WHERE al.kind = 'location_offline'
		  AND al.status IN ('active', 'acknowledged')
		  AND NOT COALESCE(`+monitorstate.LocationOfflinePredicate("l")+`, FALSE)
	`)
	if err != nil {
		return fmt.Errorf("query recovered locations: %w", err)
	}
	defer rows.Close()

	var recovered []offlineLocation
	for rows.Next() {
		var l offlineLocation
		if err := rows.Scan(&l.alertID, &l.tenantID, &l.locationID, &l.name, &l.lastSeenAt, &l.triggeredAt, &l.resolveReason); err != nil {
			return fmt.Errorf("scan recovered location: %w", err)
		}
		recovered = append(recovered, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, l := range recovered {
		resolvedAt, err := a.resolveAlertWithReason(ctx, l.alertID, time.Now(), l.resolveReason)
		if errors.Is(err, errAlertAlreadyResolved) {
			continue
		}
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": l.alertID}).Error("Failed to resolve location offline alert")
			continue
		}
		record := l.record(l.triggeredAt)
		a.publishAlertEvent(ctx, "resolved", l.binding(), &record, &resolvedAt)
		a.notifyFiredChannels(ctx, "resolved", l.binding(), &record)
	}
	return nil
}

// dispatchOpenLocationOfflineAlerts fires due escalation tiers and reminders
// for open location_offline alerts to the tenant default channels, with the
// same delay/reminder contract as mesh and availability alerts.
func (a *Alerter) dispatchOpenLocationOfflineAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, l.name, l.last_seen_at,
			`+locationMonitorCountsSQL("l")+`,
			al.triggered_at, te.alert_reminder_seconds
		FROM alerts al
		JOIN tenants te ON te.id = al.tenant_id
		JOIN locations l ON l.id = al.source_location_id
		WHERE al.kind = 'location_offline'
		  AND al.status IN ('active', 'acknowledged')
		  AND `+monitorstate.LocationOfflinePredicate("l")+`
	`)
	if err != nil {
		return fmt.Errorf("query open location offline alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		loc             offlineLocation
		reminderSeconds int
	}
	var open []openAlert
	for rows.Next() {
		var oa openAlert
		if err := rows.Scan(&oa.loc.alertID, &oa.loc.tenantID, &oa.loc.locationID, &oa.loc.name, &oa.loc.lastSeenAt,
			&oa.loc.monitorCount, &oa.loc.onlyMonitorCount, &oa.loc.triggeredAt, &oa.reminderSeconds); err != nil {
			return fmt.Errorf("scan open location offline alert: %w", err)
		}
		open = append(open, oa)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, oa := range open {
		targets, err := a.resolveTenantDefaultChannels(ctx, oa.loc.tenantID)
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.loc.alertID}).Error("Failed to resolve location offline alert channels")
			continue
		}
		states, err := a.loadNotificationStates(ctx, []uuid.UUID{oa.loc.alertID})
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": oa.loc.alertID}).Error("Failed to load location offline notification states")
			continue
		}
		record := oa.loc.record(oa.loc.triggeredAt)
		binding := oa.loc.binding()
		alertAge := now.Sub(oa.loc.triggeredAt)
		reminderInterval := time.Duration(oa.reminderSeconds) * time.Second
		for _, target := range targets {
			if !target.channel.IsActive {
				continue
			}
			state := getNotificationState(states, oa.loc.alertID, target.channel.ID)
			if event := openAlertEventDue(state, alertAge, target.delay, now, reminderInterval); event != "" {
				a.fireChannel(ctx, event, binding, &record, target.channel, now, reminderInterval)
			}
		}
	}
	return nil
}
