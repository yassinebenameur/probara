package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/worker/internal/worker"
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

	// Initialize status update publisher (optional)
	statusPublisher, err := statusupdates.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize status update publisher")
	} else {
		defer statusPublisher.Close()
	}

	// Create worker
	w := worker.NewWorker(cfg, log, metricsRegistry, dbClient, queueClient, statusPublisher)

	// Start minimal HTTP server for health/metrics
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
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

	// Start worker in a goroutine
	go func() {
		if err := w.Start(); err != nil {
			log.WithError(err).Fatal("Worker failed")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down worker...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Worker forced to shutdown")
		os.Exit(1)
	}

	log.Info("Worker exited")
}
