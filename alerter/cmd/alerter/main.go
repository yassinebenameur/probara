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
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
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

	// Wire SMTP backend into the email alert plugin.
	if cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		smtpMailer, err := email.NewSMTPMailer(email.SMTPParams{
			Host:             cfg.SMTPHost,
			Port:             cfg.SMTPPort,
			Username:         cfg.SMTPUsername,
			Password:         cfg.SMTPPassword,
			From:             cfg.SMTPFrom,
			UseTLS:           cfg.SMTPUseTLS,
			DefaultRecipient: cfg.AlertEmailTo,
		})
		if err != nil {
			log.WithError(err).Warn("Failed to configure SMTP mailer, email alerts will be skipped")
		} else {
			email.SetMailer(smtpMailer)
		}
	} else {
		log.Warn("SMTP not configured; email alert plugin will reject Send")
	}

	// Secrets encryption — required to dispatch encrypted channel configs.
	var secretsEncryptor secrets.Encryptor = secrets.NoOpEncryptor{}
	if kp, kerr := secrets.NewEnvKeyProvider(); kerr == nil {
		secretsEncryptor = secrets.NewAESGCMEncryptor(kp)
	} else if kerr != secrets.ErrKeyNotConfigured {
		log.WithError(kerr).Fatal("Invalid PROBARA_SECRETS_KEY")
	} else {
		log.Warn("PROBARA_SECRETS_KEY not set; alerter will only handle plaintext channel configs")
	}

	// Create alerter
	alert := alerter.NewAlerter(cfg, log, metricsRegistry, dbClient, natsClient, secretsEncryptor)

	// Start minimal HTTP server for health/metrics
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", healthzHandler(natsClient))
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

// healthzHandler reports the pod as dead once the NATS connection is
// permanently closed, so Kubernetes restarts it instead of leaving a zombie
// alerter. A nil client (NATS disabled) or a reconnecting connection is
// healthy.
func healthzHandler(natsClient *queue.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if natsClient != nil && natsClient.Closed() {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("NATS connection permanently closed"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}
