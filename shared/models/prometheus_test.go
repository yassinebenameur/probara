package models

import "testing"

func TestPrometheusComparison(t *testing.T) {
	threshold := 5.0
	for _, tc := range []struct {
		op   string
		v    float64
		want bool
	}{
		{"lt", 4, true}, {"lt", 5, false}, {"lte", 5, true}, {"lte", 6, false},
		{"gt", 6, true}, {"gt", 5, false}, {"gte", 5, true}, {"gte", 4, false},
		{"eq", 5, true}, {"eq", 4, false}, {"ne", 4, true}, {"ne", 5, false},
	} {
		c := PrometheusMonitorConfig{Operator: tc.op, Threshold: &threshold}
		if got := c.Matches(tc.v); got != tc.want {
			t.Errorf("%g %s 5 = %v", tc.v, tc.op, got)
		}
	}
}
