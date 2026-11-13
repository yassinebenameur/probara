package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yassinebenameur/probara/scheduler/internal/ingest"
	"github.com/yassinebenameur/probara/scheduler/internal/scheduler"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

func main() {
	// Load configuration
	cfg, err := config.LoadSchedulerConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New(cfg.ServiceName, cfg.LogLevel)
	log.Info("Starting scheduler service")

	// Initialize metrics
	metricsRegistry := metrics.NewRegistry(cfg.ServiceName)

	// Initialize database client
	dbClient, err := db.NewClient(cfg.PostgresURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize database client")
	}
	defer dbClient.Close()

	// Initialize NATS queue client
	queueClient, err := queue.NewClient(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize NATS client")
	}
	defer queueClient.Close()

	// Create scheduler
	sched := scheduler.NewScheduler(cfg, log, metricsRegistry, dbClient, queueClient)
	secretsEncryptor, _, err := bootstrap.PlatformEncryptor()
	if err != nil {
		log.WithError(err).Fatal("Invalid PROBARA_SECRETS_KEY")
	}
	sched.ConfigureEncryption(secretsEncryptor)

	// Status updates reach the API SSE stream and status pages; shared by the
	// results ingest and the absence watchdog.
	statusPublisher, err := statusupdates.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize status update publisher; status pages won't live-update")
		statusPublisher = nil
	} else {
		defer statusPublisher.Close()
		sched.SetStatusPublisher(statusPublisher)
	}

	// Results ingest: persists check results workers publish over NATS and
	// advances monitor state. Workers themselves have no Postgres access.
	var ingestConsumer *ingest.Ingest
	if cfg.ResultIngestEnabled {
		ingestConsumer = ingest.New(cfg, log, metricsRegistry, dbClient, queueClient, statusPublisher)
		ingestConsumer.ConfigureEncryption(secretsEncryptor)
		go func() {
			if err := ingestConsumer.Start(); err != nil {
				log.WithError(err).Fatal("Results ingest failed")
			}
		}()
	} else {
		log.Warn("RESULT_INGEST_ENABLED=false; check results published by workers will not be persisted")
	}

	// Health, readiness and metrics.
	go bootstrap.Serve(log, "metrics", cfg.MetricsPort, bootstrap.OpsMux(metricsRegistry,
		bootstrap.Healthz(queueClient),
		bootstrap.Readyz(log, bootstrap.DatabaseCheck(dbClient), bootstrap.QueueCheck(queueClient)),
	))

	// Start scheduler in a goroutine
	// Shutdown cancels the scheduler's context, which makes Start return
	// context.Canceled: that is the normal exit, not a failure.
	go func() {
		if err := sched.Start(); err != nil && !errors.Is(err, context.Canceled) {
			log.WithError(err).Fatal("Scheduler failed")
		}
	}()

	bootstrap.WaitForShutdownSignal()
	log.Info("Shutting down scheduler...")

	ctx, cancel := bootstrap.ShutdownContext()
	defer cancel()

	if ingestConsumer != nil {
		if err := ingestConsumer.Shutdown(ctx); err != nil {
			log.WithError(err).Error("Results ingest forced to shutdown")
		}
	}

	if err := sched.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Scheduler forced to shutdown")
		os.Exit(1)
	}

	log.Info("Scheduler exited")
}
