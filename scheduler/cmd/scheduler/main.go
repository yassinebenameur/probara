package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/scheduler/internal/ingest"
	"github.com/yassinebenameur/probara/scheduler/internal/scheduler"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
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
	var secretsEncryptor secrets.Encryptor = secrets.NoOpEncryptor{}
	if kp, kerr := secrets.NewEnvKeyProvider(); kerr == nil {
		secretsEncryptor = secrets.NewAESGCMEncryptor(kp)
	} else if kerr != secrets.ErrKeyNotConfigured {
		log.WithError(kerr).Fatal("Invalid PROBARA_SECRETS_KEY")
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

	// Start minimal HTTP server for health/metrics
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Check database connection
			if err := dbClient.HealthCheck(ctx); err != nil {
				log.WithError(err).Debug("Database health check failed")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Database unavailable"))
				return
			}

			// Check NATS connection
			if err := queueClient.HealthCheck(ctx); err != nil {
				log.WithError(err).Debug("NATS health check failed")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("NATS unavailable"))
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		mux.HandleFunc("/metrics", metricsRegistry.Handler().ServeHTTP)

		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
			Handler: mux,
		}

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start metrics server")
		}
	}()

	// Start scheduler in a goroutine
	go func() {
		if err := sched.Start(); err != nil {
			log.WithError(err).Fatal("Scheduler failed")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down scheduler...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
