package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPingMonitorConfigValidate(t *testing.T) {
	valid := []string{
		`{"host":"example.com"}`, // every config saved before counted echoes
		`{"host":"192.0.2.1"}`,
		`{"host":"2001:db8::1"}`,
		`{"host":"example.com","count":1}`,
		`{"host":"example.com","count":10,"interval_ms":200}`,
		`{"host":"example.com","interval_ms":10000}`,
		`{"host":"example.com","max_packet_loss_pct":0}`,
		`{"host":"example.com","max_packet_loss_pct":99.9,"max_rtt_ms":250}`,
	}
	for _, raw := range valid {
		var c PingMonitorConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v, want nil", raw, err)
		}
	}
	invalid := map[string]string{
		`{"host":""}`:                                      "host is required",
		`{"host":"bad host"}`:                              "valid IP address or hostname",
		`{"host":"example.com","count":0}`:                 "count must be between 1 and 10",
		`{"host":"example.com","count":11}`:                "count must be between 1 and 10",
		`{"host":"example.com","interval_ms":199}`:         "interval_ms must be between 200 and 10000",
		`{"host":"example.com","interval_ms":10001}`:       "interval_ms must be between 200 and 10000",
		`{"host":"example.com","max_packet_loss_pct":100}`: "max_packet_loss_pct",
		`{"host":"example.com","max_packet_loss_pct":-1}`:  "max_packet_loss_pct",
		`{"host":"example.com","max_rtt_ms":0}`:            "max_rtt_ms must be greater than 0",
	}
	for raw, want := range invalid {
		var c PingMonitorConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Validate() = %v, want %q", raw, err, want)
		}
	}
}

func intp(n int) *int { return &n }

// The schedule always fits the timeout: a default interval shrinks, the last
// reply keeps PingReplyWaitMs (or half a short timeout).
func TestPingSchedule(t *testing.T) {
	cases := []struct {
		name         string
		cfg          PingMonitorConfig
		timeout      time.Duration
		wantCount    int
		wantInterval time.Duration
	}{
		{"defaults fit a 10s timeout", PingMonitorConfig{}, 10 * time.Second, 3, time.Second},
		{"defaults fit exactly in 3s", PingMonitorConfig{}, 3 * time.Second, 3, time.Second},
		{"defaults squeezed into 2s", PingMonitorConfig{}, 2 * time.Second, 3, 500 * time.Millisecond},
		{"defaults squeezed into 1s", PingMonitorConfig{}, time.Second, 3, 250 * time.Millisecond},
		{"one echo keeps its interval", PingMonitorConfig{Count: intp(1)}, time.Second, 1, time.Second},
		{"explicit interval squeezed too", PingMonitorConfig{Count: intp(5), IntervalMs: intp(2000)}, 5 * time.Second, 5, time.Second},
		{"ten echoes in 1s", PingMonitorConfig{Count: intp(10)}, time.Second, 10, 55 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			count, interval := tc.cfg.PingSchedule(tc.timeout)
			if count != tc.wantCount || interval != tc.wantInterval {
				t.Fatalf("PingSchedule(%s) = %d, %s; want %d, %s", tc.timeout, count, interval, tc.wantCount, tc.wantInterval)
			}
			if count > 1 {
				wait := tc.timeout - time.Duration(count-1)*interval
				if wait < min(PingReplyWaitMs*time.Millisecond, tc.timeout/2) {
					t.Fatalf("last reply gets %s", wait)
				}
			}
		})
	}
}

func TestValidatePingSchedule(t *testing.T) {
	cases := []struct {
		config  string
		timeout int
		want    string
	}{
		// Every timeout the API accepts keeps a bare host valid: no existing
		// monitor is refused on its next save.
		{`{"host":"a.example"}`, 1, ""},
		{`{"host":"a.example"}`, 5, ""},
		{`{"host":"a.example","interval_ms":1000}`, 3, ""},
		{`{"host":"a.example","interval_ms":1000}`, 2, "timeout_seconds must be at least 3 for 3 echoes 1000 ms apart"},
		{`{"host":"a.example","count":10,"interval_ms":500}`, 5, "timeout_seconds must be at least 6 for 10 echoes 500 ms apart"},
		{`{"host":"a.example","count":10}`, 1, "timeout_seconds must be at least 3 for 10 echoes"},
		{`{"host":"a.example","count":10}`, 3, ""},
		{`{"host":"a.example","count":1,"interval_ms":10000}`, 1, ""},
		{`{"host":""}`, 1, ""}, // ValidateConfig's to report
	}
	for _, tc := range cases {
		err := ValidatePingSchedule(json.RawMessage(tc.config), tc.timeout)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("ValidatePingSchedule(%s, %d) = %v, want %q", tc.config, tc.timeout, err, tc.want)
		}
	}
}
