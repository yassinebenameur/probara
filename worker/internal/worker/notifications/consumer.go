// Package notifications implements the worker-side consumer for the
// asynchronous alert dispatch pipeline introduced by step 9 of the alert
// channel plugin system. The alerter publishes a notifications.DispatchEnvelope
// onto the "alerts.dispatch.<plugin_type>" subject of the NOTIFICATIONS
// stream; this consumer fetches the channel row, decrypts secrets, and
// invokes the registered plugin's Send method with a configurable retry
// policy (BackOff: 10s, 30s, 2m, 10m, 30m — five total attempts).
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

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// DefaultBackOff is the retry schedule installed on the JetStream consumer:
// 10s, 30s, 2m, 10m, 30m. Five total attempts before the message is dropped
// (no DLQ table in v1 — that's deferred).
var DefaultBackOff = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
}

const (
	// DispatchTimeout caps a single plugin.Send invocation so a hung webhook
	// can't tie up a consumer slot indefinitely.
	DispatchTimeout = 15 * time.Second

	// MaxDeliver matches len(DefaultBackOff)+1 — JetStream's MaxDeliver counts
	// the initial attempt, so the BackOff array must be one shorter. We pass
	// MaxDeliver=5 with a 5-entry BackOff which means: deliver, then if
	// nak'd, wait BackOff[0]=10s, redeliver, wait BackOff[1]=30s, etc.
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

	// 24h max age — well past the 5-attempt 30-minute tail of DefaultBackOff
	// so messages don't expire mid-retry.
	if _, err := c.queue.EnsureWorkQueueStream(ctx, streamName, []string{subjectGlob}, 24*time.Hour); err != nil {
		return fmt.Errorf("ensure notifications stream: %w", err)
	}

	consumer, err := c.queue.CreateConsumerWithOptions(ctx, streamName, consumerName, queue.ConsumerOptions{
		AckWait:    DispatchTimeout + 5*time.Second,
		MaxDeliver: MaxDeliver,
		BackOff:    DefaultBackOff,
	})
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

// handle is the per-message entry point. Returning nil acks the message;
// returning an error triggers JetStream's BackOff retry sequence.
func (c *Consumer) handle(ctx context.Context, msg *queue.Message) error {
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

	p, ok := plugin.DefaultRegistry.Get(envelope.ChannelType)
	if !ok {
		// The plugin is missing from this worker build. This is a deployment
		// bug — return an error so JetStream retries and the operator notices
		// the consumer falling behind, then ship the missing plugin.
		entry.Error("No registered plugin for channel type")
		return fmt.Errorf("unknown plugin type: %s", envelope.ChannelType)
	}

	channel, err := c.loadChannel(ctx, envelope.ChannelID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Channel was deleted while the message was in flight. Drop —
			// retrying won't help.
			entry.Warn("Channel no longer exists; dropping message")
			return nil
		}
		entry.WithError(err).Error("Failed to load channel")
		return err
	}
	if !channel.IsActive {
		entry.Info("Channel deactivated; dropping message")
		return nil
	}
	if channel.Type != envelope.ChannelType {
		// Edge case: the channel was repurposed (type changed). Drop because
		// the plugin we'd dispatch to no longer matches the stored config.
		entry.WithField("current_type", channel.Type).Warn("Channel type changed since publish; dropping message")
		return nil
	}

	configMap := map[string]any{}
	if len(channel.Config) > 0 {
		if err := json.Unmarshal(channel.Config, &configMap); err != nil {
			entry.WithError(err).Error("Failed to decode channel config")
			return nil
		}
	}
	configMap, err = secrets.DecryptConfig(c.encryptor, p.Manifest(), configMap)
	if err != nil {
		entry.WithError(err).Error("Failed to decrypt channel config")
		return err
	}

	sendCtx, cancel := context.WithTimeout(ctx, DispatchTimeout)
	defer cancel()

	attempt := deliveryAttempt(msg)
	err = p.Send(sendCtx, plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID:     envelope.ChannelID,
			Name:   channel.Name,
			Config: configMap,
		},
		Event:     envelope.Event,
		EventType: envelope.EventType,
		Attempt:   attempt,
	})
	if err != nil {
		entry.WithError(err).WithField("attempt", attempt).Warn("Plugin Send failed; will retry per BackOff schedule")
		return err
	}

	entry.WithField("attempt", attempt).Debug("Notification delivered")
	return nil
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

	row := c.db.QueryRowContext(ctx, `
		SELECT name, type, config, is_active
		FROM alert_channels
		WHERE id = $1
	`, id)

	var channel loadedChannel
	var cfgBytes []byte
	if err := row.Scan(&channel.Name, &channel.Type, &cfgBytes, &channel.IsActive); err != nil {
		return nil, err
	}
	channel.Config = json.RawMessage(cfgBytes)
	return &channel, nil
}

// deliveryAttempt extracts the JetStream NumDelivered counter from the
// per-message headers, defaulting to 1. Used to populate
// plugin.DispatchRequest.Attempt for plugins that vary behavior on retry
// (e.g. exponential rate limiting on their side).
func deliveryAttempt(msg *queue.Message) int {
	if msg == nil || msg.Headers == nil {
		return 1
	}
	// JetStream surfaces the delivery count via the "Nats-Num-Delivered"
	// header on some client versions; older clients omit it. Treat as 1 if
	// unparseable.
	values, ok := msg.Headers["Nats-Num-Delivered"]
	if !ok || len(values) == 0 {
		return 1
	}
	var n int
	if _, err := fmt.Sscanf(values[0], "%d", &n); err != nil || n < 1 {
		return 1
	}
	return n
}
