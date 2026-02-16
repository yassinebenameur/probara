package api

import (
	"context"
	"net/http"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

// Handlers contains HTTP handlers for the API service
type Handlers struct {
	config  *config.APIConfig
	logger  *logger.Logger
	metrics *metrics.Registry
	db      *db.Client
}

// NewHandlers creates a new handlers instance
func NewHandlers(cfg *config.APIConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client) *Handlers {
	return &Handlers{
		config:  cfg,
		logger:  log,
		metrics: metricsRegistry,
		db:      dbClient,
	}
}

// Healthz is the liveness probe endpoint
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Readyz is the readiness probe endpoint
func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	if h.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := h.db.HealthCheck(ctx); err != nil {
			h.logger.WithError(err).Warn("Database health check failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Database unavailable"))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
