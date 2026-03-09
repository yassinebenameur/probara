package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

// Server represents the status page HTTP server
type Server struct {
	config     *config.StatusPageConfig
	logger     *logger.Logger
	metrics    *metrics.Registry
	db         *db.Client
	http       *http.Server
	subscriber *Subscriber
}

// NewServer creates a new status page server
func NewServer(cfg *config.StatusPageConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client) *Server {
	mux := http.NewServeMux()

	// Initialize service and handlers
	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	service := NewService(dbClient, analyticsRepo)
	hub := NewHub()
	handlers := NewHandlers(service, cfg, log, hub)

	// Start NATS subscriber for live updates (optional)
	var subscriber *Subscriber
	if cfg.NATSURL != "" {
		if sub, err := NewSubscriber(cfg.NATSURL, hub, log); err != nil {
			log.WithError(err).Warn("Failed to initialize status update subscriber")
		} else {
			if err := sub.Start(); err != nil {
				log.WithError(err).Warn("Failed to start status update subscriber")
			} else {
				subscriber = sub
			}
		}
	}

	// Register routes
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/metrics", metricsRegistry.Handler().ServeHTTP)

	// API proxy for the in-page status page customizer (optional via STATUS_PAGE_API_BASE_URL)
	mux.HandleFunc("/_sp_api/", handlers.HandleAPIProxy)

	// Public status page routes
	mux.HandleFunc("/public/status/", handlers.HandleStatusPage)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		config:     cfg,
		logger:     log,
		metrics:    metricsRegistry,
		db:         dbClient,
		http:       httpServer,
		subscriber: subscriber,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.logger.WithFields(map[string]interface{}{
		"port": s.config.HTTPPort,
	}).Info("Starting HTTP server")

	return s.http.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server")
	if s.subscriber != nil {
		s.subscriber.Close()
	}
	return s.http.Shutdown(ctx)
}
