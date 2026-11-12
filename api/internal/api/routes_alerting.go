// Routes for the dashboard, alerts, incidents and where alerts are delivered.

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	ailabelshandlers "github.com/yassinebenameur/probara/api/internal/handlers/ailabels"
	alertchannelhandlers "github.com/yassinebenameur/probara/api/internal/handlers/alertchannels"
	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
	dashboardhandlers "github.com/yassinebenameur/probara/api/internal/handlers/dashboard"
	incidenthandlers "github.com/yassinebenameur/probara/api/internal/handlers/incidents"
	notificationsettingshandlers "github.com/yassinebenameur/probara/api/internal/handlers/notificationsettings"
)

// mountDashboardRoutes mounts the dashboard read models.
func mountDashboardRoutes(r chi.Router, dashboardHandlers *dashboardhandlers.Handlers) {
	r.Route("/dashboard", func(r chi.Router) {
		r.Get("/overview", dashboardHandlers.GetOverview)
		r.Get("/summary", dashboardHandlers.GetSummary)
		r.Get("/problem-monitors", dashboardHandlers.GetProblemMonitors)
		r.Get("/recent-failures", dashboardHandlers.GetRecentFailures)
		r.Get("/recent-alerts", dashboardHandlers.GetRecentAlerts)
		r.Get("/group-sparkline", dashboardHandlers.GetGroupSparkline)
	})
}

// mountAlertRoutes mounts alert reads, the live stream, triage actions and the triage label.
func mountAlertRoutes(r chi.Router, alertHandlers *alerthandlers.Handlers, aiLabelHdlrs *ailabelshandlers.Handlers) {
	r.Route("/alerts", func(r chi.Router) {
		r.Get("/", alertHandlers.ListAlerts)
		r.Get("/recent", alertHandlers.GetRecentAlerts)
		r.Get("/stream", alertHandlers.StreamAlerts)
		r.Get("/{id}", alertHandlers.GetAlert)
		r.Post("/{id}/acknowledge", alertHandlers.AcknowledgeAlert)
		r.Post("/{id}/resolve", alertHandlers.ResolveAlert)
		// Bulk triage: per-alert results and one audit event per alert.
		r.Post("/bulk/acknowledge", alertHandlers.BulkAcknowledgeAlerts)
		r.Post("/bulk/resolve", alertHandlers.BulkResolveAlerts)
		// Triage training label (audited write; not in postReadPaths).
		// A corrected label also stops the alert's pending
		// suspected-cause follow-ups.
		r.Post("/{id}/triage/label", aiLabelHdlrs.LabelAlertTriage)
	})
}

// mountIncidentRoutes mounts incident CRUD, attachments, status-page publication and AI analysis.
func mountIncidentRoutes(r chi.Router, incidentHandlers *incidenthandlers.Handlers, aiLabelHdlrs *ailabelshandlers.Handlers) {
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
		r.Post("/{id}/ai-analysis", incidentHandlers.RequestIncidentAIAnalysis)
		r.Get("/{id}/ai-analysis", incidentHandlers.GetIncidentAIAnalysis)
		r.Post("/{id}/ai-analysis/label", aiLabelHdlrs.LabelIncidentAnalysis)
	})
}

// mountRetiredAlertPolicyRoutes mounts the 410 tombstones left where alert policies used to be.
func mountRetiredAlertPolicyRoutes(r chi.Router) {
	r.Route("/alert-policies", func(r chi.Router) {
		gone := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"error":"alert policies were replaced by notification settings; see /notification-settings"}`))
		}
		r.Post("/", gone)
		r.Get("/", gone)
		r.Get("/{id}", gone)
		r.Patch("/{id}", gone)
		r.Delete("/{id}", gone)
	})
}

// mountAlertChannelRoutes mounts alert-channel CRUD, test sends and the plugin catalog.
func mountAlertChannelRoutes(r chi.Router, alertChannelHandlers *alertchannelhandlers.Handlers) {
	r.Route("/alert-channels", func(r chi.Router) {
		r.Post("/", alertChannelHandlers.CreateAlertChannel)
		r.Get("/", alertChannelHandlers.ListAlertChannels)
		r.Get("/{id}", alertChannelHandlers.GetAlertChannel)
		r.Patch("/{id}", alertChannelHandlers.UpdateAlertChannel)
		r.Delete("/{id}", alertChannelHandlers.DeleteAlertChannel)
		r.Post("/{id}/test", alertChannelHandlers.TestAlertChannel)
	})

	// Alert channel plugin catalog (manifests driving the UI form).
	r.Route("/alert-channel-plugins", func(r chi.Router) {
		r.Get("/", alertChannelHandlers.ListPlugins)
		r.Get("/{type}", alertChannelHandlers.GetPlugin)
	})
}

// mountNotificationSettingsRoutes mounts the workspace notification settings.
func mountNotificationSettingsRoutes(r chi.Router, notificationSettingsHdlrs *notificationsettingshandlers.Handlers) {
	r.Route("/notification-settings", func(r chi.Router) {
		r.Get("/", notificationSettingsHdlrs.GetSettings)
		r.Put("/", notificationSettingsHdlrs.UpdateSettings)
	})
}
