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
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/statusupdates"
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

	// Wire SMTP backend into the email plugin so the notifications consumer
	// can dispatch email alerts. No-op when SMTP is unset — email plugin Send
	// will return an explicit error and JetStream will retry until the
	// operator wires SMTP in.
	if cfg.NotificationsEnabled && cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		smtpMailer, err := email.NewSMTPMailer(email.SMTPParams{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
			UseTLS:   cfg.SMTPUseTLS,
		})
		if err != nil {
			log.WithError(err).Warn("Failed to configure worker SMTP mailer; email alerts will fail until SMTP is fixed")
		} else {
			email.SetMailer(smtpMailer)
		}
	} else if cfg.NotificationsEnabled {
		log.Warn("Notifications consumer enabled but SMTP not configured; email channels will fail")
	}

	// Secrets encryption — same provider the API uses so what API encrypted,
	// the worker can decrypt.
	var secretsEncryptor secrets.Encryptor = secrets.NoOpEncryptor{}
	if kp, kerr := secrets.NewEnvKeyProvider(); kerr == nil {
		secretsEncryptor = secrets.NewAESGCMEncryptor(kp)
	} else if kerr != secrets.ErrKeyNotConfigured {
		log.WithError(kerr).Fatal("Invalid PROBARA_SECRETS_KEY")
	} else if cfg.NotificationsEnabled {
		log.Warn("PROBARA_SECRETS_KEY not set; worker will only handle plaintext channel configs")
	}
	w.ConfigureEncryption(secretsEncryptor)

	// Start notifications consumer if enabled — runs in its own goroutine and
	// shares the queue client + db client with the check-worker loop.
	var notifConsumer *notifications.Consumer
	if cfg.NotificationsEnabled {
		notifConsumer = notifications.New(cfg, log, dbClient, queueClient, secretsEncryptor)
		go func() {
			ctx := context.Background()
			if err := notifConsumer.Start(ctx); err != nil && err != context.Canceled {
				log.WithError(err).Error("Notifications consumer exited with error")
			}
		}()
	}

	// Start minimal HTTP server for health/metrics
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", healthzHandler(queueClient))
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

// healthzHandler reports the pod as dead once the NATS connection is
// permanently closed, so Kubernetes restarts it instead of leaving a zombie
// that can never consume jobs again. A reconnecting connection is healthy.
func healthzHandler(queueClient *queue.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if queueClient != nil && queueClient.Closed() {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("NATS connection permanently closed"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}
