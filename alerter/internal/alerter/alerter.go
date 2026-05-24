package alerter

import (
	"context"
	"fmt"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Alerter represents the alerter service
type Alerter struct {
	config    *config.AlerterConfig
	logger    *logger.Logger
	metrics   *metrics.Registry
	db        *db.Client
	nats      *queue.Client
	encryptor secrets.Encryptor
	stop      chan struct{}
}

// NewAlerter creates a new alerter instance. A nil encryptor is treated as a
// NoOp passthrough — channels persisted in plaintext continue to dispatch.
func NewAlerter(cfg *config.AlerterConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client, natsClient *queue.Client, encryptor secrets.Encryptor) *Alerter {
	if encryptor == nil {
		encryptor = secrets.NoOpEncryptor{}
	}
	return &Alerter{
		config:    cfg,
		logger:    log,
		metrics:   metricsRegistry,
		db:        dbClient,
		nats:      natsClient,
		encryptor: encryptor,
		stop:      make(chan struct{}),
	}
}

// Start starts the alerter evaluation loop
func (a *Alerter) Start() error {
	a.logger.Info("Starting alerter evaluation loop")

	if a.db == nil {
		return fmt.Errorf("database client is required")
	}

	if a.nats != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := a.nats.EnsureStream(ctx, a.config.AlertStream, []string{a.config.AlertSubject + ".*"}); err != nil {
			a.logger.WithError(err).Warn("Failed to ensure ALERTS stream")
		}
		if a.config.AsyncDispatch {
			// 24h max age — well past the 5-attempt BackOff [10s, 30s, 2m, 10m, 30m]
			// so messages don't expire mid-retry.
			if _, err := a.nats.EnsureWorkQueueStream(ctx, a.config.NotificationsStream, []string{a.config.NotificationsSubjectGlob}, 24*time.Hour); err != nil {
				a.logger.WithError(err).Fatal("Failed to ensure NOTIFICATIONS stream — required for ALERTER_ASYNC_DISPATCH=true")
			}
			a.logger.WithFields(map[string]interface{}{
				"stream":  a.config.NotificationsStream,
				"subject": a.config.NotificationsSubjectGlob,
			}).Info("Async notification dispatch enabled")
		}
	} else {
		a.logger.Warn("NATS client is nil, alert events will not be published")
		if a.config.AsyncDispatch {
			return fmt.Errorf("ALERTER_ASYNC_DISPATCH=true requires NATS_URL")
		}
	}

	evalInterval := time.Duration(a.config.AlertEvalIntervalSeconds) * time.Second
	if evalInterval <= 0 {
		evalInterval = 30 * time.Second
	}
	ticker := time.NewTicker(evalInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stop:
			a.logger.Info("Alerter loop stopped")
			return nil
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), evalInterval)
			if err := a.evaluateAlerts(ctx); err != nil {
				a.logger.WithError(err).Error("Alert evaluation failed")
			}
			cancel()
		}
	}
}

// Shutdown gracefully shuts down the alerter
func (a *Alerter) Shutdown(ctx context.Context) error {
	a.logger.Info("Shutting down alerter")
	close(a.stop)

	// Wait for loop to finish or timeout
	done := make(chan struct{})
	go func() {
		time.Sleep(1 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
