package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/api/internal/api"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
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

	// Wire the SMTP backend into the email plugin. The API never dispatches
	// real alerts — that is the alerter's job — but POST
	// /alert-channels/{id}/test invokes the same plugin Send path, so without a
	// mailer here every email channel test fails with "mailer not configured"
	// even when delivery works end to end.
	if cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		smtpMailer, err := email.NewSMTPMailer(email.SMTPParams{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
			UseTLS:   cfg.SMTPUseTLS,
		})
		if err != nil {
			log.WithError(err).Warn("Failed to configure SMTP mailer; email channel tests will fail")
		} else {
			email.SetMailer(smtpMailer)
		}
	} else {
		log.Warn("SMTP not configured; email channel tests will report mailer not configured")
	}

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
