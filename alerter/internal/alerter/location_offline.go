package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// offlineLocation is one location_offline alert's subject.
type offlineLocation struct {
	alertID      uuid.UUID
	tenantID     uuid.UUID
	locationID   uuid.UUID
	name         string
	lastSeenAt   sql.NullTime
	monitorCount int
	triggeredAt  time.Time
	// resolveReason is set on resolve (notifications.ResolveReason*).
	resolveReason string
}

// label is the readable subject baked into MonitorName, so every channel
// plugin and template renders the alert meaningfully.
func (l offlineLocation) label() string {
	return "location: " + l.name
}

func (l offlineLocation) record(triggeredAt time.Time) alertRecord {
	locationID, name := l.locationID, l.name
	record := alertRecord{
		ID: l.alertID, TenantID: l.tenantID,
		Kind: notifications.KindLocationOffline, TriggeredAt: triggeredAt,
		SourceLocationID:     &locationID,
		SourceLocationName:   &name,
		LocationMonitorCount: l.monitorCount,
		ResolveReason:        l.resolveReason,
	}
	if l.lastSeenAt.Valid {
		seen := l.lastSeenAt.Time
		record.LocationLastSeenAt = &seen
	}
	return record
}

func (l offlineLocation) binding() policyBinding {
	return policyBinding{TenantID: l.tenantID, MonitorName: l.label()}
}

// locationMonitorCountExpr counts the live monitors that run from the
// location aliased by locAlias: the blast radius an offline alert reports.
func locationMonitorCountExpr(locAlias string) string {
	return `(SELECT COUNT(*) FROM monitor_locations ml
		JOIN monitors m ON m.id = ml.monitor_id AND m.deleted_at IS NULL AND m.enabled
		WHERE ml.location_id = ` + locAlias + `.id)`
}

// evaluateLocationOffline runs the location_offline lifecycle (rule S-F6):
// open one alert per offline location, resolve it once the location is seen
// again, disabled, deleted, or the tenant turned these alerts off, and
// dispatch tiers and reminders to the tenant default routes. A location has
// no monitor, so like mesh edges it routes only through
// alertrouting.TenantTargetsSQL. It runs before evaluateMeshEdges, whose
// dispatch skips edges with an offline endpoint.
func (a *Alerter) evaluateLocationOffline(ctx context.Context) error {
	if err := a.openLocationOfflineAlerts(ctx); err != nil {
		return err
	}
	if err := a.resolveLocationOfflineAlerts(ctx); err != nil {
		return err
	}
	if err := a.dispatchOpenLocationOfflineAlerts(ctx); err != nil {
		return err
	}
	a.updateFleetGauges(ctx)
	return nil
}

// fleetSilenceMinLocations is how many recently-seen locations must exist
// platform-wide before "every one of them is silent" reads as a platform
// fault rather than a dead site: with one location the two look the same,
// and a single alert is no storm.
const fleetSilenceMinLocations = 2

// openLocationOfflineAlerts opens an alert for each offline location seen
// within monitorstate.LocationOfflineAlertHorizon (so an upgrade never pages
// for long-abandoned locations). It opens none while the whole fleet is
// silent — at least fleetSilenceMinLocations recently-seen locations and
// none of them seen within the tenant threshold: heartbeats reach
// last_seen_at through the broker and the scheduler's ingest, so that is
// the signature of a platform fault, which would otherwise page for every
// location at once.
func (a *Alerter) openLocationOfflineAlerts(ctx context.Context) error {
	var fleetNewest sql.NullTime
	var fleetSize int
	if err := a.db.QueryRowContext(ctx, `
		SELECT MAX(l.last_seen_at), COUNT(*) FROM locations l
		WHERE l.enabled AND l.deleted_at IS NULL
		  AND `+monitorstate.LocationWithinOfflineHorizonPredicate("l")).Scan(&fleetNewest, &fleetSize); err != nil {
		return fmt.Errorf("query fleet heartbeat: %w", err)
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT l.tenant_id, l.id, l.name, l.last_seen_at, `+locationMonitorCountExpr("l")+`,
			$2::int >= $3::int AND COALESCE($1::timestamptz <= NOW() - make_interval(secs => GREATEST(te.location_offline_after_seconds, $4::int)), TRUE)
		FROM locations l
		JOIN tenants te ON te.id = l.tenant_id
		WHERE te.location_offline_alerts_enabled
		  AND `+monitorstate.LocationOfflinePredicate("l", "te")+`
		  AND `+monitorstate.LocationWithinOfflineHorizonPredicate("l")+`
		  AND NOT EXISTS (
			SELECT 1 FROM alerts al
			WHERE al.kind = 'location_offline'
			  AND al.status IN ('active', 'acknowledged')
			  AND al.source_location_id = l.id)
	`, fleetNewest, fleetSize, fleetSilenceMinLocations, monitorstate.MinLocationOfflineAfterSeconds)
	if err != nil {
		return fmt.Errorf("query offline locations: %w", err)
	}
	defer rows.Close()

	var offline []offlineLocation
	held := 0
	for rows.Next() {
		var l offlineLocation
		var fleetSilent bool
		if err := rows.Scan(&l.tenantID, &l.locationID, &l.name, &l.lastSeenAt, &l.monitorCount, &fleetSilent); err != nil {
			return fmt.Errorf("scan offline location: %w", err)
		}
		if fleetSilent {
			held++
			continue
		}
		offline = append(offline, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if held > 0 {
		a.logger.WithFields(map[string]interface{}{
			"locations": held, "fleet_size": fleetSize, "newest_heartbeat": fleetNewest.Time,
		}).Warn("No location heartbeat reached the platform within the offline threshold; not opening location offline alerts (heartbeat ingest or broker fault?)")
	}

	for _, l := range offline {
		err := a.db.QueryRowContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
				triggered_at, failure_count, source_location_id, created_at, updated_at)
			VALUES ($1, $2, NULL, NULL, 'location_offline', 'active', NOW(), 0, $3, NOW(), NOW())
			ON CONFLICT (source_location_id)
				WHERE kind = 'location_offline' AND status IN ('active', 'acknowledged') DO NOTHING
			RETURNING id
		`, uuid.New(), l.tenantID, l.locationID).Scan(&l.alertID)
		if errors.Is(err, sql.ErrNoRows) {
			continue // another replica won the race
		}
		if err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{"location_id": l.locationID}).Error("Failed to open location offline alert")
			continue
		}
		record := l.record(time.Now())
		a.publishAlertEvent(ctx, "created", l.binding(), &record, nil)
	}
	return nil
}

func (a *Alerter) resolveLocationOfflineAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, COALESCE(l.name, 'unknown'),
			l.last_seen_at, al.triggered_at,
			CASE
				WHEN l.id IS NULL OR l.deleted_at IS NOT NULL THEN '`+notifications.ResolveReasonLocationDeleted+`'
				WHEN NOT l.enabled THEN '`+notifications.ResolveReasonLocationDisabled+`'
				WHEN NOT `+monitorstate.LocationOfflinePredicate("l", "te")+` THEN '`+notifications.ResolveReasonRecovered+`'
				ELSE '`+notifications.ResolveReasonAlertsDisabled+`'
			END
		FROM alerts al
		JOIN tenants te ON te.id = al.tenant_id
		LEFT JOIN locations l ON l.id = al.source_location_id
		WHERE al.kind = 'location_offline'
		  AND al.status IN ('active', 'acknowledged')
		  AND (l.id IS NULL
			OR NOT te.location_offline_alerts_enabled
			OR NOT `+monitorstate.LocationOfflinePredicate("l", "te")+`)
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
// for open location_offline alerts, through dueEvent and its ack/snooze gate
// like every other open-alert loop.
func (a *Alerter) dispatchOpenLocationOfflineAlerts(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.source_location_id, l.name, l.last_seen_at,
			`+locationMonitorCountExpr("l")+`,
			al.triggered_at, te.alert_reminder_seconds,
			`+alertrouting.DispatchEligibleSinceExpr("al", "te")+`,
			al.status, `+alertrouting.AckStopsExpr("te")+`, al.snoozed_until, al.unacknowledged_at
		FROM alerts al
		JOIN tenants te ON te.id = al.tenant_id
		JOIN locations l ON l.id = al.source_location_id
		WHERE al.kind = 'location_offline'
		  AND al.status IN ('active', 'acknowledged')
		  AND te.location_offline_alerts_enabled
		  AND `+monitorstate.LocationOfflinePredicate("l", "te")+`
		  AND NOT `+alertrouting.SnoozedPredicate("al")+`
	`)
	if err != nil {
		return fmt.Errorf("query open location offline alerts: %w", err)
	}
	defer rows.Close()

	type openAlert struct {
		loc             offlineLocation
		reminderSeconds int
		eligibleSince   time.Time
		gate            DispatchGate
	}
	var open []openAlert
	for rows.Next() {
		var oa openAlert
		var status string
		var snoozedUntil, unacknowledgedAt sql.NullTime
		if err := rows.Scan(&oa.loc.alertID, &oa.loc.tenantID, &oa.loc.locationID, &oa.loc.name, &oa.loc.lastSeenAt,
			&oa.loc.monitorCount, &oa.loc.triggeredAt, &oa.reminderSeconds, &oa.eligibleSince,
			&status, &oa.gate.AckStops, &snoozedUntil, &unacknowledgedAt); err != nil {
			return fmt.Errorf("scan open location offline alert: %w", err)
		}
		oa.gate.Acknowledged = status == "acknowledged"
		oa.gate.SnoozedUntil, oa.gate.UnacknowledgedAt = snoozedUntil.Time, unacknowledgedAt.Time
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
		alertAge := now.Sub(oa.eligibleSince)
		reminderInterval := time.Duration(oa.reminderSeconds) * time.Second
		for _, target := range targets {
			state := getNotificationState(states, oa.loc.alertID, target.channel.ID)
			if eventType, ok := dueEvent(now, state, target, alertAge, reminderInterval, oa.gate); ok {
				a.fireChannel(ctx, eventType, binding, &record, target.channel, now, reminderInterval, oa.gate.stopOnAck(target))
			}
		}
	}
	return nil
}

// alertKindsForGauge are the kinds probara_alerter_open_alerts always
// reports, so a kind that drops to zero open alerts reads 0, not stale.
var alertKindsForGauge = []string{
	notifications.KindAvailability, notifications.KindLatencyAnomaly, notifications.KindHostMetric,
	notifications.KindMeshEdge, notifications.KindTLSExpiry, notifications.KindDomainExpiry,
	notifications.KindSLOBurn, notifications.KindLocationOffline, notifications.KindCheckWarning,
}

// updateFleetGauges refreshes the fleet gauges: offline locations (S-F6,
// whether or not their tenant alerts on them) and open alerts by kind.
// Advisory: a failure is logged, never fails the tick.
func (a *Alerter) updateFleetGauges(ctx context.Context) {
	if a.locationsOffline == nil || a.openAlerts == nil {
		return
	}
	var offline int
	if err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM locations l JOIN tenants te ON te.id = l.tenant_id
		WHERE `+monitorstate.LocationOfflinePredicate("l", "te")).Scan(&offline); err != nil {
		a.logger.WithError(err).Warn("Failed to count offline locations")
	} else {
		a.locationsOffline.With(prometheus.Labels{}).Set(float64(offline))
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT kind, COUNT(*) FROM alerts WHERE status IN ('active', 'acknowledged') GROUP BY kind`)
	if err != nil {
		a.logger.WithError(err).Warn("Failed to count open alerts")
		return
	}
	defer rows.Close()
	counts := map[string]float64{}
	for rows.Next() {
		var kind string
		var n float64
		if err := rows.Scan(&kind, &n); err != nil {
			a.logger.WithError(err).Warn("Failed to scan open alert count")
			return
		}
		counts[kind] = n
	}
	if rows.Err() != nil {
		return
	}
	for _, kind := range alertKindsForGauge {
		a.openAlerts.With(prometheus.Labels{"kind": kind}).Set(counts[kind])
	}
}
