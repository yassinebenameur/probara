package alertchannels

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// ListPlugins handles GET /api/v1/alert-channel-plugins.
// Returns every registered alert channel plugin's manifest, sorted by Type.
func (h *Handlers) ListPlugins(w http.ResponseWriter, r *http.Request) {
	manifests := plugin.DefaultRegistry.All()
	httpx.WriteJSON(w, http.StatusOK, manifests)
}

// GetPlugin handles GET /api/v1/alert-channel-plugins/{type}.
func (h *Handlers) GetPlugin(w http.ResponseWriter, r *http.Request) {
	pluginType := chi.URLParam(r, "type")
	if pluginType == "" {
		apierrors.WriteValidationError(w, "plugin type is required")
		return
	}

	p, ok := plugin.DefaultRegistry.Get(pluginType)
	if !ok {
		apierrors.WriteNotFoundError(w, "plugin not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, p.Manifest())
}
