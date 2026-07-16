package alertchannels

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// ListPlugins handles GET /api/v1/alert-channel-plugins.
// Returns every registered alert channel plugin's manifest, sorted by Type.
func (h *Handlers) ListPlugins(w http.ResponseWriter, r *http.Request) {
	manifests := plugin.DefaultRegistry.All()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(manifests); err != nil {
		h.logger.WithError(err).Error("Failed to encode plugin manifests")
		errors.WriteInternalError(w, "failed to encode response")
	}
}

// GetPlugin handles GET /api/v1/alert-channel-plugins/{type}.
func (h *Handlers) GetPlugin(w http.ResponseWriter, r *http.Request) {
	pluginType := chi.URLParam(r, "type")
	if pluginType == "" {
		errors.WriteValidationError(w, "plugin type is required")
		return
	}

	p, ok := plugin.DefaultRegistry.Get(pluginType)
	if !ok {
		errors.WriteNotFoundError(w, "plugin not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(p.Manifest()); err != nil {
		h.logger.WithError(err).Error("Failed to encode plugin manifest")
		errors.WriteInternalError(w, "failed to encode response")
	}
}
