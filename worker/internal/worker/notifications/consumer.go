// Package notifications implements the worker-side consumer for the
// asynchronous alert dispatch pipeline introduced by step 9 of the alert
// channel plugin system. The alerter publishes a notifications.DispatchEnvelope
// onto the "alerts.dispatch.<plugin_type>" subject of the NOTIFICATIONS
// stream; this consumer fetches the channel row, decrypts secrets, and
// invokes the registered plugin's Send method with a configurable retry
// policy (delayed NAKs: 10s, 30s, 2m, 10m — five total attempts).
//
// The alerter is the source of truth for "should this send happen?" — it
// writes alert_notification_states pre-publish, so its own dedup logic
// prevents the alerter from republishing on every eval tick. The worker only
// runs the actual delivery and lives with the small risk of duplicates if a
// successful Send is followed by a Nak (rare: usually Send-failure and Nak
// happen together).
package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// DefaultBackOff is the delayed-NAK schedule after a failed attempt:
// 10s, 30s, 2m, 10m. Five total attempts before redelivery stops
// (no DLQ table in v1 — that's deferred).
var DefaultBackOff = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

const (
	// DispatchTimeout caps handling including DB/config loading and Send.
	DispatchTimeout = 15 * time.Second

	// MaxDeliver includes the initial attempt and four retries.
	MaxDeliver = 5
)

// Consumer wires the dependencies needed to consume a single alert dispatch
// message: DB (to fetch the channel row), encryptor (to decrypt secrets),
// queue (to manage the consumer), and the plugin registry (via the package
// singleton).
type Consumer struct {
	cfg       *config.WorkerConfig
	logger    *logger.Logger
	db        *db.Client
	queue     *queue.Client
	encryptor secrets.Encryptor
}

// New constructs a Consumer. The encryptor must be the same instance used by
// the API service (so what the API encrypts, the worker can decrypt).
func New(cfg *config.WorkerConfig, log *logger.Logger, dbClient *db.Client, queueClient *queue.Client, encryptor secrets.Encryptor) *Consumer {
	if encryptor == nil {
		encryptor = secrets.NoOpEncryptor{}
	}
	return &Consumer{
		cfg:       cfg,
		logger:    log,
		db:        dbClient,
		queue:     queueClient,
		encryptor: encryptor,
	}
}

// Start ensures the NOTIFICATIONS stream + per-plugin consumer exist, then
// blocks on Consume until ctx is cancelled. Returns when ctx is done or the
// queue returns a non-cancellation error.
func (c *Consumer) Start(ctx context.Context) error {
	streamName := c.cfg.NotificationsStream
	subjectGlob := c.cfg.NotificationsSubjectGlob
	consumerName := c.cfg.NotificationsConsumerName

	// 24h max age — well past the complete retry schedule
	// so messages don't expire mid-retry.
	if _, err := c.queue.EnsureWorkQueueStream(ctx, streamName, []string{subjectGlob}, 24*time.Hour); err != nil {
		return fmt.Errorf("ensure notifications stream: %w", err)
	}

	consumer, err := c.queue.CreateConsumerWithOptions(ctx, streamName, consumerName, consumerOptions())
	if err != nil {
		return fmt.Errorf("create notifications consumer: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"stream":      streamName,
		"subject":     subjectGlob,
		"consumer":    consumerName,
		"backoff":     DefaultBackOff,
		"max_deliver": MaxDeliver,
	}).Info("Notifications consumer started")

	return c.queue.Consume(ctx, consumer, func(msg *queue.Message) error {
		return c.handle(ctx, msg)
	})
}

func consumerOptions() queue.ConsumerOptions {
	return queue.ConsumerOptions{
		AckWait:    DispatchTimeout + 5*time.Second,
		MaxDeliver: MaxDeliver,
		// Do not set BackOff: its first entry overrides AckWait, allowing
		// redelivery before a still-running Send reaches its timeout.
	}
}

// handle is the per-message entry point. Returning nil acks the message;
// returning an error requests an explicit delayed NAK.
func (c *Consumer) handle(ctx context.Context, msg *queue.Message) (err error) {
	ctx, cancel := context.WithTimeout(ctx, DispatchTimeout)
	defer cancel()
	defer func() {
		if err != nil {
			err = queue.RetryAfter(err, retryDelay(err, deliveryAttempt(msg)))
		}
	}()
	var envelope notifications.DispatchEnvelope
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		// Poison message — log and ack (returning nil) so it doesn't loop
		// forever. There's no recovery path for invalid JSON.
		c.logger.WithError(err).WithField("subject", msg.Subject).Error("Failed to unmarshal dispatch envelope; dropping message")
		return nil
	}

	entry := c.logger.WithFields(logrus.Fields{
		"channel_id":      envelope.ChannelID,
		"channel_type":    envelope.ChannelType,
		"alert_id":        envelope.AlertID,
		"event_type":      envelope.EventType,
		"idempotency_key": envelope.IdempotencyKey(),
	})

	if envelope.V != 1 {
		entry.Error("Unsupported dispatch envelope version; dropping")
		return nil
	}
	record := c.deliveryRecord(entry, envelope, deliveryAttempt(msg)) // delivery history

	p, ok := plugin.DefaultRegistry.Get(envelope.ChannelType)
	if !ok {
		// The plugin is missing from this worker build. This is a deployment
		// bug — return an error so JetStream retries and the operator notices
		// the consumer falling behind, then ship the missing plugin.
		entry.Error("No registered plugin for channel type")
		err := fmt.Errorf("unknown plugin type: %s", envelope.ChannelType)
		record.failed(ctx, deliverylog.OutcomeFailed, deliverylog.ReasonPluginMissing, err, nil)
		return err
	}

	channel, err := c.loadChannel(ctx, envelope.ChannelID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Channel (or its workspace) was deleted while the message was
			// in flight. Drop — retrying won't help.
			entry.Warn("Channel no longer exists; dropping message")
			return nil
		}
		entry.WithError(err).Error("Failed to load channel")
		return err
	}
	if !channel.IsActive {
		entry.Info("Channel deactivated; dropping message")
		record.dropped(ctx, deliverylog.ReasonChannelInactive, nil)
		return nil
	}
	if channel.Type != envelope.ChannelType {
		// Edge case: the channel was repurposed (type changed). Drop because
		// the plugin we'd dispatch to no longer matches the stored config.
		entry.WithField("current_type", channel.Type).Warn("Channel type changed since publish; dropping message")
		record.dropped(ctx, deliverylog.ReasonChannelChanged, nil)
		return nil
	}

	// Hold a share lock on the alert row from the status check through the
	// send. Resolution is an UPDATE of that row, so it waits until this send
	// has finished, and the resolve it then publishes is delivered after it.
	// Without the lock a trigger that passed the check could still reach the
	// provider after a concurrent worker delivered the resolve. Resolves take
	// it too: a relapse reopen is an UPDATE of the row as well, so it waits
	// for a resolve being delivered, and a resolve still queued when the
	// reopen commits is dropped under the lock ("recovered" must never reach
	// a route after "relapsed").
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		entry.WithError(err).Error("Failed to begin alert lock")
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Paging providers key their incident per page episode
	// (notifications.AlertDetails.PagedRelapse), so a late resolve closes
	// only the incident it was for and is always delivered.
	keyedIncidents := p.Manifest().HasCapability(plugin.CapabilityAcknowledge)
	superseded, muted, alertStatus, reason, err := eventSuperseded(ctx, tx, envelope, keyedIncidents)
	if err != nil {
		entry.WithError(err).Error("Failed to load alert status")
		return err
	}
	// Which provider incident this send is about is what the route last had
	// delivered, read now under the lock, not what the alerter's claim
	// assumed when it published (useOpenIncident).
	if err := useOpenIncident(ctx, tx, &envelope); err != nil {
		entry.WithError(err).Error("Failed to load the route's open incident")
		return err
	}
	if muted {
		// Acknowledged or snoozed since publish (alertrouting.MuteHolds): an
		// acknowledgement must never lose the race to a page or reminder
		// already queued. Unlike a resolve the mute can lift, so a page that
		// provably reached nobody hands the alerter's claim back under the
		// same lock and is due again then (releaseMutedClaim).
		if err := releaseMutedClaim(ctx, tx, envelope); err != nil {
			entry.WithError(err).Error("Failed to release a muted notification claim")
			return err
		}
		var closed *closeRecord
		if keyedIncidents {
			closed = c.closePreviousIncident(ctx, entry, p, channel, envelope, deliveryAttempt(msg))
		}
		closed.write(ctx, tx)
		record.droppedTx(ctx, tx, mutedReason(ctx, tx, envelope))
		if err := tx.Commit(); err != nil {
			entry.WithError(err).Error("Failed to commit a released notification claim")
			return err
		}
		entry.WithField("alert_status", alertStatus).Info("Alert acknowledged or snoozed since publish; dropping held event")
		return nil
	}
	if superseded && envelope.EventType == "resolved" {
		// Only an alert open again (a relapse) supersedes a resolve. A route
		// this recovery provably never reached hands its claim back under the
		// same lock (releaseSupersededResolveClaim), so it hears the
		// relapse's resolve; the history row is written in the same tx.
		if err := releaseSupersededResolveClaim(ctx, tx, envelope); err != nil {
			entry.WithError(err).Error("Failed to release a superseded resolve claim")
			return err
		}
		record.droppedTx(ctx, tx, reason)
		if err := tx.Commit(); err != nil {
			entry.WithError(err).Error("Failed to commit a released resolve claim")
			return err
		}
		entry.WithField("alert_status", alertStatus).Info("Alert reopened since publish; dropping superseded resolve")
		return nil
	}
	if superseded {
		// A retried trigger/reminder/ack must never land after the resolve:
		// PagerDuty and Opsgenie accept a resolve for an alert they do not
		// have yet as a no-op, so a late trigger would open an incident that
		// nothing ever closes. One published before a relapse reopen belongs
		// to the episode that recovered; the relapse pages on its own.
		entry.WithField("alert_status", alertStatus).Info("Alert resolved or reopened since publish; dropping superseded event")
		var closed *closeRecord
		if keyedIncidents {
			closed = c.closePreviousIncident(ctx, entry, p, channel, envelope, deliveryAttempt(msg))
		}
		record.dropped(ctx, reason, tx) // releases the alert row
		closed.write(ctx, nil)
		return nil
	}

	configMap := map[string]any{}
	if len(channel.Config) > 0 {
		if err := json.Unmarshal(channel.Config, &configMap); err != nil {
			entry.WithError(err).Error("Failed to decode channel config")
			record.failed(ctx, deliverylog.OutcomePermanentFailure, deliverylog.ReasonInvalidConfig, err, tx)
			return nil
		}
	}
	configMap, err = secrets.DecryptConfig(c.encryptor, p.Manifest(), configMap)
	if err != nil {
		entry.WithError(err).Error("Failed to decrypt channel config")
		record.failed(ctx, deliverylog.OutcomeFailed, "", err, tx)
		return err
	}
	record.channelLoaded(p.Manifest(), configMap)

	attempt := deliveryAttempt(msg)
	alertID, alertErr := uuid.Parse(envelope.AlertID)
	channelID, channelErr := uuid.Parse(envelope.ChannelID)
	send := func(eventType, deliveryKey string, event notifications.AlertEvent) error {
		// A redelivery may reach another worker, or this one after a
		// restart; the ledger lets it skip the recipients an earlier attempt
		// already reached. It writes through the pool, never tx: tx only
		// holds the alert lock and rolls back after a send.
		var recipients plugin.RecipientLedger
		if alertErr == nil && channelErr == nil {
			recipients = recipientledger.For(c.db, alertID, channelID, deliveryKey)
		}
		recipients = record.track(recipients)
		return p.Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{
				ID:     envelope.ChannelID,
				Name:   channel.Name,
				Config: configMap,
			},
			Event:      event,
			EventType:  eventType,
			Attempt:    attempt,
			Recipients: recipients,
		})
	}
	marked, err := c.markPage(ctx, envelope)
	if err != nil {
		entry.WithError(err).Error("Failed to record a page attempt")
		return err
	}
	err = send(envelope.EventType, envelope.RecipientKey(), envelope.Event)
	c.settlePage(ctx, entry, envelope, marked, err)
	var closed *closeRecord
	if err == nil {
		c.recordDeliveredPage(ctx, entry, envelope)
	} else if keyedIncidents && (plugin.IsPermanent(err) || attempt >= MaxDeliver) {
		// A relapse notice nothing will retry: its close of the earlier
		// incident may never have gone out with it.
		closed = c.closePreviousIncident(ctx, entry, p, channel, envelope, attempt)
	}
	// Delivery history: Send is done, so the alert lock goes first (tx only
	// ever rolls back on this path) and the row is written through the pool.
	record.finished(ctx, err, tx) // releases the alert row
	closed.write(ctx, nil)
	if err != nil {
		if plugin.IsPermanent(err) {
			// Retrying cannot fix a rejected credential or a deleted
			// webhook; ack so the failure is logged once, not five times.
			entry.WithError(err).WithField("attempt", attempt).Error("Plugin Send failed permanently; dropping message")
			return nil
		}
		entry.WithError(err).WithField("attempt", attempt).Warn("Plugin Send failed; will retry")
		return err
	}

	entry.WithField("attempt", attempt).Debug("Notification delivered")
	return nil
}

// eventSuperseded reports whether envelope describes an alert state the
// alert has since left: any non-resolve event for an alert that is now
// resolved (or deleted), an acknowledgement of an alert unacknowledged
// since, any non-resolve event published in an earlier paging episode (the
// alert's relapse_count passed the event's), and a resolve while the alert
// is open again after a relapse, unless the channel keys its provider
// incident per episode (keyedIncidents). A resolve of an alert that is
// resolved now is always delivered, whatever episode it was queued in: a
// channel that never heard the relapse got no resolve for it, so this one is
// what closes its provider's incident. JetStream redelivers failed messages on
// a backoff, so a created/reminder/acknowledged/suspected_cause message can
// outlive the resolve the alerter published after it; delivering it then
// would reopen the alert on the provider side. It reads the status FOR SHARE
// inside tx, and the caller keeps tx open until the send returns, so an
// acknowledge, snooze, resolve or reopen committed after this check waits for
// the send.
//
// muted is set (with superseded false) when the alert is still open but
// acknowledged or snoozed and the event is one the mute holds
// (alertrouting.MuteHolds: a page or a reminder): the caller drops it and,
// when it is safe, hands its claim back (releaseMutedClaim), because the
// mute is temporary.
//
// reason is the delivery-history reason of a superseded event: resolved or
// unacknowledged (supersededReason), deliverylog.ReasonReopened for a resolve
// dropped because the alert is open again, deliverylog.ReasonEarlierEpisode
// for any other event of an episode the alert has left.
func eventSuperseded(ctx context.Context, tx *sql.Tx, envelope notifications.DispatchEnvelope, keyedIncidents bool) (superseded, muted bool, status string, reason deliverylog.Reason, err error) {
	alertID, err := uuid.Parse(envelope.AlertID)
	if err != nil {
		return false, false, "", "", fmt.Errorf("invalid alert id %q: %w", envelope.AlertID, err)
	}
	var snoozed bool
	var relapseCount int
	err = tx.QueryRowContext(ctx, `SELECT al.status, `+alertrouting.SnoozedPredicate("al")+`, al.relapse_count
		FROM alerts al WHERE al.id = $1 FOR SHARE`, alertID).Scan(&status, &snoozed, &relapseCount)
	if errors.Is(err, sql.ErrNoRows) {
		// Deleted with its alert history: nothing left to tell.
		return envelope.EventType != "resolved", false, "", supersededReason(""), nil
	}
	if err != nil {
		return false, false, "", "", err
	}
	if envelope.EventType == "resolved" {
		// Resolves are published only once the resolution committed, so an
		// open alert here reopened since: "recovered" must not follow
		// "relapsed" there. A paging provider's resolve closes the earlier
		// episode's incident only, so it still goes out.
		if status != "resolved" && !keyedIncidents {
			return true, false, status, deliverylog.ReasonReopened, nil
		}
		return false, false, status, "", nil
	}
	if relapseCount > envelope.Event.Alert.RelapseCount {
		return true, false, status, deliverylog.ReasonEarlierEpisode, nil
	}
	if status == "resolved" {
		return true, false, status, supersededReason(status), nil
	}
	if envelope.EventType == "acknowledged" && status != "acknowledged" {
		// Unacknowledged since publish: telling the provider to stop
		// escalating would now be wrong. The unacknowledge already re-armed
		// the claim (acknowledged_sent_at), so a later acknowledge is sent.
		return true, false, status, supersededReason(status), nil
	}
	return false, alertrouting.MuteHolds(envelope.EventType, status == "acknowledged", snoozed), status, "", nil
}

// releaseMutedClaim hands back the alerter's claim on a page ("created") the
// mute held, inside tx (which still holds the alert row FOR SHARE, so no
// claim can move meanwhile), so the route is paged once the mute lifts
// instead of counting as paged when it never was. Left in place, a route
// would only ever get reminders after an unacknowledge, which paging
// providers ignore.
//
// Only a page that provably reached nobody is handed back: no attempt
// marker (markPage; an attempt whose send may have been accepted keeps it)
// and no recipient recorded delivered, on whichever delivery attempt this is.
// Any other held page keeps its claim, so the channel still counts as paged
// and gets the acknowledgement and the recovery notice: a provider that
// opened an incident on an attempt whose reply was lost, or the recipients a
// multi-recipient send already reached, must hear both. The row is matched
// only while it still records exactly this claim (its time is the event's
// timestamp, the claim's `now`).
//
// A held relapse notice goes out as a page too (a "created" event with
// RelapseNotice), claimed by moving the route's row to 'relapsed' in the
// alert's episode; handing it back restores the row the claim replaced
// (DispatchEnvelope.Release), so the notice is due again and nothing else
// about the route changed. The one exception is a replaced 'resolved' whose
// recovery notice provably reached nobody (recoveryReached): that row goes
// back as 'created', still paged, so the route hears the next recovery.
//
// A held reminder keeps its claim: its next round counts from the later of
// the last send and the way out of the mute, which comes after this claim,
// so the earlier last_sent_at would change nothing.
func releaseMutedClaim(ctx context.Context, tx *sql.Tx, envelope notifications.DispatchEnvelope) error {
	if envelope.EventType != "created" || envelope.Event.Timestamp.IsZero() {
		return nil
	}
	alertID, err := uuid.Parse(envelope.AlertID)
	if err != nil {
		return fmt.Errorf("invalid alert id %q: %w", envelope.AlertID, err)
	}
	channelID, err := uuid.Parse(envelope.ChannelID)
	if err != nil {
		return fmt.Errorf("invalid channel id %q: %w", envelope.ChannelID, err)
	}
	reached, err := recipientledger.Reached(ctx, tx, alertID, channelID, envelope.RecipientKey())
	if err != nil || reached {
		return err
	}
	if envelope.Event.Alert.RelapseNotice && envelope.Release != nil {
		// The row as the relapse claim found it (last event, its time, the
		// episode of its last page), so a route that heard the earlier
		// recovery is 'resolved' again and gets no second recovery notice,
		// the same as when the acknowledgement had come before the claim.
		release := *envelope.Release
		if release.EventType == "resolved" {
			// But 'resolved' only says the alerter claimed that recovery
			// notice, not that it went out: it may still wait on a retry, or
			// have been dropped as superseded after this claim moved the row
			// on, which no longer matched releaseSupersededResolveClaim. Left
			// 'resolved', the route then never hears the relapse's recovery
			// either (the alerter sends one only to rows not already
			// 'resolved'), and its last word stays the earlier DOWN.
			heard, err := recoveryReached(ctx, tx, alertID, channelID, release.PagedRelapse, envelope.Event.Alert.RelapseCount)
			if err != nil {
				return err
			}
			if !heard {
				release.EventType = "created"
			}
		}
		// Done here: the row the notice hands back is never a first page's
		// claim, and one restored as 'created' at the claim's own time must
		// not be mistaken for it by the delete below.
		_, err := tx.ExecContext(ctx, `
			UPDATE alert_notification_states
			SET last_event_type = $4, last_sent_at = COALESCE($5::timestamptz, last_sent_at), paged_relapse = $6, updated_at = NOW()
			WHERE alert_id = $1 AND channel_id = $2
			  AND last_event_type = 'relapsed' AND last_sent_at = $3::timestamptz`,
			alertID, channelID, envelope.Event.Timestamp, release.EventType, release.SentAt, release.PagedRelapse)
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM alert_notification_states
		WHERE alert_id = $1 AND channel_id = $2
		  AND last_event_type = 'created' AND last_sent_at = $3::timestamptz`,
		alertID, channelID, envelope.Event.Timestamp)
	return err
}

// recoveryReached reports whether the recovery notice behind a route's
// 'resolved' row possibly reached the channel, by the test
// releaseSupersededResolveClaim applies: its attempt marker or a recipient
// in the ledger under its delivery key (a delivered resolve keeps its marker;
// one that failed with proof nothing was accepted clears it; a dropped one
// never wrote one). That resolve was claimed after the route's last page
// (episode pagedRelapse) and before the relapse notice of episode relapse, so
// its key is the resolve key of an episode from pagedRelapse to relapse-1
// (an alert muted through whole episodes can skip some). The row records one resolve
// claim since its last page, so a key in that range is that resolve's (an
// earlier one handed back under releaseSupersededResolveClaim provably
// reached nobody). A resolve sent before attempt markers existed, or by the
// alerter's synchronous path, reads as not reached: the safe side is a second
// recovery notice, never none.
func recoveryReached(ctx context.Context, tx *sql.Tx, alertID, channelID uuid.UUID, pagedRelapse, relapse int) (bool, error) {
	var keys []string
	for episode := pagedRelapse; episode < relapse; episode++ {
		keys = append(keys, notifications.RecipientEventKeyFor("resolved", time.Time{}, episode))
	}
	return recipientledger.ReachedAny(ctx, tx, alertID, channelID, keys)
}

// releaseSupersededResolveClaim hands back the alerter's claim on a resolve
// dropped because the alert is open again after a relapse, inside tx (which
// still holds the alert row FOR SHARE). The alerter committed the route's row
// as 'resolved' when it published, but the route never heard that recovery.
// The route is due the relapse notice either way (its page is from an
// earlier episode); but if the relapse recovers before the route's
// escalation delay, a row left 'resolved' gets no resolve at all (the
// alerter sends one only to rows not already 'resolved'), and the channel
// never hears that the alert recovered. Moved back to 'created' it counts as
// still paged, as on the sync path where a resolve not sent before the
// reopen stays unclaimed, so the relapse's resolve reaches it.
//
// Like releaseMutedClaim, only a resolve that provably reached nobody is
// handed back, whichever delivery attempt this is: no attempt marker (markPage
// marks resolves as it marks pages, and clears the marker only when the send
// proved nothing was accepted) and no recipient recorded delivered under its
// key, which names the episode. One that may have reached the provider keeps
// 'resolved'. The row is matched only while it still records exactly this
// claim: its time is the event's timestamp, and a relapse notice claimed
// since would have moved it to 'relapsed' with a new time.
func releaseSupersededResolveClaim(ctx context.Context, tx *sql.Tx, envelope notifications.DispatchEnvelope) error {
	if envelope.Event.Timestamp.IsZero() {
		return nil
	}
	alertID, err := uuid.Parse(envelope.AlertID)
	if err != nil {
		return fmt.Errorf("invalid alert id %q: %w", envelope.AlertID, err)
	}
	channelID, err := uuid.Parse(envelope.ChannelID)
	if err != nil {
		return fmt.Errorf("invalid channel id %q: %w", envelope.ChannelID, err)
	}
	reached, err := recipientledger.Reached(ctx, tx, alertID, channelID, envelope.RecipientKey())
	if err != nil || reached {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE alert_notification_states
		SET last_event_type = 'created', updated_at = NOW()
		WHERE alert_id = $1 AND channel_id = $2
		  AND last_event_type = 'resolved' AND last_sent_at = $3::timestamptz`,
		alertID, channelID, envelope.Event.Timestamp)
	return err
}

type loadedChannel struct {
	Name     string
	Type     string
	Config   json.RawMessage
	IsActive bool
}

func (c *Consumer) loadChannel(ctx context.Context, channelIDStr string) (*loadedChannel, error) {
	id, err := uuid.Parse(channelIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid channel id %q: %w", channelIDStr, err)
	}

	// A channel of a deleted workspace reads as gone, so a notification
	// queued before the delete is dropped like one for a deleted channel:
	// nothing is sent and nothing is recorded (shared/tenancy).
	row := c.db.QueryRowContext(ctx, `
		SELECT name, type, config, is_active
		FROM alert_channels
		WHERE id = $1 AND `+tenancy.LiveTenantSQL("alert_channels.tenant_id")+`
	`, id)

	var channel loadedChannel
	var cfgBytes []byte
	if err := row.Scan(&channel.Name, &channel.Type, &cfgBytes, &channel.IsActive); err != nil {
		return nil, err
	}
	channel.Config = json.RawMessage(cfgBytes)
	return &channel, nil
}

// retryDelay is the delayed-NAK interval after a failed attempt: the
// provider's Retry-After when it sent one, else the BackOff schedule.
func retryDelay(err error, attempt int) time.Duration {
	if d, ok := plugin.RetryAfterDelay(err); ok {
		return d
	}
	return DefaultBackOff[min(attempt-1, len(DefaultBackOff)-1)]
}

// deliveryAttempt uses JetStream metadata, defaulting to 1. Used to populate
// plugin.DispatchRequest.Attempt for plugins that vary behavior on retry
// (e.g. exponential rate limiting on their side).
func deliveryAttempt(msg *queue.Message) int {
	if msg == nil || msg.NumDelivered == 0 {
		return 1
	}
	return int(msg.NumDelivered)
}
