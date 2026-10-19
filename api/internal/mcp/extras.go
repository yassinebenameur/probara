package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Extras are the tools that have no 1:1 route. describe_monitor_type reads
// only compile-time registries (validation.DefaultRegistry, the secret field
// table); wait_for_alert_events reads the alert stream through the router,
// so tenant scoping and auth stay the SSE handler's.
func (h *Handler) Extras() []extraTool {
	return []extraTool{
		{readOnly: true, def: describeMonitorTypeTool(), handler: describeMonitorType},
		{readOnly: true, def: listMonitorTypesTool(), handler: listMonitorTypes},
		{readOnly: true, def: waitForAlertEventsTool(), handler: h.waitForAlertEvents},
	}
}

func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no}
}

func listMonitorTypesTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "list_monitor_types",
		Title:       "List monitor types",
		Description: "Every monitor type this installation supports, whether it is an active check (runs on a schedule with a timeout) and which config fields are secrets.",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Annotations: readOnlyAnnotations("List monitor types"),
	}
}

type monitorTypeInfo struct {
	Type             string             `json:"type"`
	ActiveCheck      bool               `json:"active_check"`
	SecretFields     []string           `json:"secret_fields,omitempty"`
	SecretMapFields  []string           `json:"secret_map_fields,omitempty"`
	ConfigSchema     *jsonschema.Schema `json:"config_schema,omitempty"`
	ConfigSchemaNote string             `json:"config_schema_note,omitempty"`
}

func typeInfo(t string, withSchema bool) (monitorTypeInfo, error) {
	mt := models.MonitorType(t)
	info := monitorTypeInfo{
		Type:            t,
		ActiveCheck:     validation.IsActiveCheckType(mt),
		SecretFields:    secrets.MonitorSecretFields[t],
		SecretMapFields: secrets.MonitorSecretMapFields[t],
	}
	if !withSchema {
		return info, nil
	}
	typ, ok := validation.DefaultRegistry.ConfigType(mt)
	if !ok {
		return info, fmt.Errorf("monitor type %q has no config schema", t)
	}
	schema, err := bodySchema(typ)
	if err != nil {
		return info, err
	}
	info.ConfigSchema = schema
	info.ConfigSchemaNote = "Reflected from the struct the validator decodes. Required fields are not marked; " +
		"create_monitor / test_monitor_config answer with the exact validation error. Secret fields read back as \"***\"."
	return info, nil
}

func describeMonitorTypeTool() *mcp.Tool {
	return &mcp.Tool{
		Name:  "describe_monitor_type",
		Title: "Describe monitor type",
		Description: "The JSON schema of a monitor type's config object, its secret fields, and whether it is an " +
			"active check. Call before create_monitor, update_monitor or test_monitor_config.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"type": {Type: "string", Description: "Monitor type.", Enum: anySlice(monitorTypes())},
			},
			Required: []string{"type"},
		},
		Annotations: readOnlyAnnotations("Describe monitor type"),
	}
}

func describeMonitorType(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(orEmpty(req.Params.Arguments), &args); err != nil {
		return toolError("arguments: " + err.Error()), nil
	}
	if !validation.DefaultRegistry.Has(models.MonitorType(args.Type)) {
		return toolError(fmt.Sprintf("unknown monitor type %q; known types: %s", args.Type, strings.Join(monitorTypes(), ", "))), nil
	}
	info, err := typeInfo(args.Type, true)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return jsonResult(info)
}

func listMonitorTypes(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var out []monitorTypeInfo
	for _, t := range monitorTypes() {
		info, err := typeInfo(t, false)
		if err != nil {
			return toolError(err.Error()), nil
		}
		out = append(out, info)
	}
	return jsonResult(out)
}

const maxAlertWait = 60 * time.Second

func waitForAlertEventsTool() *mcp.Tool {
	return &mcp.Tool{
		Name:  "wait_for_alert_events",
		Title: "Wait for alert events",
		Description: "Listen to the workspace's live alert stream and return the alert events (created, " +
			"acknowledged, resolved) that arrive within the timeout. Returns as soon as one arrives, or an empty " +
			"list when the window passes quietly. Use it to watch a fix land or an outage unfold.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"timeout_seconds": {Type: "integer", Description: "How long to listen, 1-60 (default 30)."},
				tenantArg:         {Type: "string", Format: "uuid", Description: "Workspace (sign-in connections only)."},
			},
		},
		Annotations: readOnlyAnnotations("Wait for alert events"),
	}
}

type alertEvent struct {
	Event string          `json:"event"`
	Alert json.RawMessage `json:"alert"`
}

func (h *Handler) waitForAlertEvents(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		TimeoutSeconds int    `json:"timeout_seconds"`
		TenantID       string `json:"tenant_id"`
	}
	if err := json.Unmarshal(orEmpty(req.Params.Arguments), &args); err != nil {
		return toolError("arguments: " + err.Error()), nil
	}
	wait := time.Duration(args.TimeoutSeconds) * time.Second
	if wait <= 0 {
		wait = 30 * time.Second
	}
	if wait > maxAlertWait {
		wait = maxAlertWait
	}

	streamTool := &tool{spec: spec{Name: "wait_for_alert_events", Method: http.MethodGet, Route: "/alerts/stream"}}
	streamTool.argSet = map[string]struct{}{tenantArg: {}}
	raw, _ := json.Marshal(map[string]string{tenantArg: args.TenantID})
	if args.TenantID == "" {
		raw = nil
	}

	started := time.Now()
	listenCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var header http.Header
	if req.Extra != nil {
		header = req.Extra.Header
	}
	inner, err := streamTool.buildRequest(listenCtx, raw, header)
	if err != nil {
		return toolError(err.Error()), nil
	}
	inner.Header.Set("Accept", "text/event-stream")

	sw := newStreamRecorder(cancel)
	h.router.ServeHTTP(sw, inner)

	if status := sw.status(); status >= 400 {
		return toolError(fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(sw.text()))), nil
	}
	return jsonResult(map[string]any{"events": sw.events(), "listened_seconds": int(time.Since(started).Round(time.Second).Seconds())})
}

// streamRecorder collects an SSE response and ends the stream (cancels the
// inner request) as soon as the first alert event is complete.
type streamRecorder struct {
	mu     sync.Mutex
	header http.Header
	code   int
	buf    bytes.Buffer
	stop   context.CancelFunc
}

func newStreamRecorder(stop context.CancelFunc) *streamRecorder {
	return &streamRecorder{header: http.Header{}, stop: stop}
}

func (s *streamRecorder) Header() http.Header { return s.header }

func (s *streamRecorder) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = code
	}
}

func (s *streamRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		s.code = http.StatusOK
	}
	if s.buf.Len() < maxCapture {
		s.buf.Write(p)
	}
	if len(parseAlertEvents(s.buf.Bytes())) > 0 {
		s.stop()
	}
	return len(p), nil
}

func (s *streamRecorder) Flush() {}

func (s *streamRecorder) status() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == 0 {
		return http.StatusOK
	}
	return s.code
}

func (s *streamRecorder) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *streamRecorder) events() []alertEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return parseAlertEvents(s.buf.Bytes())
}

// parseAlertEvents reads complete SSE events (terminated by a blank line)
// and keeps the alert_* ones; connected and heartbeat events are dropped.
func parseAlertEvents(stream []byte) []alertEvent {
	out := []alertEvent{}
	var event, data string
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 64<<10), maxCapture)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if strings.HasPrefix(event, "alert_") && json.Valid([]byte(data)) {
				out = append(out, alertEvent{Event: event, Alert: json.RawMessage(data)})
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return out
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
}

func orEmpty(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage("{}")
	}
	return raw
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
