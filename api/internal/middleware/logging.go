package middleware

import (
	"net/http"
	"time"

	"github.com/yassinebenameur/probara/shared/logger"
)

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			log.WithFields(map[string]interface{}{
				"method":      r.Method,
				"path":        r.URL.Path,
				"status_code": wrapped.statusCode,
				"duration_ms": duration.Milliseconds(),
				"request_id":  GetRequestID(r.Context()),
			}).Info("HTTP request")
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
