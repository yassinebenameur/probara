package mcp

import (
	"net/http"
	"reflect"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	otlphandlers "github.com/yassinebenameur/probara/api/internal/handlers/otlp"
)

// documentedRoute is a REST route the OpenAPI document describes although
// it is not an MCP tool (it stays in notExposed for MCP): machine ingest
// that integrations call directly, the alert stream, and the document
// itself.
type documentedRoute struct {
	spec
	// Public routes take no session or API key (security: []); any
	// credential they have is in the path.
	Public bool
	// RequestMedia are raw request body types, described without a schema.
	RequestMedia []string
	Headers      []param
	// Errors are the route's own error answers; they replace the shared
	// answer for the same status.
	Errors map[int]routeError
}

// routeError is one error answer of a documented route: JSON Bodies (the
// types the handlers write; several when the status comes in more than one
// shape), or a schema-less Media type.
type routeError struct {
	Description string
	Bodies      []reflect.Type
	Media       string
	RetryAfter  bool
}

func plainError(description string) routeError {
	return routeError{Description: description, Media: "text/plain"}
}

func openAPIRoutes() []documentedRoute {
	pushToken := map[string]string{"token": "The push monitor's token, from get_push_info. Possession of the URL is the credential."}
	pushQuery := []param{
		{Name: "status", Description: "Result to record (default up).", Enum: []string{"up", "down", "error"}},
		{Name: "error", Description: "Error message to record with a failure."},
		{Name: "metrics", Type: "object", Description: "Any other query parameter is recorded as a metric of the " +
			"same name; numeric and true/false values are parsed, everything else is kept as text."},
	}
	pushErrors := map[int]routeError{
		400: plainError("Invalid status, malformed JSON body, or a body that is not one JSON object."),
		403: plainError("The push monitor is disabled."),
		404: plainError("No push monitor has this token."),
		413: plainError("The body is larger than 64 KiB."),
		414: plainError("The query string is larger than 64 KiB."),
	}
	otlpStatus := reflect.TypeFor[otlphandlers.Status]()
	envelope := reflect.TypeFor[apierrors.ErrorResponse]()
	return []documentedRoute{
		{
			spec: spec{
				Name:  "push_heartbeat",
				Title: "Push heartbeat",
				Description: "Record one result for a push monitor: the monitored job calls this URL on each run. " +
					"Without parameters it records an up result.",
				Method: http.MethodGet, Route: "/push/{token}",
				PathDocs: pushToken, Query: pushQuery, Platform: true,
			},
			Public: true, Errors: pushErrors,
		},
		{
			spec: spec{
				Name:  "push_heartbeat_report",
				Title: "Push heartbeat with report",
				Description: "Record one result for a push monitor, with an optional JSON object body (sent as " +
					"application/json) that overlays the query parameters: status and error as for the GET form, " +
					"every other key a metric.",
				Method: http.MethodPost, Route: "/push/{token}",
				PathDocs: pushToken, Query: pushQuery, Platform: true,
				BodyArg: &param{Name: "report", Type: "object", Description: "status (up, down, error), error, and metrics as further keys."},
			},
			Public: true, Errors: pushErrors,
		},
		{
			spec: spec{
				Name:  "export_otlp_metrics",
				Title: "Export OTLP metrics",
				Description: "OTLP/HTTP metrics export for agent monitors: point an OpenTelemetry collector's otlphttp " +
					"exporter at <origin>/api/v1/otlp with a write-scope API key and the X-Probara-Agent-Id header. " +
					"The body is an ExportMetricsServiceRequest in protobuf or JSON, optionally gzip-compressed " +
					"(Content-Encoding: gzip; 4 MiB compressed, 20 MiB expanded); the answer is an " +
					"ExportMetricsServiceResponse in the request's encoding. Errors raised by the handler use the OTLP " +
					"status shape {\"code\", \"message\"}.",
				Method: http.MethodPost, Route: "/otlp/v1/metrics",
			},
			RequestMedia: []string{"application/x-protobuf", "application/json"},
			Headers: []param{{Name: "X-Probara-Agent-Id", Description: "The agent monitor's agent ID. Without it the " +
				"probara.agent.id resource attribute is used."}},
			Errors: map[int]routeError{
				400: {Description: "Unreadable, malformed or oversized payload (OTLP status); or, for a session, a " +
					"malformed tenant_id (API error envelope).", Bodies: []reflect.Type{otlpStatus, envelope}},
				403: {Description: "The agent monitor is disabled (OTLP status); or a read-only credential or a " +
					"workspace the user is not a member of (API error envelope).", Bodies: []reflect.Type{otlpStatus, envelope}},
				404: {Description: "No agent monitor matches the agent ID in this workspace.", Bodies: []reflect.Type{otlpStatus}},
				415: {Description: "Content-Type is neither application/x-protobuf nor application/json.",
					Bodies: []reflect.Type{otlpStatus}},
				429: {Description: "The monitor's export rate limit (OTLP_MONITOR_RATE_PER_MIN; OTLP status, " +
					"Retry-After: 30), or failed authentication from this address (API error envelope).",
					Bodies: []reflect.Type{otlpStatus, envelope}, RetryAfter: true},
				503: {Description: "Metrics cannot be ingested right now; retry after Retry-After seconds.",
					Bodies: []reflect.Type{otlpStatus}, RetryAfter: true},
			},
		},
		{
			spec: spec{
				Name:  "stream_alerts",
				Title: "Stream alert events",
				Description: "Server-sent events for the workspace's alerts (text/event-stream). The stream opens " +
					"with a connected event, sends a heartbeat event every 15 seconds, and one event per alert " +
					"change: alert_created, alert_acknowledged, alert_resolved or alert_updated, whose data is the " +
					"alert as JSON. Not rate limited once authenticated.",
				Method: http.MethodGet, Route: "/alerts/stream",
			},
		},
		{
			spec: spec{
				Name:  "get_openapi_document",
				Title: "Get the OpenAPI document",
				Description: "This document. Generated from the same catalog as the MCP tools; served to any " +
					"authenticated caller.",
				Method: http.MethodGet, Route: "/openapi.json", Platform: true,
			},
		},
	}
}

// openAPINotExposed: the document is not an operation an agent needs.
var openAPINotExposed = map[string]string{
	"GET " + OpenAPIPath: "the OpenAPI document of these routes; agents already receive each tool's schema",
}

func init() {
	for route, reason := range openAPINotExposed {
		notExposed[route] = reason
	}
}
