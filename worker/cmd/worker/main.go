package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
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
	// over NATS and never touches Postgres; only the notifications and AI-RCA
	// side consumers need a DB. Remote location workers run without one.
	var dbClient *db.Client
	if cfg.PostgresURL != "" {
		dbClient, err = db.NewClient(cfg.PostgresURL)
		if err != nil {
			log.WithError(err).Fatal("Failed to initialize database client")
		}
		defer dbClient.Close()
	} else {
		log.Info("POSTGRES_URL not set; notifications and AI-RCA consumers disabled (expected for location workers)")
	}

	// Initialize NATS queue client
	queueClient, err := queue.NewClient(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize NATS client")
	}
	defer queueClient.Close()

	// Create worker
	w := worker.NewWorker(cfg, log, metricsRegistry, queueClient)

	// Wire SMTP backend into the email plugin so the notifications consumer
	// can dispatch email alerts. No-op when SMTP is unset — email plugin Send
	// will return an explicit error and JetStream will retry until the
	// operator wires SMTP in.
	if cfg.NotificationsEnabled && cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		smtpMailer, err := email.NewSMTPMailer(email.SMTPParams{
			Host:       cfg.SMTPHost,
			Port:       cfg.SMTPPort,
			Username:   cfg.SMTPUsername,
			Password:   cfg.SMTPPassword,
			From:       cfg.SMTPFrom,
			FromName:   cfg.SMTPFromName,
			UseTLS:     cfg.SMTPUseTLS,
			AppBaseURL: cfg.AppBaseURL,
		})
		if err != nil {
			log.WithError(err).Warn("Failed to configure worker SMTP mailer; email alerts will fail until SMTP is fixed")
		} else {
			email.SetMailer(smtpMailer)
		}
	} else if cfg.NotificationsEnabled {
		log.Warn("Notifications consumer enabled but SMTP not configured; email channels will fail")
	}

	// Private-location workers derive a location-only config key from their
	// credential. Default-fleet workers use the platform at-rest key.
	var secretsEncryptor secrets.Encryptor = secrets.NoOpEncryptor{}
	if cfg.LocationID != "" {
		secretsEncryptor, err = locationauth.ConfigEncryptor(cfg.LocationCredential)
		if err != nil {
			log.WithError(err).Fatal("Invalid LOCATION_CREDENTIAL")
		}
	} else if kp, kerr := secrets.NewEnvKeyProvider(); kerr == nil {
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
	if cfg.NotificationsEnabled && dbClient == nil {
		log.Warn("NOTIFICATIONS_ENABLED is set but POSTGRES_URL is not; notifications consumer disabled")
	}
	if cfg.NotificationsEnabled && dbClient != nil {
		notifConsumer = notifications.New(cfg, log, dbClient, queueClient, secretsEncryptor)
		go func() {
			ctx := context.Background()
			if err := notifConsumer.Start(ctx); err != nil && err != context.Canceled {
				log.WithError(err).Error("Notifications consumer exited with error")
			}
		}()
	}

	// Start the AI root cause consumer. It always runs (per-tenant ai_settings
	// rows may configure an LLM even with no env default); the env LLM, when
	// configured, is the fallback for tenants without their own row.
	var envAnalyzer ai.RootCauseAnalyzer
	switch a, aerr := ai.NewAnalyzer(ai.Config{
		Provider:  cfg.LLMProvider,
		BaseURL:   cfg.LLMBaseURL,
		APIKey:    cfg.LLMAPIKey,
		Model:     cfg.LLMModel,
		JSONMode:  cfg.LLMJSONMode,
		MaxTokens: cfg.LLMMaxTokens,
		Timeout:   time.Duration(cfg.LLMTimeoutSeconds) * time.Second,
	}); {
	case aerr == nil:
		envAnalyzer = a
		log.Info("AI root cause analysis: env LLM default configured")
	case errors.Is(aerr, ai.ErrNotConfigured):
		log.Info("AI root cause analysis: no env LLM default; tenants may configure their own")
	default:
		log.WithError(aerr).Warn("AI root cause analysis: invalid env LLM config; ignoring env default")
	}

	if dbClient != nil {
		aircaConsumer := airca.New(cfg, log, dbClient, queueClient, envAnalyzer, secretsEncryptor)
		go func() {
			ctx := context.Background()
			if err := aircaConsumer.Start(ctx); err != nil && err != context.Canceled {
				log.WithError(err).Error("AI root cause consumer exited with error")
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

			// Check database connection (only when configured — location
			// workers run without Postgres)
			if dbClient != nil {
				if err := dbClient.HealthCheck(ctx); err != nil {
					log.WithError(err).Debug("Database health check failed")
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Database unavailable"))
					return
				}
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
		mux.HandleFunc(models.MeshEchoPath, worker.MeshEchoHandler(cfg.LocationID))

		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
			Handler: mux,
		}

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start metrics server")
		}
	}()

	// The mesh echo endpoint also gets a dedicated listener when HTTP_PORT
	// differs from METRICS_PORT, so deployments can expose the echo across
	// networks without exposing /metrics. Equal ports (the helm default)
	// means the shared mux above already serves it.
	if cfg.HTTPPort != cfg.MetricsPort {
		go func() {
			echoMux := http.NewServeMux()
			echoMux.HandleFunc(models.MeshEchoPath, worker.MeshEchoHandler(cfg.LocationID))
			echoMux.HandleFunc("/healthz", healthzHandler(queueClient))

			server := &http.Server{
				Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
				Handler: echoMux,
			}

			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.WithError(err).Fatal("Failed to start mesh echo server")
			}
		}()
	}

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
