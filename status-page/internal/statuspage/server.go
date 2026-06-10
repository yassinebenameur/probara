package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

// Server represents the status page HTTP server
type Server struct {
	config       *config.StatusPageConfig
	logger       *logger.Logger
	metrics      *metrics.Registry
	db           *db.Client
	http         *http.Server
	subscriber   *Subscriber
	sseConnected prometheus.Gauge
}

// NewServer creates a new status page server
func NewServer(cfg *config.StatusPageConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client) *Server {
	mux := http.NewServeMux()

	// Initialize service and handlers
	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	service := NewService(dbClient, analyticsRepo)
	hub := NewHub()
	renderCache := newRenderCache(renderCacheTTLFromEnv())
	handlers := NewHandlers(service, cfg, log, hub, renderCache)

	// 1 while the NATS subscriber that drives SSE updates and render-cache
	// invalidation is connected; 0 when it failed to start (pages then go
	// stale up to the render-cache TTL and live updates are off).
	sseConnected := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "statuspage_sse_subscriber_connected",
		Help: "1 when the NATS status-update subscriber is connected, 0 otherwise.",
	})
	metricsRegistry.GetRegistry().MustRegister(sseConnected)
	sseConnected.Set(0)

	// Start NATS subscriber for live updates and render-cache invalidation.
	// Without it the page still works, but SSE is dead and cached HTML is only
	// refreshed by TTL — loud failure, not a Warn-and-forget.
	var subscriber *Subscriber
	if cfg.NATSURL != "" {
		if sub, err := NewSubscriber(cfg.NATSURL, hub, dbClient, log, renderCache); err != nil {
			log.WithError(err).Error("Failed to initialize status update subscriber; live updates and event-driven cache invalidation are disabled")
		} else {
			if err := sub.Start(); err != nil {
				log.WithError(err).Error("Failed to start status update subscriber; live updates and event-driven cache invalidation are disabled")
			} else {
				subscriber = sub
				sseConnected.Set(1)
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

	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 60 * time.Second
	}

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		config:       cfg,
		logger:       log,
		metrics:      metricsRegistry,
		db:           dbClient,
		http:         httpServer,
		subscriber:   subscriber,
		sseConnected: sseConnected,
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
		if s.sseConnected != nil {
			s.sseConnected.Set(0)
		}
	}
	return s.http.Shutdown(ctx)
}
