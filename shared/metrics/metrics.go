package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry wraps Prometheus registry with service-specific metrics
type Registry struct {
	registry *prometheus.Registry
	service  string
}

// NewRegistry creates a new metrics registry for the given service
func NewRegistry(serviceName string) *Registry {
	reg := prometheus.NewRegistry()

	// Register standard Go runtime metrics
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	return &Registry{
		registry: reg,
		service:  serviceName,
	}
}

// GetRegistry returns the underlying Prometheus registry
func (r *Registry) GetRegistry() *prometheus.Registry {
	return r.registry
}

// Handler returns an HTTP handler for the /metrics endpoint
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

// NewCounter creates a new counter metric
func (r *Registry) NewCounter(name, help string, labels []string) *prometheus.CounterVec {
	return promauto.With(r.registry).NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "probara",
			Subsystem: r.service,
			Name:      name,
			Help:      help,
		},
		labels,
	)
}

// NewGauge creates a new gauge metric
func (r *Registry) NewGauge(name, help string, labels []string) *prometheus.GaugeVec {
	return promauto.With(r.registry).NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "probara",
			Subsystem: r.service,
			Name:      name,
			Help:      help,
		},
		labels,
	)
}

// NewHistogram creates a new histogram metric
func (r *Registry) NewHistogram(name, help string, labels []string, buckets []float64) *prometheus.HistogramVec {
	if buckets == nil {
		buckets = prometheus.DefBuckets
	}
	return promauto.With(r.registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "probara",
			Subsystem: r.service,
			Name:      name,
			Help:      help,
			Buckets:   buckets,
		},
		labels,
	)
}
