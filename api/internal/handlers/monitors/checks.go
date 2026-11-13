// On-demand checks: testing an unsaved config and running a saved monitor now.

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// TestMonitorConfigRequest is the body for POST /api/v1/monitors/test.
// RunMonitorNowRequest is the optional body of POST /monitors/{id}/run.
type RunMonitorNowRequest struct {
	LocationID *string `json:"location_id,omitempty"`
}

type TestMonitorConfigRequest struct {
	Type           models.MonitorType `json:"type"`
	Config         json.RawMessage    `json:"config"`
	TimeoutSeconds int                `json:"timeout_seconds"`
	// MonitorID resolves write-only secret placeholders ("***") against the
	// stored monitor when testing an edit.
	MonitorID *string `json:"monitor_id,omitempty"`
	// LocationID routes the test to that private location's workers so it
	// runs from the same vantage point as the scheduled checks. Empty =
	// default platform fleet.
	LocationID *string `json:"location_id,omitempty"`
}

// TestMonitorConfig handles POST /api/v1/monitors/test — runs one ephemeral
// check via a worker (NATS request-reply) so a config can be validated before
// saving. Nothing is persisted.
func (h *Handlers) TestMonitorConfig(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	if h.jobRequester == nil {
		apierrors.WriteInternalError(w, "test checks are not available (queue not configured)")
		return
	}

	var req TestMonitorConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if !validation.DefaultRegistry.Has(req.Type) {
		apierrors.WriteValidationError(w, "unknown monitor type: "+string(req.Type))
		return
	}
	if len(req.Config) == 0 {
		apierrors.WriteValidationError(w, "config is required")
		return
	}
	if err := validation.DefaultRegistry.Validate(req.Type, req.Config); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	// A test of a test-call config places a real call.
	if err := monitorservice.RequireCallPermission(r.Context(), req.Type, req.Config); err != nil {
		apierrors.WriteForbiddenError(w, err.Error())
		return
	}
	if containsEncryptedTestSecret(req.Type, req.Config) {
		apierrors.WriteValidationError(w, "encrypted credentials cannot be supplied in test requests; use saved secret placeholders")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 10
	}
	if req.TimeoutSeconds > 60 {
		req.TimeoutSeconds = 60
	}

	var monitorID *uuid.UUID
	if req.MonitorID != nil && *req.MonitorID != "" {
		parsed, err := uuid.Parse(*req.MonitorID)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid monitor_id")
			return
		}
		monitorID = &parsed
	}

	// Custom destinations must not turn a read-only test into an oracle for
	// saved credentials. Tests using only caller-supplied credentials remain safe.
	if monitorID != nil && secrets.HasMonitorSecrets(string(req.Type)) && !ctxpkg.CanWrite(r.Context()) {
		apierrors.WriteForbiddenError(w, "testing with saved credentials requires write permission")
		return
	}
	config, err := h.service.ResolveTestConfig(r.Context(), tenantUUID, monitorID, req.Type, req.Config)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorTypeMismatch) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantID}).Error("Failed to resolve test config")
		apierrors.WriteInternalError(w, "failed to resolve config")
		return
	}

	testSubject := sharedmodels.TestCheckSubject
	if req.LocationID != nil && *req.LocationID != "" {
		locationID, err := uuid.Parse(*req.LocationID)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid location_id")
			return
		}
		if h.locationProtector == nil {
			apierrors.WriteInternalError(w, "private location security is unavailable")
			return
		}
		config, err = h.locationProtector.ProtectMonitorConfigForWorker(r.Context(), tenantUUID, locationID, string(req.Type), config)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to protect private-location test config")
			apierrors.WriteInternalError(w, "failed to protect test config")
			return
		}
		testSubject = sharedmodels.TestCheckSubjectForLocation(locationID.String())
	}

	payload, err := json.Marshal(sharedmodels.CheckJobPayload{
		Type:           string(req.Type),
		Config:         config,
		TimeoutSeconds: req.TimeoutSeconds,
	})
	if err != nil {
		apierrors.WriteInternalError(w, "failed to encode test payload")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSeconds+10)*time.Second)
	defer cancel()
	reply, err := h.jobRequester.Request(ctx, testSubject, payload)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "type": req.Type, "subject": testSubject}).Warn("Test check request failed")
		if testSubject != sharedmodels.TestCheckSubject {
			apierrors.WriteInternalError(w, "no worker answered at this location — is its worker running and connected?")
			return
		}
		apierrors.WriteInternalError(w, "no worker answered the test request — is a worker running?")
		return
	}

	var response sharedmodels.TestCheckResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		apierrors.WriteInternalError(w, "invalid worker response")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

func containsEncryptedTestSecret(monitorType models.MonitorType, config json.RawMessage) bool {
	var fields map[string]any
	if json.Unmarshal(config, &fields) != nil {
		return false // The type validator reports malformed config separately.
	}
	for _, field := range secrets.MonitorSecretFields[string(monitorType)] {
		if value, ok := fields[field].(string); ok && secrets.LooksLikeEnvelope(value) {
			return true
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[string(monitorType)] {
		values, _ := fields[field].(map[string]any)
		for _, raw := range values {
			if value, ok := raw.(string); ok && secrets.LooksLikeEnvelope(value) {
				return true
			}
		}
	}
	return false
}

// RunMonitorNow handles POST /api/v1/monitors/{id}/run
func (h *Handlers) RunMonitorNow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	if h.jobPublisher == nil {
		apierrors.WriteInternalError(w, "on-demand monitor runs are unavailable")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
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
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
		}).Error("Failed to fetch monitor for on-demand run")
		apierrors.WriteInternalError(w, "failed to fetch monitor")
		return
	}

	if sharedmodels.IsPassiveMonitorType(string(monitor.Type)) {
		apierrors.WriteValidationError(w, fmt.Sprintf("%s monitors cannot be run on-demand", monitor.Type))
		return
	}
	// Running a test-call monitor places a real call from every selected
	// location.
	if err := monitorservice.RequireCallPermission(r.Context(), monitor.Type, monitor.Config); err != nil {
		apierrors.WriteForbiddenError(w, err.Error())
		return
	}

	// Optional body: {"location_id": "..."} narrows the run to one of the
	// monitor's locations. Default: fan out exactly like the scheduler (all
	// selected locations, or the default fleet when none).
	var runReq RunMonitorNowRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&runReq) // empty body is fine
	}

	locationIDs := []string{""}
	if len(monitor.LocationIDs) > 0 {
		locationIDs = locationIDs[:0]
		for _, id := range monitor.LocationIDs {
			locationIDs = append(locationIDs, id.String())
		}
	}
	if runReq.LocationID != nil && *runReq.LocationID != "" {
		requested, err := uuid.Parse(*runReq.LocationID)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid location_id")
			return
		}
		found := false
		for _, id := range monitor.LocationIDs {
			if id == requested {
				found = true
				break
			}
		}
		if !found {
			apierrors.WriteValidationError(w, "location is not selected on this monitor")
			return
		}
		locationIDs = []string{requested.String()}
	}

	// GetMonitor masks secret config fields ("***") for API responses; resolve
	// the placeholders back to the stored (still encrypted) values so the
	// worker can actually authenticate.
	checkConfig, err := h.service.ResolveTestConfig(r.Context(), tenantUUID, &monitorID, monitor.Type, monitor.Config)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
		}).Error("Failed to resolve monitor config for on-demand run")
		apierrors.WriteInternalError(w, "failed to queue monitor run")
		return
	}

	now := time.Now()
	timeoutSeconds := monitor.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	deadline := now.Add(time.Duration(2*timeoutSeconds) * time.Second)

	var jobID string
	for _, locationID := range locationIDs {
		locationConfig := checkConfig
		if locationID != "" {
			if h.locationProtector == nil {
				apierrors.WriteInternalError(w, "private location security is unavailable")
				return
			}
			parsedLocationID, err := uuid.Parse(locationID)
			if err != nil {
				apierrors.WriteInternalError(w, "invalid stored location ID")
				return
			}
			locationConfig, err = h.locationProtector.ProtectMonitorConfigForWorker(
				r.Context(), tenantUUID, parsedLocationID, string(monitor.Type), checkConfig,
			)
			if err != nil {
				h.logger.WithFields(map[string]interface{}{"error": err.Error(), "location_id": locationID}).Error("Failed to protect private-location monitor config")
				apierrors.WriteInternalError(w, "failed to protect monitor config")
				return
			}
		}
		payload := sharedmodels.CheckJobPayload{
			MonitorID:      monitor.ID.String(),
			Type:           string(monitor.Type),
			Config:         locationConfig,
			TimeoutSeconds: timeoutSeconds,
			LocationID:     locationID,
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantID,
				"monitor_id": monitorID.String(),
			}).Error("Failed to marshal check payload")
			apierrors.WriteInternalError(w, "failed to queue monitor run")
			return
		}

		subject := sharedmodels.CheckJobSubjectDefault(h.checkSubject)
		if locationID != "" {
			subject = sharedmodels.CheckJobSubjectForLocation(h.checkSubject, locationID)
		}

		jobID = uuid.New().String()
		job := sharedmodels.NewJob(jobID, tenantUUID.String(), sharedmodels.JobTypeCheck, "v1", payloadJSON).WithDeadline(deadline)

		if err := h.jobPublisher.PublishJSON(r.Context(), subject, job, nil); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantID,
				"monitor_id": monitorID.String(),
				"subject":    subject,
			}).Error("Failed to publish on-demand monitor run job")
			apierrors.WriteInternalError(w, "failed to queue monitor run")
			return
		}
	}

	resp := models.RunMonitorNowResponse{
		JobID:     jobID,
		MonitorID: monitor.ID,
		QueuedAt:  now,
		Deadline:  deadline,
	}

	httpx.WriteJSON(w, http.StatusAccepted, resp)
}
