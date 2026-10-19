package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	mimemultipart "mime/multipart"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

const (
	// maxCapture bounds what one inner response may buffer in memory; a
	// route that writes more is reported as truncated rather than held.
	maxCapture = 16 << 20
	// maxTextResult is what an agent gets back as text. Larger bodies are
	// cut with a note pointing at the tool's narrowing arguments: a
	// megabyte of JSON helps no model and overflows most clients.
	maxTextResult = 256 << 10
)

// call runs one tool: it rebuilds the REST request the tool stands for and
// dispatches it through the root router in process, so the call passes the
// exact middleware chain (auth, audit, write gate, role gates) a direct API
// call does. Nothing here decides access; the inner request does.
func (h *Handler) call(ctx context.Context, t *tool, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var header http.Header
	if req.Extra != nil {
		header = req.Extra.Header
	}
	inner, err := t.buildRequest(ctx, req.Params.Arguments, header)
	if err != nil {
		return toolError(err.Error()), nil
	}
	rec := newRecorder()
	h.router.ServeHTTP(rec, inner)
	return t.result(rec), nil
}

// buildRequest maps flat tool arguments onto the route: path parameters into
// the path, query parameters into the query string, tenant_id into the
// X-Tenant-ID header, and everything else into the JSON body.
func (t *tool) buildRequest(ctx context.Context, raw json.RawMessage, outer http.Header) (*http.Request, error) {
	args := map[string]json.RawMessage{}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(trimmed, &args); err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object: %v", err)
		}
	}

	path := t.spec.Route
	for _, name := range t.pathParams {
		value, ok, err := scalarArg(args, name)
		if err != nil {
			return nil, err
		}
		if !ok || value == "" {
			return nil, fmt.Errorf("missing required argument %q", name)
		}
		path = strings.Replace(path, "{"+name+"}", url.PathEscape(value), 1)
	}

	query := url.Values{}
	for _, q := range t.spec.Query {
		values, ok, err := queryArg(args, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			if q.Required {
				return nil, fmt.Errorf("missing required argument %q", q.Name)
			}
			continue
		}
		for _, v := range values {
			query.Add(q.Name, v)
		}
	}

	tenantID, _, err := scalarArg(args, tenantArg)
	if err != nil {
		return nil, err
	}

	var body []byte
	contentType := "application/json"
	switch {
	case t.spec.Multipart != nil:
		body, contentType, err = multipartBody(args, t.spec.Multipart)
		if err != nil {
			return nil, err
		}
	case t.spec.BodyArg != nil:
		if v, ok := args[t.spec.BodyArg.Name]; ok {
			body = v
		} else if t.spec.BodyArg.Required {
			return nil, fmt.Errorf("missing required argument %q", t.spec.BodyArg.Name)
		}
	case t.spec.Body != nil:
		// Always an object, even with no fields: some handlers reject an
		// empty body (the AI connection test demands exactly {}).
		fields := map[string]json.RawMessage{}
		for name, v := range args {
			if _, ok := t.bodyFields[name]; ok {
				fields[name] = v
			}
		}
		body, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}

	if unknown := t.unknownArgs(args); len(unknown) > 0 {
		return nil, fmt.Errorf("unknown argument(s) %s; this tool accepts: %s",
			strings.Join(unknown, ", "), strings.Join(t.argNames(), ", "))
	}

	target := apiPrefix + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	// detached keeps the caller's cancellation but none of the outer
	// request's context values: chi reuses a RouteContext it finds in the
	// context (the outer /mcp match), and the inner chain must resolve the
	// identity from scratch rather than inherit it.
	inner, err := http.NewRequestWithContext(withVia(detached{ctx}), t.spec.Method, target, reader)
	if err != nil {
		return nil, err
	}
	// Bearer credential only. The MCP route strips cookies before it ever
	// gets here; copying the header set explicitly keeps it that way.
	if auth := outer.Get("Authorization"); auth != "" {
		inner.Header.Set("Authorization", auth)
	}
	if xff := outer.Get("X-Forwarded-For"); xff != "" {
		inner.Header.Set("X-Forwarded-For", xff)
	}
	ua := "probara-mcp"
	if outerUA := outer.Get("User-Agent"); outerUA != "" {
		ua = outerUA + " " + ua
	}
	inner.Header.Set("User-Agent", ua)
	inner.Header.Set("Accept", "application/json")
	if body != nil {
		inner.Header.Set("Content-Type", contentType)
	}
	if tenantID != "" {
		inner.Header.Set("X-Tenant-ID", tenantID)
	}
	return inner, nil
}

// multipartBody wraps text content as the form file `file` that the import
// preview handler parses, so an agent can preview an import from text.
func multipartBody(args map[string]json.RawMessage, m *multipart) ([]byte, string, error) {
	content, ok, err := scalarArg(args, m.ContentArg)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("missing required argument %q", m.ContentArg)
	}
	filename, _, err := scalarArg(args, m.FilenameArg)
	if err != nil {
		return nil, "", err
	}
	if filename == "" {
		filename = "import"
	}
	var buf bytes.Buffer
	mw := mimemultipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write([]byte(content)); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func (t *tool) unknownArgs(args map[string]json.RawMessage) []string {
	var unknown []string
	for name := range args {
		if _, ok := t.argSet[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func (t *tool) argNames() []string {
	names := make([]string, 0, len(t.argSet))
	for name := range t.argSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scalarArg reads a string-ish argument; numbers and booleans are accepted
// and formatted, since models often send an id or a count unquoted.
func scalarArg(args map[string]json.RawMessage, name string) (string, bool, error) {
	raw, ok := args[name]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false, fmt.Errorf("argument %q: %v", name, err)
	}
	s, err := formatScalar(v)
	if err != nil {
		return "", false, fmt.Errorf("argument %q: %v", name, err)
	}
	return s, true, nil
}

func queryArg(args map[string]json.RawMessage, q param) ([]string, bool, error) {
	raw, ok := args[q.Name]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, fmt.Errorf("argument %q: %v", q.Name, err)
	}
	if list, isList := v.([]any); isList {
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, err := formatScalar(item)
			if err != nil {
				return nil, false, fmt.Errorf("argument %q: %v", q.Name, err)
			}
			out = append(out, s)
		}
		return out, true, nil
	}
	s, err := formatScalar(v)
	if err != nil {
		return nil, false, fmt.Errorf("argument %q: %v", q.Name, err)
	}
	return []string{s}, true, nil
}

func formatScalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a string, number or boolean")
	}
}

// result turns the recorded REST response into a tool result. API errors
// come back as tool errors carrying the API's own message, so the model can
// correct its arguments and retry.
func (t *tool) result(rec *recorder) *mcp.CallToolResult {
	status := rec.status()
	body := rec.buf.Bytes()
	mediaType, _, _ := mime.ParseMediaType(rec.Header().Get("Content-Type"))

	if status >= 400 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 4096 {
			msg = msg[:4096] + "…"
		}
		return toolError(fmt.Sprintf("HTTP %d %s: %s", status, http.StatusText(status), msg))
	}
	if rec.overflow {
		return toolError(fmt.Sprintf("the response exceeded %d MiB; narrow the request with this tool's filter or limit arguments", maxCapture>>20))
	}
	if len(body) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("OK (HTTP %d, no content)", status)}}}
	}

	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: body, MIMEType: mediaType}}}
	case isTextual(mediaType):
		text := strings.TrimRight(string(body), "\n")
		if len(text) > maxTextResult {
			text = text[:maxTextResult] + fmt.Sprintf(
				"\n\n[truncated: the full response is %d bytes; narrow it with this tool's filter, limit or time-range arguments]",
				len(body))
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	default:
		name := rec.Header().Get("Content-Disposition")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI:      "probara://download/" + t.spec.Name + dispositionSuffix(name),
			MIMEType: mediaType,
			Blob:     body,
		}}}}
	}
}

func isTextual(mediaType string) bool {
	return mediaType == "" ||
		strings.HasPrefix(mediaType, "text/") ||
		mediaType == "application/json" ||
		strings.HasSuffix(mediaType, "+json") ||
		mediaType == "application/yaml" ||
		mediaType == "application/x-yaml" ||
		mediaType == "application/x-sh" ||
		mediaType == "application/x-shellscript"
}

func dispositionSuffix(disposition string) string {
	if _, params, err := mime.ParseMediaType(disposition); err == nil && params["filename"] != "" {
		return "/" + url.PathEscape(params["filename"])
	}
	return ""
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// recorder is a bounded in-memory ResponseWriter for inner dispatches.
type recorder struct {
	header   http.Header
	code     int
	buf      bytes.Buffer
	overflow bool
}

func newRecorder() *recorder { return &recorder{header: http.Header{}} }

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	if room := maxCapture - r.buf.Len(); len(p) > room {
		r.overflow = true
		if room > 0 {
			r.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return r.buf.Write(p)
}

// Flush is a no-op; buffered handlers may still assert http.Flusher.
func (r *recorder) Flush() {}

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

// detached forwards cancellation and deadline but hides every value of the
// parent context. See buildRequest for why the inner request needs that.
type detached struct{ parent context.Context }

func (d detached) Deadline() (time.Time, bool) { return d.parent.Deadline() }
func (d detached) Done() <-chan struct{}       { return d.parent.Done() }
func (d detached) Err() error                  { return d.parent.Err() }
func (d detached) Value(any) any               { return nil }

// withVia marks the inner request as MCP-originated; the audit recorder
// copies it into the event details.
func withVia(ctx context.Context) context.Context {
	return ctxpkg.WithVia(ctx, ctxpkg.ViaMCP)
}

// clientIP is the address recorded for audit when no proxy header exists.
func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
