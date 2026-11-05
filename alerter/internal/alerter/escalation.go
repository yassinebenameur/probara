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

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/contacts"
	"github.com/yassinebenameur/probara/shared/escalation"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/notifications/present"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Escalation policies (migration 000113, package shared/escalation) page
// people. A policy's channel targets need nothing here: TargetsSQL emits
// them as ordinary routes, so dispatchOpenAlerts walks them with the
// alert_notification_states claim, reminders and the ack gate. This file
// pages the person targets:
//
//   - dispatchEscalations fires each due person step of an open alert once
//     per paging episode and repeat cycle. The claim is a row in
//     alert_escalation_states (INSERT ... ON CONFLICT DO NOTHING) taken
//     under the alert row's FOR SHARE lock and gated on status = 'active'
//     AND NOT snoozed, so an ack or a snooze stops the walk and nothing
//     fires twice across replicas. Unfired steps count from the alert's
//     dispatch-eligible time or, when later, the end of a snooze or the
//     last unacknowledge (alertrouting.ResumedAt, as on a stop-on-ack
//     route), so lifting a mute never releases a burst.
//   - Each step resolves its targets at fire time (escalation.ResolveUsers:
//     the user, whoever the schedule has on call, the workspace admins) and
//     sends to every deliverable verified contact: email through the
//     platform mailer, SMS through the workspace's personal SMS channel,
//     both as direct-recipient sends (plugin.CapabilityDirectRecipient; the
//     async envelope carries the ContactID only). alert_escalation_pages
//     records each contact reached under escalation.PageKey, so a step
//     rolled back after a transient failure skips them on its retry.
//   - dispatchEscalationNotices tells everyone a policy paged in the
//     alert's current episode that it was acknowledged (except whoever
//     acknowledged it) or resolved, once each.
//
// Every person send records one delivery-history row (deliverylog) and
// carries an acknowledge link scoped to its contact where the plugin
// renders one.

// escalationNoticeWindow bounds how long after resolving an alert its
// resolve notices are still retried.
const escalationNoticeWindow = 24 * time.Hour

// escalationAlert is one open alert walking a policy.
type escalationAlert struct {
	record        alertRecord
	binding       policyBinding
	policyID      uuid.UUID
	eligibleSince time.Time
	resumedAt     time.Time
}

// paging start: steps count from the later of the eligible time and the
// resume point.
func (e escalationAlert) start() time.Time {
	if e.resumedAt.After(e.eligibleSince) {
		return e.resumedAt
	}
	return e.eligibleSince
}

// dispatchEscalations fires the due person steps of every open, active,
// unsnoozed alert routed by an escalation policy. Its dispatchability is
// dispatchOpenAlerts' (maintenance, roll-up and dependency suppression
// included), narrowed to active alerts.
func (a *Alerter) dispatchEscalations(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.kind, al.triggered_at, al.failure_count, al.last_error,
			al.root_cause_monitor_id, al.root_cause_down_since, rcm.name,
			al.baseline_latency_ms, al.observed_latency_ms, al.anomaly_score,
			al.metric_name, al.metric_value, al.threshold_value, al.failing_locations,
			`+alertrouting.DispatchEligibleSinceExpr("al", "te")+`,
			al.snoozed_until, al.unacknowledged_at, al.relapse_count, al.reopened_at,
			`+alertrouting.EffectivePolicyExpr("m")+`
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id
		JOIN tenants te ON te.id = al.tenant_id
		LEFT JOIN monitors rcm ON rcm.id = al.root_cause_monitor_id
		WHERE `+openAlertDispatchablePredicate()+`
		  AND al.status = 'active'
		  AND EXISTS (
			SELECT 1 FROM escalation_policy_steps es
			JOIN escalation_policy_targets et ON et.step_id = es.id AND et.kind <> 'channel'
			WHERE es.policy_id = `+alertrouting.EffectivePolicyExpr("m")+`)
	`)
	if err != nil {
		return fmt.Errorf("query escalating alerts: %w", err)
	}
	var open []escalationAlert
	for rows.Next() {
		var e escalationAlert
		r := &e.record
		var lastError, rcName, metricName sql.NullString
		var rcID uuid.NullUUID
		var rcDownSince, snoozedUntil, unackAt sql.NullTime
		var baseline, observed, score, metricValue, thresholdValue sql.NullFloat64
		var failingLocations []byte
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MonitorID, &e.binding.MonitorName, &r.Kind, &r.TriggeredAt,
			&r.FailureCount, &lastError, &rcID, &rcDownSince, &rcName,
			&baseline, &observed, &score, &metricName, &metricValue, &thresholdValue, &failingLocations,
			&e.eligibleSince, &snoozedUntil, &unackAt, &r.RelapseCount, &r.ReopenedAt, &e.policyID); err != nil {
			rows.Close()
			return fmt.Errorf("scan escalating alert: %w", err)
		}
		if lastError.Valid {
			r.LastError = &lastError.String
		}
		r.Status = "active"
		r.FailingLocations = parseFailingLocations(failingLocations)
		setRootCause(r, rcID, rcName, rcDownSince)
		setLatencyMetrics(r, baseline, observed, score)
		setHostMetric(r, metricName, metricValue, thresholdValue)
		e.binding.MonitorID, e.binding.TenantID = r.MonitorID, r.TenantID
		// Person targets always stop on ack, so the unack restarts them.
		e.resumedAt = alertrouting.ResumedAt(snoozedUntil.Time, unackAt.Time, true)
		open = append(open, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read escalating alerts: %w", err)
	}
	if len(open) == 0 {
		return nil
	}

	policyIDs := make([]uuid.UUID, 0, len(open))
	alertIDs := make([]uuid.UUID, 0, len(open))
	for _, e := range open {
		policyIDs = append(policyIDs, e.policyID)
		alertIDs = append(alertIDs, e.record.ID)
	}
	policies, err := escalation.LoadMany(ctx, a.db, policyIDs)
	if err != nil {
		return err
	}
	claimed, err := a.loadEscalationClaims(ctx, alertIDs)
	if err != nil {
		return err
	}

	now := time.Now()
	for i := range open {
		e := &open[i]
		p, ok := policies[e.policyID]
		if !ok || p.TenantID != e.record.TenantID {
			continue
		}
		for _, k := range p.DueSteps(now.Sub(e.start()), claimed[e.record.ID][e.record.RelapseCount]) {
			if err := a.fireEscalationStep(ctx, e, p, k, now); err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{
					"alert_id": e.record.ID, "policy_id": p.ID, "step": k.Position, "cycle": k.Cycle,
				}).Warn("Failed to page an escalation step")
				// A failed step is retried next cycle; later steps wait for it.
				break
			}
		}
	}
	return nil
}

// loadEscalationClaims returns the claimed steps of alertIDs, by alert and
// paging episode.
func (a *Alerter) loadEscalationClaims(ctx context.Context, alertIDs []uuid.UUID) (map[uuid.UUID]map[int]map[escalation.StepKey]bool, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT alert_id, relapse_count, cycle, step_position
		FROM alert_escalation_states WHERE alert_id = ANY($1)`, pq.Array(alertIDs))
	if err != nil {
		return nil, fmt.Errorf("load escalation claims: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]map[int]map[escalation.StepKey]bool{}
	for rows.Next() {
		var id uuid.UUID
		var relapse int
		var k escalation.StepKey
		if err := rows.Scan(&id, &relapse, &k.Cycle, &k.Position); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[int]map[escalation.StepKey]bool{}
		}
		if out[id][relapse] == nil {
			out[id][relapse] = map[escalation.StepKey]bool{}
		}
		out[id][relapse][k] = true
	}
	return out, rows.Err()
}

// fireEscalationStep claims one step firing and pages its people. The
// claim and the sends run under the alert row's share lock, like
// deliverNotification: an ack, snooze, resolve or reopen committed before
// the claim wins, and one committed after waits for the sends. A transient
// failure rolls the claim back (returned as an error, so the step is
// retried next cycle) while the contacts already reached stay in the
// ledger; a permanent one (no SMS channel, rejected number) is logged and
// the step stays fired.
func (a *Alerter) fireEscalationStep(ctx context.Context, e *escalationAlert, p *escalation.Policy, k escalation.StepKey, now time.Time) error {
	step, ok := p.Step(k.Position)
	if !ok {
		return nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin escalation claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	var snoozed bool
	var relapse int
	if err := tx.QueryRowContext(ctx, `
		SELECT al.status, `+alertrouting.SnoozedPredicate("al")+`, al.relapse_count
		FROM alerts al WHERE al.id = $1 FOR SHARE`, e.record.ID).Scan(&status, &snoozed, &relapse); err != nil {
		return fmt.Errorf("lock escalating alert: %w", err)
	}
	if status != "active" || snoozed || relapse != e.record.RelapseCount {
		return nil
	}
	var claimedID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO alert_escalation_states (alert_id, relapse_count, cycle, step_position, policy_id, fired_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
		RETURNING alert_id`, e.record.ID, relapse, k.Cycle, k.Position, p.ID, now).Scan(&claimedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // another replica fired it
	}
	if err != nil {
		return fmt.Errorf("claim escalation step: %w", err)
	}

	users, err := escalation.ResolveUsers(ctx, a.db, e.record.TenantID, step, now)
	if err != nil {
		return err
	}
	targets, err := escalation.DeliverableContacts(ctx, a.db, a.secretsEncryptor(), e.record.TenantID, users)
	if err != nil {
		return err
	}
	fields := map[string]interface{}{"alert_id": e.record.ID, "policy_id": p.ID, "step": k.Position, "cycle": k.Cycle}
	if len(targets) == 0 {
		// An empty schedule slot or nobody with a verified contact: the step
		// pages nobody and the walk moves on to the next one.
		a.logger.WithFields(fields).Warn("Escalation step has nobody to page")
		return tx.Commit()
	}
	key := escalation.PageKey(k.Position, k.Cycle, relapse)
	reached, err := a.escalationLedger(ctx, e.record.ID, key)
	if err != nil {
		return err
	}
	var retry error
	for _, c := range targets {
		if reached[c.ID] {
			continue
		}
		err := a.sendPerson(ctx, personSend{contact: c, eventType: present.EventCreated, key: key, stopOnAck: true},
			e.binding, &e.record, now)
		if err == nil {
			a.recordEscalationPage(ctx, e.record.ID, c, relapse, escalation.EventPage, key)
			continue
		}
		a.logger.WithError(err).WithFields(fields).WithField("contact_id", c.ID).Warn("Failed to page a person")
		if !plugin.IsPermanent(err) {
			retry = err
		}
	}
	if retry != nil {
		return fmt.Errorf("escalation step will be retried: %w", retry)
	}
	return tx.Commit()
}

// escalationLedger returns the contacts already sent key for alertID.
func (a *Alerter) escalationLedger(ctx context.Context, alertID uuid.UUID, key string) (map[uuid.UUID]bool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT contact_id FROM alert_escalation_pages WHERE alert_id = $1 AND delivery_key = $2`, alertID, key)
	if err != nil {
		return nil, fmt.Errorf("load escalation ledger: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// recordEscalationPage notes that contact was sent key, through the pool:
// the step's claim may still roll back, and this progress must survive it.
func (a *Alerter) recordEscalationPage(ctx context.Context, alertID uuid.UUID, c contacts.Contact, relapse int, event, key string) {
	if _, err := a.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO alert_escalation_pages (alert_id, contact_id, user_id, relapse_count, event, delivery_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`, alertID, c.ID, c.UserID, relapse, event, key); err != nil {
		a.logger.WithError(err).WithField("alert_id", alertID).Warn("Failed to record an escalation page")
	}
}

// dispatchEscalationNotices sends the acknowledged and resolved notices to
// everyone a policy paged in the alert's current episode. Each is claimed in
// alert_escalation_pages (one per contact and episode) under the alert's
// share lock, and a transient failure rolls the claim back for the next
// cycle; resolve notices are retried for escalationNoticeWindow. The
// acknowledging user gets no acknowledged notice.
func (a *Alerter) dispatchEscalationNotices(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
		SELECT DISTINCT ON (p.alert_id, p.contact_id)
			p.alert_id, p.contact_id, al.status, al.relapse_count, al.tenant_id, al.monitor_id,
			COALESCE(m.name, ''), al.kind, al.triggered_at, al.resolved_at, al.failure_count, al.last_error,
			COALESCE(al.resolve_reason, '')
		FROM alert_escalation_pages p
		JOIN alerts al ON al.id = p.alert_id
		LEFT JOIN monitors m ON m.id = al.monitor_id
		WHERE p.event = 'page' AND p.relapse_count = al.relapse_count
		  AND ((al.status = 'resolved' AND al.resolved_at > NOW() - make_interval(secs => $1))
		    OR (al.status = 'acknowledged' AND p.user_id IS DISTINCT FROM al.acknowledged_by_user_id))
		  AND NOT EXISTS (
			SELECT 1 FROM alert_escalation_pages n
			WHERE n.alert_id = p.alert_id AND n.contact_id = p.contact_id
			  AND n.event = al.status::text AND n.relapse_count = al.relapse_count)
		ORDER BY p.alert_id, p.contact_id
		LIMIT 200`, escalationNoticeWindow.Seconds())
	if err != nil {
		return fmt.Errorf("query escalation notices: %w", err)
	}
	type notice struct {
		record    alertRecord
		binding   policyBinding
		contactID uuid.UUID
		event     string
	}
	var todo []notice
	for rows.Next() {
		var n notice
		r := &n.record
		var monitorID uuid.NullUUID
		var lastError sql.NullString
		if err := rows.Scan(&r.ID, &n.contactID, &n.event, &r.RelapseCount, &r.TenantID, &monitorID,
			&n.binding.MonitorName, &r.Kind, &r.TriggeredAt, &r.ResolvedAt, &r.FailureCount, &lastError,
			&r.ResolveReason); err != nil {
			rows.Close()
			return fmt.Errorf("scan escalation notice: %w", err)
		}
		if monitorID.Valid {
			r.MonitorID = monitorID.UUID
		}
		if lastError.Valid {
			r.LastError = &lastError.String
		}
		r.Status = n.event
		n.binding.MonitorID, n.binding.TenantID = r.MonitorID, r.TenantID
		todo = append(todo, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read escalation notices: %w", err)
	}
	now := time.Now()
	for i := range todo {
		n := &todo[i]
		if err := a.sendEscalationNotice(ctx, &n.record, n.binding, n.contactID, n.event, now); err != nil {
			a.logger.WithError(err).WithFields(map[string]interface{}{
				"alert_id": n.record.ID, "contact_id": n.contactID, "event_type": n.event,
			}).Warn("Failed to send an escalation notice")
		}
	}
	return nil
}

// sendEscalationNotice claims and sends one acknowledged/resolved notice.
func (a *Alerter) sendEscalationNotice(ctx context.Context, record *alertRecord, binding policyBinding, contactID uuid.UUID, event string, now time.Time) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin escalation notice claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var relapse int
	if err := tx.QueryRowContext(ctx, `SELECT status, relapse_count FROM alerts WHERE id = $1 FOR SHARE`, record.ID).
		Scan(&status, &relapse); err != nil {
		return fmt.Errorf("lock alert for notice: %w", err)
	}
	if status != event || relapse != record.RelapseCount {
		return nil
	}
	contact, err := contacts.LoadVerified(ctx, a.db, a.secretsEncryptor(), contactID)
	key := escalation.NoticeKey(event, relapse)
	var claimed uuid.UUID
	userID := contact.UserID
	if errors.Is(err, contacts.ErrNotDeliverable) {
		// Gone or unverified since the page: claim the notice so the sweep
		// stops asking, without sending. The user comes from the page row.
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM alert_escalation_pages WHERE alert_id = $1 AND contact_id = $2 LIMIT 1`,
			record.ID, contactID).Scan(&userID); err != nil {
			return fmt.Errorf("load paged user: %w", err)
		}
	} else if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO alert_escalation_pages (alert_id, contact_id, user_id, relapse_count, event, delivery_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
		RETURNING alert_id`, record.ID, contactID, userID, relapse, event, key).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim escalation notice: %w", err)
	}
	if !contact.Verified {
		return tx.Commit()
	}
	if err := a.sendPerson(ctx, personSend{contact: contact, eventType: event, key: key}, binding, record, now); err != nil && !plugin.IsPermanent(err) {
		return err
	}
	return tx.Commit()
}

// personSend is one send to one verified contact.
type personSend struct {
	contact   contacts.Contact
	eventType string
	// key is the delivery-ledger key (escalation.PageKey / NoticeKey).
	key string
	// stopOnAck: the worker drops the page if the alert is acknowledged
	// or snoozed by the time it is delivered (pages, not notices).
	stopOnAck bool
}

// errNoPersonalSMSChannel is the permanent failure of an SMS page in a
// workspace without a usable personal SMS channel.
var errNoPersonalSMSChannel = errors.New("the workspace has no active SMS channel for people")

// sendPerson delivers one event to one contact as a direct-recipient send:
// email through the platform mailer (the email plugin, no channel), SMS
// through the workspace's personal SMS channel. With async dispatch on it
// publishes an envelope carrying the ContactID, never the address. It
// records one delivery-history row either way.
func (a *Alerter) sendPerson(ctx context.Context, ps personSend, binding policyBinding, alert *alertRecord, now time.Time) error {
	entry := deliverylog.Entry{
		AlertID:     alert.ID,
		EventType:   ps.eventType,
		DeliveryKey: ps.key,
		Path:        deliverylog.PathSync,
		UserID:      ps.contact.UserID,
		ContactID:   ps.contact.ID,
		Recipient:   contacts.Mask(ps.contact.Kind, ps.contact.Address),
	}
	err := a.sendPersonVia(ctx, ps, binding, alert, now, &entry)
	if entry.Path == deliverylog.PathAsync && err == nil {
		entry.Outcome = deliverylog.OutcomeQueued
	} else {
		entry.Outcome = deliverylog.SendOutcome(err)
	}
	entry.Err = err
	if rerr := deliverylog.Record(ctx, a.db, entry); rerr != nil {
		a.logger.WithError(rerr).WithField("alert_id", alert.ID).Warn("Failed to record person delivery")
	}
	return err
}

func (a *Alerter) sendPersonVia(ctx context.Context, ps personSend, binding policyBinding, alert *alertRecord, now time.Time, entry *deliverylog.Entry) error {
	ref := plugin.ChannelRef{Name: "Email", Config: map[string]any{}}
	pluginType := "email"
	var channelID uuid.UUID
	var rawConfig []byte
	if ps.contact.Kind == contacts.KindSMS {
		var active bool
		err := a.db.QueryRowContext(ctx, `
			SELECT ac.id, ac.name, ac.type, ac.config, ac.is_active
			FROM tenants t
			JOIN alert_channels ac ON ac.id = t.personal_sms_channel_id AND ac.tenant_id = t.id
			WHERE t.id = $1`, alert.TenantID).Scan(&channelID, &ref.Name, &pluginType, &rawConfig, &active)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
			entry.ChannelName = "SMS"
			return plugin.Permanent(errNoPersonalSMSChannel)
		}
		if err != nil {
			return fmt.Errorf("load personal SMS channel: %w", err)
		}
		ref.ID = channelID.String()
	}
	entry.ChannelID, entry.ChannelName = channelID, ref.Name
	p, ok := plugin.DefaultRegistry.Get(pluginType)
	if !ok || !p.Manifest().HasCapability(plugin.CapabilityDirectRecipient) {
		return plugin.Permanent(fmt.Errorf("a %s channel cannot address one person", pluginType))
	}

	var resolvedAt *time.Time
	if ps.eventType == present.EventResolved {
		resolvedAt = &now
		if alert.ResolvedAt != nil {
			resolvedAt = alert.ResolvedAt
		}
	}
	event := buildAlertEvent(ps.eventType, binding, alert, resolvedAt, now)

	if a.config != nil && a.config.AsyncDispatch && a.nats != nil {
		entry.Path = deliverylog.PathAsync
		envelope := notifications.DispatchEnvelope{
			V:           1,
			ChannelType: pluginType,
			AlertID:     alert.ID.String(),
			EventType:   ps.eventType,
			Event:       event,
			DeliveryKey: ps.key,
			StopOnAck:   ps.stopOnAck,
			ContactID:   ps.contact.ID.String(),
		}
		if channelID != uuid.Nil {
			envelope.ChannelID = channelID.String()
		}
		msgID := alert.ID.String() + ":" + ps.contact.ID.String() + ":" + ps.key
		return a.nats.PublishJSON(ctx, "alerts.dispatch."+pluginType, envelope, map[string][]string{
			"x-idempotency-key": {msgID},
			"Nats-Msg-Id":       {msgID},
		})
	}

	if len(rawConfig) > 0 {
		cfg := map[string]any{}
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return fmt.Errorf("decode channel config: %w", err)
		}
		cfg, err := secrets.DecryptConfig(a.secretsEncryptor(), p.Manifest(), cfg)
		if err != nil {
			return fmt.Errorf("decrypt channel config: %w", err)
		}
		ref.Config = cfg
	}
	err := p.Send(ctx, plugin.DispatchRequest{
		Channel:   ref,
		Event:     event,
		EventType: ps.eventType,
		Attempt:   1,
		Recipient: &plugin.Recipient{ContactID: ps.contact.ID.String(), Address: ps.contact.Address},
	})
	if errors.Is(err, email.ErrNoMailer) {
		// No SMTP on this installation: retrying every cycle cannot help.
		err = plugin.Permanent(err)
	}
	return err
}

// secretsEncryptor is a.encryptor, or the passthrough when none is set.
func (a *Alerter) secretsEncryptor() secrets.Encryptor {
	if a.encryptor == nil {
		return secrets.NoOpEncryptor{}
	}
	return a.encryptor
}
