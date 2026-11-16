package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/incidentrecovery"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// alertSubject is what an alert is about: the monitor (or, for mesh edges, just
// the tenant) it fired for, plus whether firing may open an auto incident.
type alertSubject struct {
	MonitorID            uuid.UUID
	TenantID             uuid.UUID
	MonitorName          string
	MonitorType          string
	CreateIncidentOnFire bool
}

type alertRecord struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	MonitorID            uuid.UUID
	Kind                 string
	Status               string
	TriggeredAt          time.Time
	ResolvedAt           *time.Time
	FailureCount         int
	LastError            *string
	RootCauseMonitorID   *uuid.UUID
	RootCauseMonitorName *string
	RootCauseDownSince   *time.Time
	FailingLocations     []notifications.FailingLocation
	ImpactedMonitors     []notifications.ImpactedMonitor
	ImpactedCount        int
	BaselineLatencyMs    *float64
	ObservedLatencyMs    *float64
	AnomalyScore         *float64
	MetricName           *string
	MetricValue          *float64
	ThresholdValue       *float64
	// Mesh-edge subject (Kind == "mesh_edge"): MonitorID is the zero UUID.
	SourceLocationID   *uuid.UUID
	SourceLocationName *string
	TargetLocationID   *uuid.UUID
	TargetLocationName *string
	// Offline-location annotation (Kind == "location_offline"); the location
	// itself rides in SourceLocationID/Name and MonitorID is the zero UUID.
	LocationLastSeenAt       *time.Time
	LocationMonitorCount     int
	LocationOnlyMonitorCount int
	// ResolveReason is alerts.resolve_reason (notifications.ResolveReason*).
	ResolveReason string
	// SuspectedCause is set only for the "suspected_cause" follow-up.
	SuspectedCause *notifications.SuspectedCause
	// Relapse annotation (notifications.AlertDetails): set by a reopen, and
	// for every send by deliverNotification from the row it locks.
	RelapseCount    int
	ReopenedAt      *time.Time
	LastRecoveredAt *time.Time
	// Per delivery, set by deliverNotification from its claim
	// (notifications.AlertDetails documents them).
	RelapseNotice        bool
	PagedRelapse         int
	PreviousPagedRelapse *int
}

type alertChannel struct {
	ID       uuid.UUID
	Name     string
	Type     string
	Config   json.RawMessage
	IsActive bool
}

type notificationState struct {
	LastSentAt           time.Time
	LastEventType        string
	PagedRelapse         int
	AcknowledgedSentAt   *time.Time
	SuspectedCauseSentAt *time.Time
}

type groupDetail struct {
	Failures   []groupFailure
	ExtraCount int
}

type groupFailure struct {
	Name      string
	Status    string
	Error     *string
	CreatedAt time.Time
}

func (a *Alerter) loadNotificationStates(ctx context.Context, alertIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]*notificationState, error) {
	states := make(map[uuid.UUID]map[uuid.UUID]*notificationState)
	if len(alertIDs) == 0 {
		return states, nil
	}

	query := `
		SELECT alert_id, channel_id, last_sent_at, last_event_type, acknowledged_sent_at, suspected_cause_sent_at, paged_relapse
		FROM alert_notification_states
		WHERE alert_id = ANY($1)
	`

	rows, err := a.db.QueryContext(ctx, query, pq.Array(alertIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load notification states: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var alertID uuid.UUID
		var channelID uuid.UUID
		var state notificationState
		if err := rows.Scan(&alertID, &channelID, &state.LastSentAt, &state.LastEventType, &state.AcknowledgedSentAt, &state.SuspectedCauseSentAt, &state.PagedRelapse); err != nil {
			return nil, fmt.Errorf("failed to scan notification state: %w", err)
		}
		if _, ok := states[alertID]; !ok {
			states[alertID] = make(map[uuid.UUID]*notificationState)
		}
		states[alertID][channelID] = &state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating notification states: %w", err)
	}
	return states, nil
}

// errAlertAlreadyResolved is returned by resolveAlert when the alert was no
// longer open — normally because a sibling alerter replica resolved it a moment
// earlier. The guarded UPDATE makes exactly one replica win; the loser must
// neither publish nor notify, and this is not an error worth logging.
var errAlertAlreadyResolved = errors.New("alert already resolved")

// resolveReason says why the alerter resolves an alert, which decides
// whether a relapse may reopen it (relapse.go).
type resolveReason bool

const (
	// resolvedRecovered: the alert's condition cleared (the monitor came
	// back up, the metric or latency returned to normal, the certificate or
	// domain was renewed, the mesh edge recovered, the location reports
	// again). Sets auto_resolved, so the subject failing again within the
	// tenant's reopen cooldown reopens the alert.
	resolvedRecovered resolveReason = true
	// resolvedOutOfScope: the alerter closes the alert because its subject
	// left evaluation (monitor paused, disabled or deleted, rule or
	// threshold removed, readings gone stale, mesh edge or location gone,
	// group emptied or paused), or because the alert's form changed (a
	// domain alert switching between "expiry unknown" and dated). Not a
	// recovery: the subject coming back opens a new alert.
	resolvedOutOfScope resolveReason = false
)

// resolveAlert is the alerter's own resolve; reason records whether the
// condition cleared (auto_resolved). A manual resolve in the API leaves
// auto_resolved FALSE, so it never reopens either.
func (a *Alerter) resolveAlert(ctx context.Context, alertID uuid.UUID, now time.Time, reason resolveReason) (time.Time, error) {
	return a.resolveAlertWithReason(ctx, alertID, now, reason, "")
}

// resolveAlertWithReason is resolveAlert also recording why the alert closed
// in alerts.resolve_reason (notifications.ResolveReason*; "" leaves it NULL),
// the wording the closing notification uses.
func (a *Alerter) resolveAlertWithReason(ctx context.Context, alertID uuid.UUID, now time.Time, reason resolveReason, recorded string) (time.Time, error) {
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, updated_at = $1,
			auto_resolved = $3, resolve_reason = NULLIF($4, '')
		WHERE id = $2 AND status IN ('active', 'acknowledged')
		RETURNING resolved_at
	`

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var resolvedAt time.Time
	if err := tx.QueryRowContext(ctx, query, now, alertID, bool(reason), recorded).Scan(&resolvedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, errAlertAlreadyResolved
		}
		return time.Time{}, fmt.Errorf("failed to resolve alert: %w", err)
	}

	if err := incidentrecovery.RecordForAlertTx(ctx, tx, alertID); err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("commit alert transaction: %w", err)
	}

	return resolvedAt, nil
}

func (a *Alerter) publishAlertEvent(ctx context.Context, eventType string, binding alertSubject, alert *alertRecord, resolvedAt *time.Time) {
	if a.nats == nil {
		return
	}

	event := buildAlertEvent(eventType, binding, alert, resolvedAt, time.Now())

	subject := fmt.Sprintf("%s.%s", a.config.AlertSubject, eventType)
	if err := a.nats.PublishJSON(ctx, subject, event, nil); err != nil {
		a.logger.WithError(err).Warn("Failed to publish alert event")
	}
}

func (a *Alerter) sendChannelNotification(
	ctx context.Context,
	channel alertChannel,
	eventType string,
	binding alertSubject,
	alert *alertRecord,
	groupInfo *groupDetail,
	now time.Time,
) error {
	_ = groupInfo // group context plumbing follows in a later refactor

	p, ok := plugin.DefaultRegistry.Get(channel.Type)
	if !ok {
		return fmt.Errorf("unsupported alert channel type: %s", channel.Type)
	}

	var resolvedAt *time.Time
	if eventType == "resolved" {
		resolvedAt = &now
		if alert.ResolvedAt != nil {
			resolvedAt = alert.ResolvedAt
		}
	}
	event := buildAlertEvent(eventType, binding, alert, resolvedAt, now)
	deliveryKey := recipientKey(ctx, eventType, now)

	if a.config.AsyncDispatch && a.nats != nil {
		envelope := notifications.DispatchEnvelope{
			V:           1,
			ChannelID:   channel.ID.String(),
			ChannelType: channel.Type,
			AlertID:     alert.ID.String(),
			EventType:   eventType,
			Event:       event,
			DeliveryKey: deliveryKey,
		}
		subject := fmt.Sprintf("alerts.dispatch.%s", channel.Type)
		// One message per claimed occurrence (dispatchRound): a retry of a
		// claim that never committed republishes the same id, and JetStream's
		// duplicate reply then means "already queued", which is success.
		messageID := dispatchMessageID(ctx, envelope.IdempotencyKey(), eventType, now)
		headers := map[string][]string{
			"x-idempotency-key": {envelope.IdempotencyKey()},
			"Nats-Msg-Id":       {messageID},
		}
		traceQueued(ctx) // the delivery worker records this send
		return a.nats.PublishJSON(ctx, subject, envelope, headers)
	}

	configMap := map[string]any{}
	if len(channel.Config) > 0 {
		if err := json.Unmarshal(channel.Config, &configMap); err != nil {
			return fmt.Errorf("decode channel config: %w", err)
		}
	}
	configMap, err := secrets.DecryptConfig(a.encryptor, p.Manifest(), configMap)
	if err != nil {
		return fmt.Errorf("decrypt channel config: %w", err)
	}

	var recipients plugin.RecipientLedger
	if a.db != nil {
		// The pool, not deliverNotification's claim transaction: a failed
		// send rolls that back, and the progress must survive it.
		recipients = recipientledger.For(a.db, alert.ID, channel.ID, deliveryKey)
	}
	recipients = traceSend(ctx, recipients, p.Manifest(), configMap) // delivery history
	return p.Send(ctx, plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID:     channel.ID.String(),
			Name:   channel.Name,
			Config: configMap,
		},
		Event:      event,
		EventType:  eventType,
		Attempt:    1,
		Recipients: recipients,
	})
}

func getNotificationState(states map[uuid.UUID]map[uuid.UUID]*notificationState, alertID, channelID uuid.UUID) *notificationState {
	if alertStates, ok := states[alertID]; ok {
		return alertStates[channelID]
	}
	return nil
}

func buildAlertEvent(eventType string, binding alertSubject, alert *alertRecord, resolvedAt *time.Time, timestamp time.Time) notifications.AlertEvent {
	status := "active"
	switch eventType {
	case "resolved":
		status = "resolved"
	case "acknowledged":
		status = "acknowledged"
	case "suspected_cause":
		// An advisory follow-up keeps the alert's own status (active or
		// acknowledged); receivers tell it apart by the event type.
		if alert.Status != "" {
			status = alert.Status
		}
	}

	var rootCauseID *string
	if alert.RootCauseMonitorID != nil {
		id := alert.RootCauseMonitorID.String()
		rootCauseID = &id
	}

	kind := alert.Kind
	if kind == "" {
		kind = notifications.KindAvailability
	}

	uuidStr := func(id *uuid.UUID) *string {
		if id == nil {
			return nil
		}
		s := id.String()
		return &s
	}

	return notifications.AlertEvent{
		Type:      eventType,
		TenantID:  binding.TenantID.String(),
		Timestamp: timestamp,
		Alert: notifications.AlertDetails{
			ID:                   alert.ID.String(),
			MonitorID:            binding.MonitorID.String(),
			MonitorName:          binding.MonitorName,
			Kind:                 kind,
			Status:               status,
			TriggeredAt:          alert.TriggeredAt,
			ResolvedAt:           resolvedAt,
			FailureCount:         alert.FailureCount,
			LastError:            alert.LastError,
			RootCauseMonitorID:   rootCauseID,
			RootCauseMonitorName: alert.RootCauseMonitorName,
			RootCauseDownSince:   alert.RootCauseDownSince,
			FailingLocations:     alert.FailingLocations,
			ImpactedMonitors:     alert.ImpactedMonitors,
			ImpactedCount:        alert.ImpactedCount,
			BaselineLatencyMs:    alert.BaselineLatencyMs,
			ObservedLatencyMs:    alert.ObservedLatencyMs,
			AnomalyScore:         alert.AnomalyScore,
			MetricName:           alert.MetricName,
			MetricValue:          alert.MetricValue,
			ThresholdValue:       alert.ThresholdValue,
			SourceLocationID:     uuidStr(alert.SourceLocationID),
			SourceLocationName:   alert.SourceLocationName,
			TargetLocationID:     uuidStr(alert.TargetLocationID),
			TargetLocationName:   alert.TargetLocationName,
			SuspectedCause:       alert.SuspectedCause,
			RelapseCount:         alert.RelapseCount,
			ReopenedAt:           alert.ReopenedAt,
			LastRecoveredAt:      alert.LastRecoveredAt,
			RelapseNotice:        alert.RelapseNotice,
			PagedRelapse:         alert.PagedRelapse,
			PreviousPagedRelapse: alert.PreviousPagedRelapse,

			LocationLastSeenAt:       alert.LocationLastSeenAt,
			LocationMonitorCount:     alert.LocationMonitorCount,
			LocationOnlyMonitorCount: alert.LocationOnlyMonitorCount,
			ResolveReason:            alert.ResolveReason,
		},
	}
}
