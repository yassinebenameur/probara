package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/yassinebenameur/probara/shared/logger"
)

// RecoveryMiddleware recovers from panics and logs the error
func RecoveryMiddleware(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.WithFields(map[string]interface{}{
						"error":      fmt.Sprintf("%v", err),
						"stack":      string(debug.Stack()),
						"path":       r.URL.Path,
						"method":     r.Method,
						"request_id": GetRequestID(r.Context()),
					}).Error("Panic recovered")

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"internal_error","message":"An internal error occurred"}`))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
