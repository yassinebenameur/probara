package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/worker/internal/airca"
	"github.com/yassinebenameur/probara/worker/internal/worker"
	"github.com/yassinebenameur/probara/worker/internal/worker/notifications"
)

func main() {
	// Load configuration
	cfg, err := config.LoadWorkerConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New(cfg.ServiceName, cfg.LogLevel)
	log.Info("Starting worker service")

	// Initialize metrics
	metricsRegistry := metrics.NewRegistry(cfg.ServiceName)

	// Initialize database client — optional. The check path publishes results
	// over NATS and never touches Postgres; only the notifications, AI-RCA and
	// AI triage side consumers need a DB. Remote location workers run without one.
	var dbClient *db.Client
	if cfg.PostgresURL != "" {
		dbClient, err = db.NewClient(cfg.PostgresURL)
		if err != nil {
			log.WithError(err).Fatal("Failed to initialize database client")
		}
		defer dbClient.Close()
	} else {
		log.Info("POSTGRES_URL not set; notifications, AI-RCA and AI triage consumers disabled (expected for location workers)")
	}

	// Initialize NATS queue client
	queueClient, err := queue.NewClient(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize NATS client")
	}
	defer queueClient.Close()

	// Create worker
	w := worker.NewWorker(cfg, log, metricsRegistry, queueClient)

	// Notification runtime: egress policy and deep-link origin always; the
	// SMTP mailer only when this worker runs the notifications consumer.
	// Without SMTP the email plugin's Send returns an explicit error and
	// JetStream retries until the operator wires SMTP in.
	bootstrap.ConfigureNotifications(log, cfg.SMTPConfig, cfg.NotificationEgressConfig, bootstrap.NotificationOptions{
		Mailer:  cfg.NotificationsEnabled,
		Purpose: "email alerts",
	})

	// Private-location workers derive a location-only config key from their
	// credential. Default-fleet workers use the platform at-rest key.
	var secretsEncryptor secrets.Encryptor
	if cfg.LocationID != "" {
		secretsEncryptor, err = locationauth.ConfigEncryptor(cfg.LocationCredential)
		if err != nil {
			log.WithError(err).Fatal("Invalid LOCATION_CREDENTIAL")
		}
	} else {
		var keyConfigured bool
		secretsEncryptor, keyConfigured, err = bootstrap.PlatformEncryptor()
		if err != nil {
			log.WithError(err).Fatal("Invalid PROBARA_SECRETS_KEY")
		}
		if !keyConfigured && cfg.NotificationsEnabled {
			log.Warn("PROBARA_SECRETS_KEY not set; worker will only handle plaintext channel configs")
		}
	}
	w.ConfigureEncryption(secretsEncryptor)

	// Side consumers (notifications, AI-RCA, AI triage) run on DB-connected
	// workers under RunWithRestart, so a Consume error or a dropped stream
	// restarts them with backoff instead of leaving a queue silently
	// unconsumed while the pod still reports healthy. Cancelled on shutdown;
	// sideWG lets main wait for them (bounded) before its deferred DB/NATS
	// Close calls, so an in-flight NAK or write lands.
	sideCtx, sideCancel := context.WithCancel(context.Background())
	defer sideCancel()
	var sideWG sync.WaitGroup

	if cfg.NotificationsEnabled && dbClient == nil {
		log.Warn("NOTIFICATIONS_ENABLED is set but POSTGRES_URL is not; notifications consumer disabled")
	}
	if cfg.NotificationsEnabled && dbClient != nil {
		notifConsumer := notifications.New(cfg, log, dbClient, queueClient, secretsEncryptor)
		sideWG.Add(1)
		go func() {
			defer sideWG.Done()
			airca.RunWithRestart(sideCtx, log, "notifications-consumer", airca.DefaultRestartPolicy(), notifConsumer.Start)
		}()
	}

	// Only database-connected platform workers resolve local AI; private
	// workers execute checks independently and never receive its credentials.
	var envAnalyzer ai.RootCauseAnalyzer
	if dbClient != nil && !cfg.LocalAI.Disabled {
		a, aerr := ai.NewAnalyzer(cfg.LocalAI)
		if aerr != nil {
			log.WithError(aerr).Warn("Local AI configuration unavailable")
		} else {
			envAnalyzer = a
		}
	}

	if dbClient != nil {
		if err := airca.ValidateSweepWindow(cfg.LocalAI.Timeout); err != nil {
			log.WithError(err).Fatal("Invalid local AI timeout")
		}
		aircaConsumer := airca.New(cfg, log, dbClient, queueClient, envAnalyzer)
		sideWG.Add(2)
		go func() {
			defer sideWG.Done()
			airca.RunWithRestart(sideCtx, log, "ai-rca-consumer", airca.DefaultRestartPolicy(), aircaConsumer.Start)
		}()
		go func() {
			defer sideWG.Done()
			airca.RunWithRestart(sideCtx, log, "ai-rca-stale-sweep", airca.DefaultRestartPolicy(), func(ctx context.Context) error {
				return airca.RunStaleSweep(ctx, log, dbClient, airca.SweepInterval)
			})
		}()

		// Automatic alert triage (never changes paging): the relay claims
		// open alerts and publishes AI_TRIAGE jobs; the triage consumer
		// analyses them.
		// Platform workers only. Both keep running with AI_TRIAGE_ENABLED=false
		// or LOCAL_AI_DISABLED=true so already-claimed rows drain (skipped,
		// without a model call) instead of showing as pending forever. Without
		// an analyzer (rejected client config) the relay claims nothing.
		if cfg.LocationID == "" {
			triage := airca.NewTriageConsumer(cfg, log, dbClient, queueClient, envAnalyzer)
			relay := airca.NewRelay(cfg, log, dbClient, queueClient, envAnalyzer != nil)
			sideWG.Add(2)
			go func() {
				defer sideWG.Done()
				airca.RunWithRestart(sideCtx, log, "ai-triage-consumer", airca.DefaultRestartPolicy(), triage.Start)
			}()
			go func() {
				defer sideWG.Done()
				airca.RunWithRestart(sideCtx, log, "ai-triage-relay", airca.DefaultRestartPolicy(), relay.Run)
			}()
		}
	}

	// Health, readiness and metrics, plus the mesh echo. Location workers run
	// without Postgres, so the database is probed only when configured.
	healthz := bootstrap.Healthz(queueClient)
	readyChecks := []bootstrap.ReadyCheck{}
	if dbClient != nil {
		readyChecks = append(readyChecks, bootstrap.DatabaseCheck(dbClient))
	}
	readyChecks = append(readyChecks, bootstrap.QueueCheck(queueClient))
	opsMux := bootstrap.OpsMux(metricsRegistry, healthz, bootstrap.Readyz(log, readyChecks...))
	opsMux.HandleFunc(models.MeshEchoPath, worker.MeshEchoHandler(cfg.LocationID))
	go bootstrap.Serve(log, "metrics", cfg.MetricsPort, opsMux)

	// The mesh echo endpoint also gets a dedicated listener when HTTP_PORT
	// differs from METRICS_PORT, so deployments can expose the echo across
	// networks without exposing /metrics. Equal ports (the helm default)
	// means the shared mux above already serves it.
	if cfg.HTTPPort != cfg.MetricsPort {
		echoMux := http.NewServeMux()
		echoMux.HandleFunc(models.MeshEchoPath, worker.MeshEchoHandler(cfg.LocationID))
		echoMux.HandleFunc("/healthz", healthz)
		go bootstrap.Serve(log, "mesh echo", cfg.HTTPPort, echoMux)
	}

	// Start worker in a goroutine
	go func() {
		if err := w.Start(); err != nil {
			log.WithError(err).Fatal("Worker failed")
		}
	}()

	bootstrap.WaitForShutdownSignal()
	log.Info("Shutting down worker...")
	sideCancel()

	ctx, cancel := bootstrap.ShutdownContext()
	defer cancel()

	if err := w.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Worker forced to shutdown")
		os.Exit(1)
	}

	sideDone := make(chan struct{})
	go func() {
		sideWG.Wait()
		close(sideDone)
	}()
	select {
	case <-sideDone:
	case <-ctx.Done():
		log.Warn("Side consumers did not stop within the shutdown window")
	}

	log.Info("Worker exited")
}
