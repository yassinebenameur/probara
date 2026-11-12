package main

import (
	"fmt"
	"os"

	"github.com/yassinebenameur/probara/alerter/internal/alerter"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/queue"
)

func main() {
	// Load configuration
	cfg, err := config.LoadAlerterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New(cfg.ServiceName, cfg.LogLevel)
	log.Info("Starting alerter service")

	// Initialize metrics
	metricsRegistry := metrics.NewRegistry(cfg.ServiceName)

	// Initialize database client
	dbClient, err := db.NewClient(cfg.PostgresURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize database client")
	}

	// Initialize NATS queue client (optional)
	var natsClient *queue.Client
	if cfg.NATSURL != "" {
		client, err := queue.NewClient(cfg.NATSURL)
		if err != nil {
			log.WithError(err).Warn("Failed to initialize NATS client, alert events will be skipped")
		} else {
			natsClient = client
		}
	}

	// Notification runtime: egress policy, deep-link origin and the SMTP
	// mailer the alerter delivers alert email through.
	bootstrap.ConfigureNotifications(log, cfg.SMTPConfig, cfg.NotificationEgressConfig, bootstrap.NotificationOptions{
		Mailer:           true,
		DefaultRecipient: cfg.AlertEmailTo,
		Purpose:          "email alerts",
	})

	// Secrets encryption — required to dispatch encrypted channel configs.
	secretsEncryptor, keyConfigured, err := bootstrap.PlatformEncryptor()
	if err != nil {
		log.WithError(err).Fatal("Invalid PROBARA_SECRETS_KEY")
	}
	if !keyConfigured {
		log.Warn("PROBARA_SECRETS_KEY not set; alerter will only handle plaintext channel configs")
	}

	// Create alerter
	alert := alerter.NewAlerter(cfg, log, metricsRegistry, dbClient, natsClient, secretsEncryptor)

	// Health, readiness and metrics. NATS is optional for the alerter, so it
	// is probed only when a client exists.
	readyChecks := []bootstrap.ReadyCheck{bootstrap.DatabaseCheck(dbClient)}
	if natsClient != nil {
		readyChecks = append(readyChecks, bootstrap.QueueCheck(natsClient))
	}
	go bootstrap.Serve(log, "metrics", cfg.MetricsPort, bootstrap.OpsMux(metricsRegistry,
		bootstrap.Healthz(natsClient), bootstrap.Readyz(log, readyChecks...)))

	// Start alerter in a goroutine
	go func() {
		if err := alert.Start(); err != nil {
			log.WithError(err).Fatal("Alerter failed")
		}
	}()

	bootstrap.WaitForShutdownSignal()
	log.Info("Shutting down alerter...")

	ctx, cancel := bootstrap.ShutdownContext()
	defer cancel()

	if err := alert.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Alerter forced to shutdown")
		os.Exit(1)
	}

	if natsClient != nil {
		natsClient.Close()
	}
	if err := dbClient.Close(); err != nil {
		log.WithError(err).Warn("Failed to close database connection")
	}

	log.Info("Alerter exited")
}
