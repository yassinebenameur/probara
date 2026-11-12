// Routes for monitors and what they run on: locations, the mesh, maintenance windows.

package api

import (
	"github.com/go-chi/chi/v5"

	agenthandlers "github.com/yassinebenameur/probara/api/internal/handlers/agent"
	ailabelshandlers "github.com/yassinebenameur/probara/api/internal/handlers/ailabels"
	depsuggesthandlers "github.com/yassinebenameur/probara/api/internal/handlers/depsuggest"
	importhandlers "github.com/yassinebenameur/probara/api/internal/handlers/import"
	locationhandlers "github.com/yassinebenameur/probara/api/internal/handlers/locations"
	maintenancewindowhandlers "github.com/yassinebenameur/probara/api/internal/handlers/maintenancewindows"
	meshhandlers "github.com/yassinebenameur/probara/api/internal/handlers/mesh"
	metricsqueryhandlers "github.com/yassinebenameur/probara/api/internal/handlers/metricsquery"
	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
	pushhandlers "github.com/yassinebenameur/probara/api/internal/handlers/push"
)

// mountLocationRoutes mounts private-location CRUD and deploy info.
func mountLocationRoutes(r chi.Router, locationHandlers *locationhandlers.Handlers) {
	r.Route("/locations", func(r chi.Router) {
		r.Post("/", locationHandlers.CreateLocation)
		r.Get("/", locationHandlers.ListLocations)
		r.Get("/{id}", locationHandlers.GetLocation)
		r.Patch("/{id}", locationHandlers.UpdateLocation)
		r.Delete("/{id}", locationHandlers.DeleteLocation)
		r.Get("/{id}/deploy", locationHandlers.GetDeployInfo)
	})
}

// mountMeshRoutes mounts the inter-location connectivity mesh.
func mountMeshRoutes(r chi.Router, meshHdlrs *meshhandlers.Handlers) {
	r.Route("/mesh", func(r chi.Router) {
		r.Get("/", meshHdlrs.GetMesh)
		r.Get("/history", meshHdlrs.GetEdgeHistory)
		r.Post("/probe", meshHdlrs.ProbeNow)
	})
}

// mountMaintenanceWindowRoutes mounts maintenance-window CRUD.
func mountMaintenanceWindowRoutes(r chi.Router, maintenanceHandlers *maintenancewindowhandlers.Handlers) {
	r.Route("/maintenance-windows", func(r chi.Router) {
		r.Post("/", maintenanceHandlers.CreateMaintenanceWindow)
		r.Get("/", maintenanceHandlers.ListMaintenanceWindows)
		r.Get("/{id}", maintenanceHandlers.GetMaintenanceWindow)
		r.Patch("/{id}", maintenanceHandlers.UpdateMaintenanceWindow)
		r.Delete("/{id}", maintenanceHandlers.DeleteMaintenanceWindow)
	})
}

// mountMonitorRoutes mounts /monitors: CRUD, import/export, bulk operations, history reads, group membership, dependencies, and the per-monitor agent, push, metric and snooze endpoints other handler packages own.
func mountMonitorRoutes(r chi.Router, monitorHandlers *monitorhandlers.Handlers, importHdlrs *importhandlers.Handlers, depSuggestHdlrs *depsuggesthandlers.Handlers, aiLabelHdlrs *ailabelshandlers.Handlers, metricsQueryHandlers *metricsqueryhandlers.Handler, agentHandlers *agenthandlers.Handler, pushHandlers *pushhandlers.Handler, maintenanceHandlers *maintenancewindowhandlers.Handlers) {
	r.Route("/monitors", func(r chi.Router) {
		r.Post("/", monitorHandlers.CreateMonitor)
		r.Get("/", monitorHandlers.ListMonitors)
		r.Post("/test", monitorHandlers.TestMonitorConfig)
		// Import endpoints (must be before /{id} to avoid conflicts)
		r.Get("/export", importHdlrs.Export)
		r.Post("/import/preview", importHdlrs.Preview)
		r.Post("/import", importHdlrs.Execute)
		// Bulk operations (must be before /{id} to avoid conflicts)
		r.Post("/bulk/alerting", monitorHandlers.BulkUpdateAlerting)
		r.Post("/bulk/delete", monitorHandlers.BulkDeleteMonitors)
		// Dependency graph (must be before /{id} to avoid conflicts)
		r.Get("/dependency-graph", monitorHandlers.GetDependencyGraph)
		// Recent results for many monitors (the list's one round trip)
		r.Get("/results/batch", monitorHandlers.GetMonitorResultsBatch)
		r.Post("/dependency-suggestions", depSuggestHdlrs.Suggest)
		r.Post("/dependency-suggestions/label", aiLabelHdlrs.LabelDependencySuggestion)
		r.Get("/{id}", monitorHandlers.GetMonitor)
		r.Get("/{id}/analytics", monitorHandlers.GetMonitorAnalytics)
		r.Get("/{id}/results", monitorHandlers.GetMonitorResults)
		r.Get("/{id}/metrics-snapshots", monitorHandlers.GetMonitorMetricsSnapshots)
		r.Delete("/{id}/history", monitorHandlers.DeleteMonitorHistory)
		r.Post("/{id}/run", monitorHandlers.RunMonitorNow)
		r.Get("/{id}/artifacts/screenshot", monitorHandlers.GetSyntheticBrowserScreenshot)
		r.Patch("/{id}", monitorHandlers.UpdateMonitor)
		r.Delete("/{id}", monitorHandlers.DeleteMonitor)
		// Group membership endpoints
		r.Get("/{id}/members", monitorHandlers.GetGroupMembers)
		r.Post("/{id}/members", monitorHandlers.AddMonitorsToGroup)
		r.Delete("/{id}/members", monitorHandlers.RemoveMonitorsFromGroup)
		// Dependency endpoints
		r.Get("/{id}/dependencies", monitorHandlers.GetMonitorDependencies)
		r.Post("/{id}/dependencies", monitorHandlers.AddMonitorDependency)
		r.Delete("/{id}/dependencies/{dependsOnId}", monitorHandlers.RemoveMonitorDependency)
		r.Get("/{id}/dependents", monitorHandlers.GetMonitorDependents)
		// Metric store (agent monitors' OTel metrics)
		r.Get("/{id}/metrics/series", metricsQueryHandlers.HandleListSeries)
		r.Post("/{id}/metrics/query", metricsQueryHandlers.HandleQuery)
		// Agent install endpoints
		r.Get("/{id}/agent/install", agentHandlers.HandleGetInstallCommand)
		r.Get("/{id}/agent/config.yaml", agentHandlers.HandleGetCollectorConfig)
		r.Get("/{id}/agent/install/script.sh", agentHandlers.HandleGetInstallScript)
		r.Get("/{id}/agent/install/script.ps1", agentHandlers.HandleGetWindowsInstallScript)
		r.Get("/{id}/agent/uninstall/script.sh", agentHandlers.HandleGetUninstallScript)
		r.Get("/{id}/agent/uninstall/script.ps1", agentHandlers.HandleGetWindowsUninstallScript)
		// Push info endpoint
		r.Get("/{id}/push/info", pushHandlers.HandleGetPushInfo)
		// Snooze: quick single-monitor maintenance window
		r.Post("/{id}/snooze", maintenanceHandlers.SnoozeMonitor)
	})
}
