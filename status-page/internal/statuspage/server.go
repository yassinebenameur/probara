package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/clientip"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/securityheaders"
)

// Server represents the status page HTTP server
type Server struct {
	config       *config.StatusPageConfig
	logger       *logger.Logger
	metrics      *metrics.Registry
	db           *db.Client
	http         *http.Server
	subscriber   *Subscriber
	pushSender   *pushSender
	sseConnected prometheus.Gauge
}

// NewServer creates a new status page server
func NewServer(cfg *config.StatusPageConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client) *Server {
	// Initialize service and handlers
	analyticsRepo := sharedanalytics.NewRepository(dbClient)
	service := NewService(dbClient, analyticsRepo)
	hub := NewHub()
	renderCache := newRenderCache(renderCacheTTLFromEnv())

	// The push store is the package's only Postgres write path. It is created
	// only when the deployment has VAPID keys, so a deployment without them
	// keeps the service strictly read-only and 404s the push routes.
	var pushes *pushStore
	if cfg.WebPushConfigured() {
		pushes = newPushStore(dbClient.DB)
	}
	if cfg.LegacyTrustedProxy {
		log.Warn("STATUS_PAGE_TRUSTED_PROXY is deprecated and trusts X-Forwarded-For from any peer; set TRUSTED_PROXY_CIDRS to your proxies' ranges instead")
	}
	handlers := NewHandlers(service, cfg, log, hub, renderCache, pushes)
	warnUntrustedProxyWithGatedPages(cfg, service, log)

	// The push sender owns the reconcile-and-send loop. It exists only when
	// VAPID keys are configured, so an unconfigured deployment starts no loop
	// and never writes.
	var pushSend *pushSender
	if pushes != nil {
		pushSend = newPushSender(pushes, cfg, log)
		pushSend.Start()
	}

	// 1 while the NATS subscriber that drives SSE updates and render-cache
	// invalidation is connected; 0 when it failed to start (pages then go
	// stale up to the render-cache TTL and live updates are off).
	// Full name: probara_status_page_sse_subscriber_connected.
	sseConnected := metricsRegistry.NewGauge(
		"sse_subscriber_connected",
		"1 when the NATS status-update subscriber is connected, 0 otherwise.",
		nil,
	).WithLabelValues()
	sseConnected.Set(0)

	// Start NATS subscriber for live updates and render-cache invalidation.
	// Without it the page still works, but SSE is dead and cached HTML is only
	// refreshed by TTL — loud failure, not a Warn-and-forget.
	var subscriber *Subscriber
	if cfg.NATSURL != "" {
		if sub, err := NewSubscriber(cfg.NATSURL, hub, dbClient, log, renderCache, pushSend); err != nil {
			log.WithError(err).Error("Failed to initialize status update subscriber; live updates and event-driven cache invalidation are disabled")
		} else {
			if handlers.hosts != nil {
				sub.hosts = handlers.hosts
			}
			if err := sub.Start(); err != nil {
				log.WithError(err).Error("Failed to start status update subscriber; live updates and event-driven cache invalidation are disabled")
			} else {
				subscriber = sub
				sseConnected.Set(1)
			}
		}
	}

	// Register routes
	mux := handlers.routes(metricsRegistry.Handler())

	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 60 * time.Second
	}

	// nosniff, referrer and permissions policies and HSTS on every response;
	// each page's CSP and framing policy is set by its handler (csp.go).
	// A request on a verified custom domain is rewritten onto its page's
	// slug paths before any route sees it (custom_host.go).
	handler := securityheaders.StatusPage(clientip.NewResolver(cfg.TrustedProxyCIDRs), cfg.HSTSMaxAge)(handlers.CustomHosts(mux))

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      handler,
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
		pushSender:   pushSend,
		sseConnected: sseConnected,
	}
}

// routes is the service's mux. CustomHosts wraps it, so on a custom domain
// only the page's own routes are reachable through it.
func (h *Handlers) routes(metricsHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/metrics", metricsHandler.ServeHTTP)

	// API proxy for the in-page status page customizer (optional via STATUS_PAGE_API_BASE_URL)
	mux.HandleFunc("/_sp_api/", h.HandleAPIProxy)

	// The service worker must be a real URL -- it is the one asset the page
	// cannot inline. It is served from /public/status/ rather than from under
	// a slug because a worker's default scope is its own directory: at
	// /public/status/{slug}/sw.js the max scope would be
	// /public/status/{slug}/, which is NOT a prefix of the page URL
	// /public/status/{slug}, so registration would succeed and control
	// nothing. From here the max scope covers every page, and the client
	// narrows it to its own slug.
	mux.HandleFunc("/public/status/sw.js", h.HandlePushServiceWorker)

	// Public status page routes. This must stay registered last of the
	// /public/status/ patterns; ServeMux resolves most-specific-first, so the
	// exact sw.js pattern wins over this prefix.
	mux.HandleFunc("/public/status/", h.HandleStatusPage)

	// Anything else, on any host, gets the answer an unknown slug gets.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeNotFound(w) })
	return mux
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
	if s.pushSender != nil {
		s.pushSender.Stop()
	}
	if s.subscriber != nil {
		s.subscriber.Close()
		if s.sseConnected != nil {
			s.sseConnected.Set(0)
		}
	}
	return s.http.Shutdown(ctx)
}

// warnUntrustedProxyWithGatedPages logs once at startup when password or
// private pages exist and no trusted proxy is configured: every visitor
// behind an ingress then resolves to the ingress address, which is not
// attributable, so failed-credential throttling falls back to the per-page
// budget alone and one guesser can hold every visitor of a page at the
// unlock form.
func warnUntrustedProxyWithGatedPages(cfg *config.StatusPageConfig, service *Service, log *logger.Logger) {
	if len(cfg.TrustedProxyCIDRs) > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gated, err := service.HasGatedPages(ctx)
	if err != nil {
		log.WithError(err).Warn("Could not check for password or private status pages")
		return
	}
	if gated {
		log.Warn("Password or private status pages exist but TRUSTED_PROXY_CIDRS is unset: behind a proxy every visitor shares one address, so failed-credential throttling falls back to a per-page budget that one client can exhaust. Set TRUSTED_PROXY_CIDRS to your proxies' ranges.")
	}
}
