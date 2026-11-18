package mcp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
)

// OpenAPIPath is where the document is served, to authenticated callers.
const OpenAPIPath = apiPrefix + "/openapi.json"

// openAPICacheControl: the document only changes with a deploy, but it is
// served to authenticated callers only, so no shared cache may keep it. The
// ETag makes a revalidation after max-age a 304.
const openAPICacheControl = "private, max-age=300"

// openAPIDocument is the document generated at Bind, with its gzip encoding
// and strong validators computed once (it is ~700 KB uncompressed).
type openAPIDocument struct {
	json     []byte
	gzip     []byte
	etag     string // of the identity encoding
	gzipETag string // a strong validator differs per content coding
	untyped  []string
	err      error
}

// prepareOpenAPI generates the document. A failure is logged and leaves the
// endpoint answering 503; the API and the MCP server keep running.
func (h *Handler) prepareOpenAPI(routes map[string]gate, tools []*tool) {
	h.openapi = newOpenAPIDocument(routes, tools, h.version)
	if h.openapi.err != nil && h.log != nil {
		h.log.WithError(h.openapi.err).Error("OpenAPI document generation failed; GET " + OpenAPIPath + " answers 503")
	}
}

func newOpenAPIDocument(routes map[string]gate, tools []*tool, version string) *openAPIDocument {
	documented, err := resolveDocumented(routes)
	if err != nil {
		return &openAPIDocument{err: err}
	}
	doc, untyped, err := buildOpenAPI(tools, documented, version)
	if err != nil {
		return &openAPIDocument{err: err}
	}
	var gz bytes.Buffer
	zw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err == nil {
		_, err = zw.Write(doc)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		return &openAPIDocument{err: fmt.Errorf("openapi: gzip: %w", err)}
	}
	sum := sha256.Sum256(doc)
	tag := hex.EncodeToString(sum[:16])
	return &openAPIDocument{
		json:     doc,
		gzip:     gz.Bytes(),
		etag:     `"` + tag + `"`,
		gzipETag: `"` + tag + `-gzip"`,
		untyped:  untyped,
	}
}

// OpenAPI returns the generated document, or nil before Bind or when
// generation failed (see OpenAPIError).
func (h *Handler) OpenAPI() []byte {
	if h.openapi == nil {
		return nil
	}
	return h.openapi.json
}

// OpenAPIError is why the document could not be generated, if it could not.
func (h *Handler) OpenAPIError() error {
	if h.openapi == nil {
		return fmt.Errorf("openapi: not bound")
	}
	return h.openapi.err
}

// OpenAPIUntyped lists the described operations without a typed success
// response (no successResponses entry), sorted.
func (h *Handler) OpenAPIUntyped() []string {
	if h.openapi == nil {
		return nil
	}
	return h.openapi.untyped
}

// ServeOpenAPI serves the generated document. Mount it behind the API's auth
// middleware: it lists every operation, including the admin ones.
func (h *Handler) ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	d := h.openapi
	if d == nil || d.err != nil {
		apierrors.WriteError(w, http.StatusServiceUnavailable, "openapi_unavailable",
			"the OpenAPI document could not be generated; the API log has the cause")
		return
	}
	body, etag := d.json, d.etag
	gzipped := acceptsGzip(r.Header.Get("Accept-Encoding"))
	if gzipped {
		body, etag = d.gzip, d.gzipETag
	}
	header := w.Header()
	header.Set("Cache-Control", openAPICacheControl)
	header.Set("Vary", "Accept-Encoding")
	header.Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, d.etag, d.gzipETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", "application/json")
	if gzipped {
		header.Set("Content-Encoding", "gzip")
	}
	header.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// acceptsGzip reports whether an Accept-Encoding value allows gzip (an
// explicit q=0 refuses it; "*" accepts it).
func acceptsGzip(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding != "gzip" && coding != "x-gzip" && coding != "*" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		if q > 0 {
			return true
		}
	}
	return false
}

// etagMatches applies If-None-Match's weak comparison: "*" or any listed
// tag equal to one of ours, ignoring a W/ prefix. Either encoding's tag
// matches, since both name the same document.
func etagMatches(header string, tags ...string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" {
			return true
		}
		for _, tag := range tags {
			if candidate == tag {
				return true
			}
		}
	}
	return false
}
