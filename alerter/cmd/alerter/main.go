package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/alerter/internal/alerter"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
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

	// Initialize mailer (optional)
	var mailer alerter.Mailer
	if cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		smtpMailer, err := alerter.NewSMTPMailer(cfg)
		if err != nil {
			log.WithError(err).Warn("Failed to configure SMTP mailer, email alerts will be skipped")
		} else {
			mailer = smtpMailer
		}
	}

	// Create alerter
	alert := alerter.NewAlerter(cfg, log, metricsRegistry, dbClient, natsClient, mailer)

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

			if err := dbClient.HealthCheck(ctx); err != nil {
				log.WithError(err).Debug("Database health check failed")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Database unavailable"))
				return
			}

			if natsClient != nil {
				if err := natsClient.HealthCheck(ctx); err != nil {
					log.WithError(err).Debug("NATS health check failed")
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("NATS unavailable"))
					return
				}
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

	// Start alerter in a goroutine
	go func() {
		if err := alert.Start(); err != nil {
			log.WithError(err).Fatal("Alerter failed")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down alerter...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
