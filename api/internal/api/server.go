package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	agenthandlers "github.com/yassinebenameur/probara/api/internal/handlers/agent"
	alertchannelhandlers "github.com/yassinebenameur/probara/api/internal/handlers/alertchannels"
	"github.com/yassinebenameur/probara/api/internal/handlers/alertpolicies"
	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
	apikeyhandlers "github.com/yassinebenameur/probara/api/internal/handlers/apikeys"
	authhandlers "github.com/yassinebenameur/probara/api/internal/handlers/auth"
	dashboardhandlers "github.com/yassinebenameur/probara/api/internal/handlers/dashboard"
	importhandlers "github.com/yassinebenameur/probara/api/internal/handlers/import"
	incidenthandlers "github.com/yassinebenameur/probara/api/internal/handlers/incidents"
	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
	pushhandlers "github.com/yassinebenameur/probara/api/internal/handlers/push"
	statuspagehandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspages"
	tenanthandlers "github.com/yassinebenameur/probara/api/internal/handlers/tenants"
	userhandlers "github.com/yassinebenameur/probara/api/internal/handlers/users"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	adminauthservice "github.com/yassinebenameur/probara/api/internal/services/adminauth"
	adminusersservice "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	agentservice "github.com/yassinebenameur/probara/api/internal/services/agent"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	alertpolicyservice "github.com/yassinebenameur/probara/api/internal/services/alertpolicies"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	apikeyservice "github.com/yassinebenameur/probara/api/internal/services/apikeys"
	dashboardservice "github.com/yassinebenameur/probara/api/internal/services/dashboard"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
	incidentservice "github.com/yassinebenameur/probara/api/internal/services/incidents"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	pushservice "github.com/yassinebenameur/probara/api/internal/services/push"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	tenantservice "github.com/yassinebenameur/probara/api/internal/services/tenants"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// Server represents the API HTTP server
type Server struct {
	config          *config.APIConfig
	logger          *logger.Logger
	metrics         *metrics.Registry
	db              *db.Client
	queue           *queue.Client
	http            *http.Server
	alertSubscriber *alertservice.Subscriber
	statusPublisher *statusupdates.Publisher
}

type monitorStatusNotifier struct {
	publisher *statusupdates.Publisher
}

func (n monitorStatusNotifier) PublishStatusUpdate(_ context.Context, monitorID, tenantID uuid.UUID) {
	if n.publisher == nil {
		return
	}
	_ = n.publisher.Publish(statusupdates.Event{
		Type:      "history_deleted",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	})
}

// NewServer creates a new API server
func NewServer(cfg *config.APIConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client) *Server {
	r := chi.NewRouter()

	// Middleware chain
	r.Use(apimiddleware.RequestIDMiddleware)
	r.Use(apimiddleware.RecoveryMiddleware(log))
	r.Use(apimiddleware.LoggingMiddleware(log))
	r.Use(apimiddleware.MetricsMiddleware(cfg.ServiceName))
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	// Health and metrics endpoints (no auth required)
	healthHandlers := NewHandlers(cfg, log, metricsRegistry, dbClient)
	r.Get("/healthz", healthHandlers.Healthz)
	r.Get("/readyz", healthHandlers.Readyz)
	r.Get("/metrics", metricsRegistry.Handler().ServeHTTP)

	// Static files for agent binaries (no auth required)
	// Serve from ./static directory
	staticDir := http.Dir("./static")
	r.Get("/static/*", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/static/", http.FileServer(staticDir)).ServeHTTP(w, r)
	})

	// Status update publisher (optional)
	statusPublisher, err := statusupdates.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize status update publisher")
		statusPublisher = nil
	}

	// Check job publisher (optional, used by on-demand monitor runs)
	checkJobQueue, err := queue.NewClient(cfg.NATSURL)
	if err != nil {
		log.WithError(err).Warn("Failed to initialize NATS queue for on-demand monitor runs")
		checkJobQueue = nil
	}

	// Push service and handlers (created here to use in both public and authenticated routes)
	pushSvc := pushservice.NewService(dbClient.DB, statusPublisher)
	pushHandlers := pushhandlers.NewHandler(pushSvc, log)
	alertHub := alertservice.NewHub()
	alertSubscriber := alertservice.NewSubscriber(checkJobQueue, alertHub, cfg, log)

	// Push webhook endpoints (no auth - uses token in URL for authentication)
	r.Route("/api/v1/push", func(r chi.Router) {
		r.Get("/{token}", pushHandlers.HandlePushGet)
		r.Post("/{token}", pushHandlers.HandlePushPost)
	})

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		adminUsersSvc := adminusersservice.NewService(dbClient, cfg.AdminBcryptCost)
		adminUsersHandlers := userhandlers.NewHandlers(adminUsersSvc, log)

		// Auth endpoints (no auth required)
		authService := adminauthservice.NewService(dbClient, cfg.AdminBcryptCost)
		authHandlers := authhandlers.NewHandlers(authService, cfg, log)
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", authHandlers.Login)
			r.Post("/refresh", authHandlers.Refresh)
			r.Post("/logout", authHandlers.Logout)
			r.Get("/me", authHandlers.Me)
		})
		r.Get("/users/bootstrap/status", adminUsersHandlers.BootstrapStatus)
		r.Post("/users/bootstrap/first", adminUsersHandlers.BootstrapFirstUser)

		// Protected API routes
		r.Route("/", func(r chi.Router) {
			// Apply auth middleware to all API routes
			r.Use(apimiddleware.AuthMiddleware(dbClient, log, cfg.AdminJWTSecret))

			// Agent service and handlers (needed by monitors route)
			agentService := agentservice.NewService(dbClient.DB, statusPublisher)
			agentHandlers := agenthandlers.NewHandler(agentService, log)

			// Alert service and handlers (shared across alerts + dashboard routes)
			alertSvc := alertservice.NewService(dbClient)
			analyticsRepo := sharedanalytics.NewRepository(dbClient)
			alertHandlers := alerthandlers.NewHandlers(alertSvc, alertHub, log)

			// Dashboard service and handlers
			dashboardSvc := dashboardservice.NewService(dbClient, alertSvc, analyticsRepo)
			dashboardHandlers := dashboardhandlers.NewHandlers(dashboardSvc, log)

			// Monitor services
			groupSvc := groupservice.NewService(dbClient)
			monitorService := monitorservice.NewService(monitorservice.NewPostgresRepository(dbClient))
			monitorService.ConfigureHistoryDependencies(groupSvc, monitorStatusNotifier{publisher: statusPublisher})
			resultSvc := resultservice.NewService(dbClient, groupSvc, analyticsRepo)
			monitorHandlers := monitorhandlers.NewHandlers(monitorService, groupSvc, resultSvc, log, cfg.SyntheticArtifactsDir)
			monitorHandlers.ConfigureCheckJobs(checkJobQueue, cfg.CheckJobSubject)

			// Import service and handlers
			importSvc := importservice.NewService(dbClient, monitorService)
			importHdlrs := importhandlers.NewHandlers(importSvc, log)

			// Incident service and handlers
			incidentService := incidentservice.NewService(dbClient)
			incidentHandlers := incidenthandlers.NewHandlers(incidentService, log)

			r.Route("/monitors", func(r chi.Router) {
				r.Post("/", monitorHandlers.CreateMonitor)
				r.Get("/", monitorHandlers.ListMonitors)
				// Import endpoints (must be before /{id} to avoid conflicts)
				r.Get("/export", importHdlrs.Export)
				r.Post("/import/preview", importHdlrs.Preview)
				r.Post("/import", importHdlrs.Execute)
				r.Get("/{id}", monitorHandlers.GetMonitor)
				r.Get("/{id}/analytics", monitorHandlers.GetMonitorAnalytics)
				r.Get("/{id}/results", monitorHandlers.GetMonitorResults)
				r.Delete("/{id}/history", monitorHandlers.DeleteMonitorHistory)
				r.Post("/{id}/run", monitorHandlers.RunMonitorNow)
				r.Get("/{id}/artifacts/screenshot", monitorHandlers.GetSyntheticBrowserScreenshot)
				r.Patch("/{id}", monitorHandlers.UpdateMonitor)
				r.Delete("/{id}", monitorHandlers.DeleteMonitor)
				// Group membership endpoints
				r.Get("/{id}/members", monitorHandlers.GetGroupMembers)
				r.Post("/{id}/members", monitorHandlers.AddMonitorsToGroup)
				r.Delete("/{id}/members", monitorHandlers.RemoveMonitorsFromGroup)
				// Agent install endpoints
				r.Get("/{id}/agent/install", agentHandlers.HandleGetInstallCommand)
				r.Get("/{id}/agent/install/script.sh", agentHandlers.HandleGetInstallScript)
				// Push info endpoint
				r.Get("/{id}/push/info", pushHandlers.HandleGetPushInfo)
			})

			// Agent metrics endpoint
			r.Post("/agent/metrics", agentHandlers.HandleReceiveMetrics)

			// Dashboard
			r.Route("/dashboard", func(r chi.Router) {
				r.Get("/overview", dashboardHandlers.GetOverview)
			})

			// Alerts
			r.Route("/alerts", func(r chi.Router) {
				r.Get("/", alertHandlers.ListAlerts)
				r.Get("/recent", alertHandlers.GetRecentAlerts)
				r.Get("/stream", alertHandlers.StreamAlerts)
				r.Get("/counts/by-policy", alertHandlers.GetAlertCountsByPolicy)
				r.Get("/counts/monitors-by-policy", alertHandlers.GetMonitorCountsByPolicy)
				r.Get("/{id}", alertHandlers.GetAlert)
				r.Post("/{id}/acknowledge", alertHandlers.AcknowledgeAlert)
				r.Post("/{id}/resolve", alertHandlers.ResolveAlert)
			})

			// Incidents
			r.Route("/incidents", func(r chi.Router) {
				r.Get("/", incidentHandlers.ListIncidents)
				r.Post("/", incidentHandlers.CreateIncident)
				r.Get("/{id}", incidentHandlers.GetIncident)
				r.Patch("/{id}", incidentHandlers.UpdateIncident)
				r.Post("/{id}/state", incidentHandlers.TransitionIncidentState)
				r.Post("/{id}/timeline", incidentHandlers.CreateTimelineEntry)
				r.Post("/{id}/alerts", incidentHandlers.AttachIncidentAlert)
				r.Delete("/{id}/alerts/{alertId}", incidentHandlers.DetachIncidentAlert)
				r.Post("/{id}/monitors", incidentHandlers.AttachIncidentMonitor)
				r.Delete("/{id}/monitors/{monitorId}", incidentHandlers.DetachIncidentMonitor)
				r.Put("/{id}/status-pages/{statusPageId}", incidentHandlers.PublishIncidentToStatusPage)
				r.Delete("/{id}/status-pages/{statusPageId}", incidentHandlers.UnpublishIncidentFromStatusPage)
			})

			// Alert policies
			alertPolicyService := alertpolicyservice.NewService(dbClient)
			alertPolicyHandlers := alertpolicies.NewHandlers(alertPolicyService, log)
			r.Route("/alert-policies", func(r chi.Router) {
				r.Post("/", alertPolicyHandlers.CreateAlertPolicy)
				r.Get("/", alertPolicyHandlers.ListAlertPolicies)
				r.Get("/{id}/alerts", alertHandlers.GetAlertsByPolicy)
				r.Get("/{id}/monitors", alertHandlers.GetMonitorsByPolicy)
				r.Get("/{id}", alertPolicyHandlers.GetAlertPolicy)
				r.Patch("/{id}", alertPolicyHandlers.UpdateAlertPolicy)
				r.Delete("/{id}", alertPolicyHandlers.DeleteAlertPolicy)
			})

			// Alert channels
			alertChannelService := alertchannelservice.NewService(dbClient)
			alertChannelHandlers := alertchannelhandlers.NewHandlers(alertChannelService, log)
			r.Route("/alert-channels", func(r chi.Router) {
				r.Post("/", alertChannelHandlers.CreateAlertChannel)
				r.Get("/", alertChannelHandlers.ListAlertChannels)
				r.Get("/{id}", alertChannelHandlers.GetAlertChannel)
				r.Patch("/{id}", alertChannelHandlers.UpdateAlertChannel)
				r.Delete("/{id}", alertChannelHandlers.DeleteAlertChannel)
				r.Post("/{id}/test", alertChannelHandlers.TestAlertChannel)
			})

			// API keys
			apiKeyService := apikeyservice.NewService(dbClient)
			apiKeyHandlers := apikeyhandlers.NewHandlers(apiKeyService, log)
			r.Route("/api-keys", func(r chi.Router) {
				r.Post("/", apiKeyHandlers.CreateAPIKey)
				r.Get("/", apiKeyHandlers.ListAPIKeys)
				r.Delete("/{id}", apiKeyHandlers.RevokeAPIKey)
			})

			// Status pages
			statusPageService := statuspageservice.NewService(dbClient)
			statusPageHandlers := statuspagehandlers.NewHandlers(statusPageService, log)
			r.Route("/status-pages", func(r chi.Router) {
				r.Post("/", statusPageHandlers.CreateStatusPage)
				r.Get("/", statusPageHandlers.ListStatusPages)
				r.Get("/{id}", statusPageHandlers.GetStatusPage)
				r.Patch("/{id}", statusPageHandlers.UpdateStatusPage)
				r.Delete("/{id}", statusPageHandlers.DeleteStatusPage)
			})

			// Tenants (admin only)
			tenantSvc := tenantservice.NewService(dbClient)
			tenantHandlers := tenanthandlers.NewHandlers(tenantSvc, log)
			r.Route("/tenant-settings", func(r chi.Router) {
				r.Get("/", tenantHandlers.GetTenantSettings)
				r.Patch("/", tenantHandlers.UpdateTenantSettings)
			})
			r.Route("/tenants", func(r chi.Router) {
				r.Use(apimiddleware.RequireAdmin)
				r.Get("/", tenantHandlers.ListTenants)
			})

			// Users (admin only)
			r.Route("/users", func(r chi.Router) {
				r.Use(apimiddleware.RequireAdmin)
				r.Get("/", adminUsersHandlers.ListUsers)
				r.Post("/", adminUsersHandlers.CreateUser)
				r.Get("/{id}", adminUsersHandlers.GetUser)
				r.Patch("/{id}", adminUsersHandlers.UpdateUser)
				r.Delete("/{id}", adminUsersHandlers.DeleteUser)
			})
		})
	})

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		config:          cfg,
		logger:          log,
		metrics:         metricsRegistry,
		db:              dbClient,
		queue:           checkJobQueue,
		http:            httpServer,
		alertSubscriber: alertSubscriber,
		statusPublisher: statusPublisher,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.logger.WithFields(map[string]interface{}{
		"port": s.config.HTTPPort,
	}).Info("Starting HTTP server")

	if s.alertSubscriber != nil {
		if err := s.alertSubscriber.Start(context.Background()); err != nil {
			return fmt.Errorf("start alert subscriber: %w", err)
		}
	}

	return s.http.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server")
	if s.alertSubscriber != nil {
		s.alertSubscriber.Stop()
	}
	if s.statusPublisher != nil {
		s.statusPublisher.Close()
	}
	if s.queue != nil {
		s.queue.Close()
	}
	return s.http.Shutdown(ctx)
}
