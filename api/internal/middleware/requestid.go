package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// RequestIDMiddleware injects a unique request ID into the request context
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		ctx := ctxpkg.WithRequestID(r.Context(), requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID extracts the request ID from the context
func GetRequestID(ctx context.Context) string {
	requestID, ok := ctxpkg.GetRequestID(ctx)
	if !ok {
		return ""
	}
	return requestID
}
