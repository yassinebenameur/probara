package importhandlers

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
	"github.com/yassinebenameur/probara/shared/logger"
)

const maxUploadSize = 10 * 1024 * 1024 // 10MB

// Handlers handles import HTTP requests
type Handlers struct {
	service importservice.ImportService
	logger  *logger.Logger
}

// NewHandlers creates a new import handlers instance
func NewHandlers(service importservice.ImportService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// Preview handles POST /api/v1/monitors/import/preview
// Accepts multipart form file upload
func (h *Handlers) Preview(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	// Limit upload size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// Parse multipart form
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		apierrors.WriteValidationError(w, "file too large (max 10MB)")
		return
	}

	// Get the file from the form
	file, header, err := r.FormFile("file")
	if err != nil {
		apierrors.WriteValidationError(w, "no file provided")
		return
	}
	defer file.Close()

	// Read file content
	data, err := io.ReadAll(file)
	if err != nil {
		apierrors.WriteInternalError(w, "failed to read file")
		return
	}

	// Parse and preview
	result, err := h.service.ParseFile(data, header.Filename)
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id": tenantID,
		"filename":  header.Filename,
		"format":    result.Format,
		"rows":      result.TotalRows,
	}).Info("Import preview generated")

	httpx.WriteJSON(w, http.StatusOK, result)
}

// Execute handles POST /api/v1/monitors/import
// Accepts JSON body with rows and mapping
func (h *Handlers) Execute(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req models.ImportExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.Rows) == 0 {
		apierrors.WriteValidationError(w, "no rows to import")
		return
	}

	// Execute the import
	result, err := h.service.ExecuteImport(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Import execution failed")
		apierrors.WriteInternalError(w, "import failed: "+err.Error())
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":     tenantID,
		"total_rows":    result.TotalRows,
		"success_count": result.SuccessCount,
		"failed_count":  result.FailedCount,
		"skipped_count": result.SkippedCount,
	}).Info("Import executed")

	httpx.WriteJSON(w, http.StatusOK, result)
}

// Export handles GET /api/v1/monitors/export
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	data, err := h.service.ExportMonitors(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Monitor export failed")
		apierrors.WriteInternalError(w, "failed to export monitors")
		return
	}

	filename := "monitors-export-" + time.Now().Format("2006-01-02") + ".yaml"
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
