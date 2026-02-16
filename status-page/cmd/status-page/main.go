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
	"github.com/yassinebenameur/probara/status-page/internal/statuspage"
)

func main() {
	// Load configuration
	cfg, err := config.LoadStatusPageConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New(cfg.ServiceName, cfg.LogLevel)
	log.Info("Starting status page service")

	// Initialize metrics
	metricsRegistry := metrics.NewRegistry(cfg.ServiceName)

	// Initialize database client
	if cfg.PostgresURL == "" {
		fmt.Fprintf(os.Stderr, "POSTGRES_URL environment variable is required\n")
		os.Exit(1)
	}

	dbClient, err := db.NewClient(cfg.PostgresURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize database client: %v\n", err)
		os.Exit(1)
	}
	defer dbClient.Close()

	// Create server
	server := statuspage.NewServer(cfg, log, metricsRegistry, dbClient)

	// Start metrics/health server (separate port, used by Docker healthchecks)
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

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		mux.HandleFunc("/metrics", metricsRegistry.Handler().ServeHTTP)

		srv := &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
			Handler: mux,
		}

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start metrics server")
		}
	}()

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start server")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Server forced to shutdown")
		os.Exit(1)
	}

	log.Info("Server exited")
}
