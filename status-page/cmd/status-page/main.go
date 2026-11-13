package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/yassinebenameur/probara/shared/bootstrap"
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

	// Health, readiness and metrics (separate port, used by Docker
	// healthchecks). The status page holds no JetStream client.
	go bootstrap.Serve(log, "metrics", cfg.MetricsPort, bootstrap.OpsMux(metricsRegistry,
		bootstrap.Healthz(nil), bootstrap.Readyz(log, bootstrap.DatabaseCheck(dbClient))))

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start server")
		}
	}()

	bootstrap.WaitForShutdownSignal()
	log.Info("Shutting down server...")

	ctx, cancel := bootstrap.ShutdownContext()
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Server forced to shutdown")
		os.Exit(1)
	}

	log.Info("Server exited")
}
