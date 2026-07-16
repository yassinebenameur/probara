package middleware

import (
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/yassinebenameur/probara/api/internal/services/audit"
)

// auditSkipPrefixes are route subtrees whose handlers emit richer explicit
// audit events themselves — the generic middleware record would be a duplicate.
var auditSkipPrefixes = []string{
	"/api/v1/users",
	"/api/v1/api-keys",
}

// AuditMutations records every mutating request (method, derived action,
// resource, outcome) after it completes. Reads are not recorded.
func AuditMutations(recorder *audit.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			for _, prefix := range auditSkipPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			event := audit.FromRequest(r)
			event.Action = deriveAction(r.Method, r.URL.Path)
			event.ResourceType, event.ResourceID = deriveResource(r.URL.Path)
			event.StatusCode = ww.Status()
			event.Outcome = outcomeFromStatus(ww.Status())
			recorder.Record(event)
		})
	}
}

func outcomeFromStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return audit.OutcomeDenied
	case status >= 400:
		return audit.OutcomeFailure
	default:
		return audit.OutcomeSuccess
	}
}

// deriveAction maps method+path to a stable "resource.verb" action string,
// e.g. PATCH /api/v1/monitors/{id} -> monitor.update,
// POST /api/v1/incidents/{id}/state -> incident.state.
func deriveAction(method, path string) string {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return strings.ToLower(method)
	}

	resource := singularize(segments[0])
	verb := methodVerb(method)

	// Sub-action POSTs (…/{id}/state, …/bulk/delete, …/test) keep the final
	// static segment as the verb for a self-describing action name.
	if last := segments[len(segments)-1]; len(segments) > 1 && !isIDSegment(last) && method == http.MethodPost {
		verb = last
	}

	return resource + "." + verb
}

func deriveResource(path string) (resourceType, resourceID string) {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return "", ""
	}
	resourceType = singularize(segments[0])
	for _, segment := range segments[1:] {
		if isIDSegment(segment) {
			return resourceType, segment
		}
	}
	return resourceType, ""
}

func pathSegments(path string) []string {
	trimmed := strings.TrimPrefix(strings.Trim(path, "/"), "api/v1/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func methodVerb(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

func singularize(resource string) string {
	if strings.HasSuffix(resource, "ses") || !strings.HasSuffix(resource, "s") {
		return resource
	}
	return strings.TrimSuffix(resource, "s")
}

func isIDSegment(segment string) bool {
	// UUIDs and numeric IDs.
	if len(segment) == 36 && strings.Count(segment, "-") == 4 {
		return true
	}
	for _, c := range segment {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(segment) > 0
}
