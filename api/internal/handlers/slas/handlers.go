package slas

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	svc "github.com/yassinebenameur/probara/api/internal/services/slas"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /slas. Reads (lists, reports, exports) are open to viewers;
// mutations and issuing go through RequireWrite like every other write.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Get("/{id}/report", h.report)
	r.Get("/{id}/reports", h.listReports)
	r.Post("/{id}/reports", h.issue)
}

// ReportRoutes mounts /sla-reports (issued, frozen reports).
func (h *Handlers) ReportRoutes(r chi.Router) {
	r.Get("/{id}", h.issuedReport)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handlers) fail(w http.ResponseWriter, err error, action string) {
	var bad svc.Invalid
	var conflict svc.Conflict
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "not found")
	case errors.As(err, &conflict):
		apierrors.WriteError(w, http.StatusConflict, "conflict", conflict.Error())
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	default:
		h.Log.WithError(err).Error("Failed to " + action)
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

func tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	s, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return id, true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid ID")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		apierrors.WriteValidationError(w, "expected one JSON object")
		return false
	}
	return true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	monitorID := uuid.Nil
	if raw := r.URL.Query().Get("monitor_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid monitor_id")
			return
		}
		monitorID = id
	}
	slas, err := h.Service.List(r.Context(), t, monitorID)
	if err != nil {
		h.fail(w, err, "list SLAs")
		return
	}
	if slas == nil {
		slas = []models.SLA{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": slas})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	var req models.CreateSLARequest
	if !decode(w, r, &req) {
		return
	}
	sla, err := h.Service.Create(r.Context(), t, &req)
	if err != nil {
		h.fail(w, err, "create SLA")
		return
	}
	writeJSON(w, http.StatusCreated, sla)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sla, err := h.Service.Get(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "get SLA")
		return
	}
	writeJSON(w, http.StatusOK, sla)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req models.UpdateSLARequest
	if !decode(w, r, &req) {
		return
	}
	sla, err := h.Service.Update(r.Context(), t, id, &req)
	if err != nil {
		h.fail(w, err, "update SLA")
		return
	}
	writeJSON(w, http.StatusOK, sla)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), t, id); err != nil {
		h.fail(w, err, "delete SLA")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// report handles GET /slas/{id}/report?period=|from=&to=[&format=].
func (h *Handlers) report(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	format, ok := parseFormat(w, r)
	if !ok {
		return
	}
	report, err := h.Service.Report(r.Context(), t, id, svc.ReportQuery{Period: q.Get("period"), From: q.Get("from"), To: q.Get("to")})
	if err != nil {
		h.fail(w, err, "build SLA report")
		return
	}
	h.render(w, report, format)
}

func (h *Handlers) listReports(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	reports, err := h.Service.ListReports(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "list SLA reports")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": reports})
}

func (h *Handlers) issue(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req models.IssueSLAReportRequest
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	report, err := h.Service.IssueReport(r.Context(), t, id, req.Period, issuer(r))
	if err != nil {
		h.fail(w, err, "issue SLA report")
		return
	}
	writeJSON(w, http.StatusCreated, report)
}

func (h *Handlers) issuedReport(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	format, ok := parseFormat(w, r)
	if !ok {
		return
	}
	report, err := h.Service.GetIssuedReport(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "get SLA report")
		return
	}
	h.render(w, report, format)
}

func issuer(r *http.Request) svc.Issuer {
	var by svc.Issuer
	if raw, ok := ctxpkg.GetAPIKeyID(r.Context()); ok {
		by.APIKeyID, _ = uuid.Parse(raw)
	}
	if raw, ok := ctxpkg.GetAdminID(r.Context()); ok {
		by.AdminID, _ = uuid.Parse(raw)
	}
	return by
}

// parseFormat reads ?format=; "" means the plain JSON response the UI
// reads, any explicit format is a download.
func parseFormat(w http.ResponseWriter, r *http.Request) (string, bool) {
	switch f := r.URL.Query().Get("format"); f {
	case "", svc.FormatJSON, svc.FormatCSV, svc.FormatPDF:
		return f, true
	default:
		apierrors.WriteValidationError(w, "format must be json, csv or pdf")
		return "", false
	}
}

func (h *Handlers) render(w http.ResponseWriter, report *models.SLAReport, format string) {
	if format == "" {
		writeJSON(w, http.StatusOK, report)
		return
	}
	// Render fully before writing headers so a failure is still a clean 500.
	var buf bytes.Buffer
	var contentType string
	var err error
	switch format {
	case svc.FormatJSON:
		contentType = "application/json"
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		err = enc.Encode(report)
	case svc.FormatCSV:
		contentType = "text/csv; charset=utf-8"
		err = svc.WriteCSV(&buf, report)
	case svc.FormatPDF:
		contentType = "application/pdf"
		err = svc.WritePDF(&buf, report)
	}
	if err != nil {
		h.fail(w, err, "render SLA report")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+svc.FileName(report, format)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
