package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "probara",
			Subsystem: "api",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"service", "route", "method", "status_code"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "probara",
			Subsystem: "api",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds",
		},
		[]string{"service", "route", "method"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

// MetricsMiddleware records HTTP metrics
func MetricsMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			route := r.URL.Path
			method := r.Method
			statusCode := strconv.Itoa(wrapped.statusCode)

			httpRequestsTotal.WithLabelValues(serviceName, route, method, statusCode).Inc()
			httpRequestDuration.WithLabelValues(serviceName, route, method).Observe(duration.Seconds())
		})
	}
}
