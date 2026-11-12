package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yassinebenameur/probara/api/internal/api"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
)

func main() {
	// Load configuration
	cfg, err := config.LoadAPIConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New(cfg.ServiceName, cfg.LogLevel)
	log.Info("Starting API service")

	// Initialize database
	if cfg.PostgresURL == "" {
		log.Fatal("POSTGRES_URL is required")
	}

	dbClient, err := db.NewClient(cfg.PostgresURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to connect to database")
	}
	defer dbClient.Close()

	// Run migrations
	migrationsPath := filepath.Join("shared", "db", "migrations")
	if err := db.Migrate(dbClient.DB, migrationsPath); err != nil {
		log.WithError(err).Fatal("Failed to run migrations")
	}
	log.Info("Database migrations completed")

	// Notification runtime. The API never dispatches real alerts — that is
	// the alerter's job — but POST /alert-channels/{id}/test invokes the same
	// plugin Send path, so without a mailer here every email channel test
	// fails with "mailer not configured" even when delivery works end to end.
	bootstrap.ConfigureNotifications(log, cfg.SMTPConfig, cfg.NotificationEgressConfig, bootstrap.NotificationOptions{
		Mailer:  true,
		Purpose: "email channel tests",
	})

	// Initialize metrics
	metricsRegistry := metrics.NewRegistry(cfg.ServiceName)

	// Create server
	server := api.NewServer(cfg, log, metricsRegistry, dbClient)

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
