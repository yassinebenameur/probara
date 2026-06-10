package metrics

import (
	"strings"
	"testing"
)

// Prometheus metric names cannot contain '-'. NewRegistry must sanitize a
// dashed service name (e.g. "status-page") so the promauto constructors do
// not panic and the resulting metrics carry an underscored subsystem.
func TestNewRegistrySanitizesDashedServiceName(t *testing.T) {
	reg := NewRegistry("status-page")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("metric constructor panicked with dashed service name: %v", r)
		}
	}()
	gauge := reg.NewGauge("sse_subscriber_connected", "test gauge", nil)
	gauge.WithLabelValues().Set(1)
	counter := reg.NewCounter("events_total", "test counter", []string{"kind"})
	counter.WithLabelValues("a").Inc()
	histogram := reg.NewHistogram("duration_seconds", "test histogram", nil, nil)
	histogram.WithLabelValues().Observe(0.1)

	families, err := reg.GetRegistry().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, fam := range families {
		names[fam.GetName()] = true
	}

	for _, want := range []string{
		"probara_status_page_sse_subscriber_connected",
		"probara_status_page_events_total",
		"probara_status_page_duration_seconds",
	} {
		if !names[want] {
			t.Errorf("metric %q not registered; got families: %v", want, keys(names))
		}
		if !strings.Contains(want, "status_page") {
			t.Fatalf("test expectation %q must contain sanitized subsystem status_page", want)
		}
	}
	for name := range names {
		if strings.Contains(name, "-") {
			t.Errorf("metric name %q contains '-', subsystem was not sanitized", name)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
