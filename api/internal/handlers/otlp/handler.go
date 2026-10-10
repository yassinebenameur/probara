// Package otlp is the HTTP face of the OTLP/HTTP metrics endpoint
// (POST /api/v1/otlp/v1/metrics): content negotiation (protobuf/JSON, gzip),
// size caps, and spec-shaped responses. Status codes are chosen for the
// otlphttp exporter's retry semantics — it retries only 429/502/503/504, so
// transient server trouble must surface as 503 (data delayed, not lost)
// while bad requests and unknown agents are terminal 4xx.
package otlp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"

	otlpservice "github.com/yassinebenameur/probara/api/internal/services/otlp"
	"github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	contentTypeProto = "application/x-protobuf"
	contentTypeJSON  = "application/json"

	// AgentIDHeader carries the monitor identity; the collector sets it as a
	// static otlphttp exporter header.
	AgentIDHeader = "X-Probara-Agent-Id"

	maxBodyBytes         = 4 << 20  // compressed (or plain) request body
	maxDecompressedBytes = 20 << 20 // gzip expansion cap
)

// Handler serves the OTLP metrics export endpoint.
type Handler struct {
	service *otlpservice.Service
	log     *logger.Logger
}

// NewHandler creates the OTLP handler.
func NewHandler(service *otlpservice.Service, log *logger.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// HandleExportMetrics handles POST /api/v1/otlp/v1/metrics.
func (h *Handler) HandleExportMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr, ok := context.GetTenantID(ctx)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	contentType := baseContentType(r.Header.Get("Content-Type"))
	if contentType != contentTypeProto && contentType != contentTypeJSON {
		writeStatus(w, http.StatusUnsupportedMediaType, "unsupported content type: use application/x-protobuf or application/json")
		return
	}

	body, err := readBody(r)
	if err != nil {
		writeStatus(w, http.StatusBadRequest, err.Error())
		return
	}

	req := pmetricotlp.NewExportRequest()
	if contentType == contentTypeProto {
		err = req.UnmarshalProto(body)
	} else {
		err = req.UnmarshalJSON(body)
	}
	if err != nil {
		writeStatus(w, http.StatusBadRequest, fmt.Sprintf("malformed OTLP payload: %v", err))
		return
	}

	result, err := h.service.Ingest(ctx, tenantID, r.Header.Get(AgentIDHeader), sha256.Sum256(body), req.Metrics())
	if err != nil {
		switch {
		case errors.Is(err, otlpservice.ErrUnknownAgent):
			writeStatus(w, http.StatusNotFound, "agent monitor not found")
		case errors.Is(err, otlpservice.ErrMonitorDisabled):
			writeStatus(w, http.StatusForbidden, "agent monitor disabled")
		case errors.Is(err, otlpservice.ErrRateLimited):
			w.Header().Set("Retry-After", "30")
			writeStatus(w, http.StatusTooManyRequests, "rate limited")
		default:
			// Transient server-side trouble: 503 keeps the exporter
			// retrying instead of dropping the batch.
			h.log.WithError(err).Error("otlp ingest failed")
			w.Header().Set("Retry-After", "10")
			writeStatus(w, http.StatusServiceUnavailable, "temporarily unable to ingest metrics")
		}
		return
	}

	resp := pmetricotlp.NewExportResponse()
	if result.RejectedPoints > 0 {
		ps := resp.PartialSuccess()
		ps.SetRejectedDataPoints(int64(result.RejectedPoints))
		ps.SetErrorMessage(result.RejectMessage)
	}
	var payload []byte
	if contentType == contentTypeProto {
		payload, err = resp.MarshalProto()
	} else {
		payload, err = resp.MarshalJSON()
	}
	if err != nil {
		h.log.WithError(err).Error("otlp response encode failed")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// readBody enforces the size caps and transparently decompresses gzip.
func readBody(r *http.Request) ([]byte, error) {
	limited := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("request body too large or unreadable (max %d bytes)", maxBodyBytes)
	}
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid gzip body: %v", err)
		}
		defer gz.Close()
		decompressed, err := io.ReadAll(io.LimitReader(gz, maxDecompressedBytes+1))
		if err != nil {
			return nil, fmt.Errorf("invalid gzip body: %v", err)
		}
		if len(decompressed) > maxDecompressedBytes {
			return nil, fmt.Errorf("decompressed body too large (max %d bytes)", maxDecompressedBytes)
		}
		return decompressed, nil
	}
	return raw, nil
}

func baseContentType(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(strings.ToLower(v))
}

// writeStatus emits a google.rpc.Status-shaped JSON error body (the OTLP
// spec's error payload; encoding it as protobuf would add a genproto
// dependency for no collector-side benefit — exporters act on the HTTP
// status code and log the message).
func writeStatus(w http.ResponseWriter, httpCode int, message string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(httpCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    httpCode,
		"message": message,
	})
}
