package alerter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const (
	// suspectedCauseSettle is how long after a channel's page its follow-up
	// waits, so it reads as a follow-up even when the page went through the
	// async retry backoff.
	suspectedCauseSettle = 3 * time.Minute
	// suspectedCauseMaxAge drops follow-ups whose triage and page are both
	// older than this: an alerter outage must not end in a burst of stale
	// hints. Measured from the later of the two, so an escalation tier paged
	// after the triage completed still gets its hint.
	suspectedCauseMaxAge = time.Hour
	// suspectedCauseBatch caps the (alert, channel) pairs one tick sends.
	suspectedCauseBatch = 50
	// defaultSuspectedCauseBudget caps the phase's wall time per tick. In the
	// default synchronous dispatch mode each send is an inline provider call
	// of up to its HTTP timeout, and advisory traffic must not eat the tick.
	defaultSuspectedCauseBudget = 5 * time.Second
)

func (a *Alerter) suspectedCauseTimeBudget() time.Duration {
	if a.suspectedCauseBudget > 0 {
		return a.suspectedCauseBudget
	}
	return defaultSuspectedCauseBudget
}

// runSuspectedCauses runs dispatchSuspectedCauses on its own sub-deadline:
// at most suspectedCauseTimeBudget, and never into the part of the tick
// reserved for the deferred recovery-notification sweep, which is its full
// recoveryRetryBudget, or half of what is left on a tick too short for that
// (a small ALERT_EVAL_INTERVAL_SECONDS). Sends it cuts off roll back their
// claim, so the next tick picks them up.
func (a *Alerter) runSuspectedCauses(ctx context.Context) error {
	if a.config != nil && a.config.SuspectedCausesDisabled {
		return nil
	}
	budget := a.suspectedCauseTimeBudget()
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline)
		available := left - min(a.recoveryRetryBudget(), left/2)
		if available <= 0 {
			return nil
		}
		budget = min(budget, available)
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return a.dispatchSuspectedCauses(ctx)
}

// dispatchSuspectedCauses sends the opt-in "suspected_cause" follow-up: one
// advisory message per (alert, channel) when the alert's automatic triage
// names a cause. It is the alerter's only reader of alert_triage. Triage never
// feeds the created/reminder/acknowledged/resolved events, routing,
// suppression or severity; this phase only ever adds one message after a page
// went out, and never delays, routes, suppresses or re-prioritises one.
//
// A follow-up goes to a channel only when all of these hold:
//   - the workspace opted in (ai_settings.triage_notifications_enabled, with
//     enabled and auto_triage_enabled) and the triage completed after it did,
//     and the installation kill switches are off (runSuspectedCauses);
//   - the triage is ready with a named cause (ai.IsNamedCause) at medium or
//     high confidence (ai.IsConfident), and no operator label corrected or
//     rejected it;
//   - the channel was already paged for this open alert (its
//     alert_notification_states row, not resolved), at least
//     suspectedCauseSettle ago, the triage or that page is younger than
//     suspectedCauseMaxAge, and current routing still reaches the channel
//     (the same resolveChannelTargets path reminders use);
//   - the channel is active and its plugin advertises
//     plugin.CapabilityFollowUp (paging tools do not);
//   - the alert is not muted: openAlertDispatchablePredicate, shared with
//     reminders (maintenance, roll-up, dependency suppression).
//
// The send goes through fireChannel → deliverNotification, whose one-shot
// claim on suspected_cause_sent_at makes replicas safe, and whose FOR SHARE
// status check refuses an alert resolved in the meantime.
func (a *Alerter) dispatchSuspectedCauses(ctx context.Context) error {
	var types []string
	for _, m := range plugin.DefaultRegistry.All() {
		if m.HasCapability(plugin.CapabilityFollowUp) {
			types = append(types, m.Type)
		}
	}
	if len(types) == 0 {
		return nil
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT al.id, al.tenant_id, al.monitor_id, m.name, al.kind, al.status, al.triggered_at,
			al.failure_count, al.root_cause_monitor_id, al.root_cause_down_since, rcm.name,
			al.baseline_latency_ms, al.observed_latency_ms, al.anomaly_score,
			al.metric_name, al.metric_value, al.threshold_value,
			tr.id, tr.hypothesis, tr.confidence,
			ac.id, ac.name, ac.type, ac.config, ac.is_active
		FROM alerts al
		JOIN monitors m ON m.id = al.monitor_id AND m.tenant_id = al.tenant_id
		JOIN tenants te ON te.id = al.tenant_id
		JOIN ai_settings s ON s.tenant_id = al.tenant_id
		JOIN alert_triage tr ON tr.alert_id = al.id AND tr.tenant_id = al.tenant_id
		JOIN alert_notification_states ans ON ans.alert_id = al.id
		JOIN alert_channels ac ON ac.id = ans.channel_id AND ac.tenant_id = al.tenant_id
		LEFT JOIN monitors rcm ON rcm.id = al.root_cause_monitor_id
		WHERE s.enabled AND s.auto_triage_enabled AND s.triage_notifications_enabled
		  AND tr.status = 'ready'
		  AND tr.hypothesis = ANY($1)
		  AND tr.confidence = ANY($2)
		  -- Opting in starts the clock: earlier triages are never flushed.
		  AND tr.completed_at >= s.triage_notifications_enabled_at
		  -- An operator who corrected or rejected the cause has spoken.
		  AND NOT EXISTS (
			SELECT 1 FROM ai_labels l
			WHERE l.tenant_id = tr.tenant_id AND l.subject_type = 'alert_triage'
			  AND l.subject_key = tr.id::text AND l.verdict IN ('corrected', 'rejected'))
		  AND ans.suspected_cause_sent_at IS NULL
		  AND ans.last_event_type <> 'resolved'
		  AND ac.is_active
		  AND ac.type = ANY($3)
		  AND ans.created_at <= NOW() - make_interval(secs => $4)
		  AND GREATEST(tr.completed_at, ans.created_at) >= NOW() - make_interval(secs => $5)
		  AND `+openAlertDispatchablePredicate()+`
		ORDER BY tr.completed_at, al.id, ac.id
		LIMIT $6
	`, pq.Array(ai.NamedCauses()), pq.Array(ai.ConfidentLevels()), pq.Array(types),
		suspectedCauseSettle.Seconds(), suspectedCauseMaxAge.Seconds(), suspectedCauseBatch)
	if err != nil {
		return fmt.Errorf("query suspected-cause follow-ups: %w", err)
	}

	type pending struct {
		binding policyBinding
		record  alertRecord
		channel alertChannel
	}
	var todo []pending
	for rows.Next() {
		var p pending
		var rcID uuid.NullUUID
		var rcDownSince sql.NullTime
		var rcName, metricName sql.NullString
		var baseline, observed, score, metricValue, thresholdValue sql.NullFloat64
		var triageID uuid.UUID
		var hypothesis, confidence string
		var configBytes []byte
		r := &p.record
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MonitorID, &p.binding.MonitorName, &r.Kind, &r.Status,
			&r.TriggeredAt, &r.FailureCount, &rcID, &rcDownSince, &rcName,
			&baseline, &observed, &score, &metricName, &metricValue, &thresholdValue,
			&triageID, &hypothesis, &confidence,
			&p.channel.ID, &p.channel.Name, &p.channel.Type, &configBytes, &p.channel.IsActive); err != nil {
			rows.Close()
			return fmt.Errorf("scan suspected-cause follow-up: %w", err)
		}
		title, actions, ok := ai.RCAChoice(hypothesis)
		if !ok || !ai.IsConfident(confidence) {
			continue // filtered in SQL; never send a key the catalogue cannot name
		}
		r.SuspectedCause = &notifications.SuspectedCause{
			TriageID: triageID.String(), Key: hypothesis, Title: title, Confidence: confidence, Actions: actions,
		}
		setRootCause(r, rcID, rcName, rcDownSince)
		setLatencyMetrics(r, baseline, observed, score)
		setHostMetric(r, metricName, metricValue, thresholdValue)
		p.binding.MonitorID, p.binding.TenantID = r.MonitorID, r.TenantID
		p.channel.Config = json.RawMessage(configBytes)
		todo = append(todo, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read suspected-cause follow-ups: %w", err)
	}

	// Current routing per alert, resolved once: a channel removed from the
	// monitor's routing after the page stops getting reminders, and gets no
	// follow-up either.
	routed := map[uuid.UUID]map[uuid.UUID]bool{}
	now := time.Now()
	for i := range todo {
		p := &todo[i]
		if err := ctx.Err(); err != nil {
			return nil // out of budget; the unclaimed rest goes next tick
		}
		channels, seen := routed[p.record.ID]
		if !seen {
			targets, err := a.resolveChannelTargets(ctx, p.record.TenantID, p.record.MonitorID)
			if err != nil {
				a.logger.WithError(err).WithFields(map[string]interface{}{"alert_id": p.record.ID}).Error("Failed to resolve channels")
				continue
			}
			channels = make(map[uuid.UUID]bool, len(targets))
			for _, t := range targets {
				channels[t.channel.ID] = true
			}
			routed[p.record.ID] = channels
		}
		if !channels[p.channel.ID] {
			continue
		}
		a.fireChannel(ctx, "suspected_cause", p.binding, &p.record, p.channel, now, 0)
	}
	return nil
}
