package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
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

	// Process-level plugin runtime: deep-link origin and the egress (SSRF)
	// policy every notification channel dials through.
	plugin.Configure(plugin.Runtime{
		AppBaseURL: cfg.AppBaseURL,
		Egress: netguard.Policy{
			BlockPrivate: cfg.NotificationBlockPrivateIPs,
			AllowedCIDRs: cfg.NotificationAllowedCIDRs,
		},
	})

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

	// AI-RCA consumer and its stale-pending sweep run on DB-connected workers
	// under RunWithRestart, so a Consume error or a dropped stream restarts
	// them with backoff instead of leaving the queue silently unconsumed.
	// Cancelled on shutdown; aiWG lets main wait for them (bounded) before
	// its deferred DB/NATS Close calls, so an in-flight NAK or write lands.
	aiCtx, aiCancel := context.WithCancel(context.Background())
	defer aiCancel()
	var aiWG sync.WaitGroup
	if dbClient != nil {
		if err := airca.ValidateSweepWindow(cfg.LocalAI.Timeout); err != nil {
			log.WithError(err).Fatal("Invalid local AI timeout")
		}
		aircaConsumer := airca.New(cfg, log, dbClient, queueClient, envAnalyzer)
		aiWG.Add(2)
		go func() {
			defer aiWG.Done()
			airca.RunWithRestart(aiCtx, log, "ai-rca-consumer", airca.DefaultRestartPolicy(), aircaConsumer.Start)
		}()
		go func() {
			defer aiWG.Done()
			airca.RunWithRestart(aiCtx, log, "ai-rca-stale-sweep", airca.DefaultRestartPolicy(), func(ctx context.Context) error {
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
			aiWG.Add(2)
			go func() {
				defer aiWG.Done()
				airca.RunWithRestart(aiCtx, log, "ai-triage-consumer", airca.DefaultRestartPolicy(), triage.Start)
			}()
			go func() {
				defer aiWG.Done()
				airca.RunWithRestart(aiCtx, log, "ai-triage-relay", airca.DefaultRestartPolicy(), relay.Run)
			}()
		}
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
	aiCancel()

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Worker forced to shutdown")
		os.Exit(1)
	}

	aiDone := make(chan struct{})
	go func() {
		aiWG.Wait()
		close(aiDone)
	}()
	select {
	case <-aiDone:
	case <-ctx.Done():
		log.Warn("AI RCA consumer did not stop within the shutdown window")
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
