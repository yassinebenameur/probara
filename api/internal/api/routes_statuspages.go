// Routes for status pages, their custom templates and the tenant template library.

package api

import (
	"github.com/go-chi/chi/v5"

	statuspagehandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspages"
)

// mountStatusPageRoutes mounts status-page CRUD, the per-page template lifecycle and the template library.
func mountStatusPageRoutes(r chi.Router, statusPageHandlers *statuspagehandlers.Handlers) {
	r.Route("/status-pages", func(r chi.Router) {
		r.Post("/", statusPageHandlers.CreateStatusPage)
		r.Get("/", statusPageHandlers.ListStatusPages)
		r.Get("/{id}", statusPageHandlers.GetStatusPage)
		r.Patch("/{id}", statusPageHandlers.UpdateStatusPage)
		r.Delete("/{id}", statusPageHandlers.DeleteStatusPage)

		// Custom UI template lifecycle: export default, draft, publish,
		// revert, reset. Sources are validated against the shared
		// template contract before they are stored.
		r.Route("/{id}/template", func(r chi.Router) {
			r.Get("/", statusPageHandlers.GetTemplateState)
			r.Delete("/", statusPageHandlers.ResetTemplate)
			r.Get("/default", statusPageHandlers.GetDefaultTemplate)
			r.Get("/versions/{version}/source", statusPageHandlers.GetTemplateVersionSource)
			r.Put("/draft", statusPageHandlers.SaveTemplateDraft)
			r.Delete("/draft", statusPageHandlers.DiscardTemplateDraft)
			r.Post("/publish", statusPageHandlers.PublishTemplate)
			r.Post("/revert", statusPageHandlers.RevertTemplate)
		})
	})

	// Tenant-level library of reusable status page templates.
	r.Route("/status-page-templates", func(r chi.Router) {
		r.Get("/", statusPageHandlers.ListLibraryTemplates)
		r.Post("/", statusPageHandlers.CreateLibraryTemplate)
		r.Get("/{templateId}", statusPageHandlers.GetLibraryTemplate)
		r.Get("/{templateId}/source", statusPageHandlers.GetLibraryTemplateSource)
		r.Patch("/{templateId}", statusPageHandlers.UpdateLibraryTemplate)
		r.Delete("/{templateId}", statusPageHandlers.DeleteLibraryTemplate)
	})
}
