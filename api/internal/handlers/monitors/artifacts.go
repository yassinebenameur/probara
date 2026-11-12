// Serving synthetic-browser screenshot artifacts.

package monitors

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
)

// GetSyntheticBrowserScreenshot handles GET /api/v1/monitors/{id}/artifacts/screenshot?path=<relative-path>[&tenant_id=<uuid>]
func (h *Handlers) GetSyntheticBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, ok := artifactTenantID(w, r)
	if !ok {
		return
	}

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
		}).Error("Failed to get monitor for artifact request")
		apierrors.WriteInternalError(w, "failed to get monitor")
		return
	}

	if monitor.Type != models.MonitorTypeSyntheticBrowser {
		apierrors.WriteValidationError(w, "artifacts are only available for synthetic_browser monitors")
		return
	}

	rawPath := r.URL.Query().Get("path")
	if strings.TrimSpace(rawPath) == "" {
		apierrors.WriteValidationError(w, "artifact path is required")
		return
	}

	artifactPath, err := sanitizeArtifactPath(rawPath, monitorID)
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	fullPath := filepath.Join(h.artifactsDir, artifactPath)
	fullPath = filepath.Clean(fullPath)
	basePath := filepath.Clean(h.artifactsDir)
	if fullPath != basePath && !strings.HasPrefix(fullPath, basePath+string(filepath.Separator)) {
		apierrors.WriteValidationError(w, "invalid artifact path")
		return
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			apierrors.WriteNotFoundError(w, "artifact not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
			"path":       fullPath,
		}).Error("Failed to stat synthetic browser artifact")
		apierrors.WriteInternalError(w, "failed to read artifact")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, fullPath)
}

// artifactTenantID is httpx.TenantID plus a fallback the other handlers do not
// have: when authentication attached no tenant, it reads ?tenant_id=.
func artifactTenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if _, err := middleware.GetTenantID(r.Context()); err == nil {
		return httpx.TenantID(w, r)
	}

	fallback := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if fallback == "" {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}

	tenantUUID, err := uuid.Parse(fallback)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
}

func sanitizeArtifactPath(raw string, monitorID uuid.UUID) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("artifact path is required")
	}

	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid artifact path")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid artifact path")
	}

	normalized := filepath.ToSlash(clean)
	expectedPrefix := monitorID.String() + "/"
	if !strings.HasPrefix(normalized, expectedPrefix) {
		return "", fmt.Errorf("artifact path must belong to the requested monitor")
	}

	return clean, nil
}
