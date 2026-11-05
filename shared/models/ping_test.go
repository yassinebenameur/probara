package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPingMonitorConfigValidate(t *testing.T) {
	i := func(v int) *int { return &v }
	i64 := func(v int64) *int64 { return &v }
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name    string
		cfg     PingMonitorConfig
		wantErr string
	}{
		{"host only (legacy single echo)", PingMonitorConfig{Host: "example.com"}, ""},
		{"ip literal", PingMonitorConfig{Host: "2001:db8::1"}, ""},
		{"full config", PingMonitorConfig{Host: "8.8.8.8", Count: i(3), IntervalMs: i(500), PacketSize: i(56),
			MaxPacketLossPct: f(34), MaxRTTMs: i64(200), WarnRTTMs: i64(100)}, ""},
		{"missing host", PingMonitorConfig{}, "host is required"},
		{"bad host", PingMonitorConfig{Host: "not a host"}, "valid IP address or hostname"},
		{"count zero", PingMonitorConfig{Host: "h", Count: i(0)}, "count must be between 1 and 10"},
		{"count eleven", PingMonitorConfig{Host: "h", Count: i(11)}, "count must be between 1 and 10"},
		{"interval below floor", PingMonitorConfig{Host: "h", IntervalMs: i(199)}, "interval_ms"},
		{"interval at floor", PingMonitorConfig{Host: "h", IntervalMs: i(200)}, ""},
		{"interval above cap", PingMonitorConfig{Host: "h", IntervalMs: i(10001)}, "interval_ms"},
		{"packet too small", PingMonitorConfig{Host: "h", PacketSize: i(23)}, "packet_size"},
		{"packet too large", PingMonitorConfig{Host: "h", PacketSize: i(1473)}, "packet_size"},
		{"loss negative", PingMonitorConfig{Host: "h", MaxPacketLossPct: f(-1)}, "max_packet_loss_pct"},
		{"loss 100 unreachable", PingMonitorConfig{Host: "h", MaxPacketLossPct: f(100)}, "max_packet_loss_pct"},
		{"loss zero", PingMonitorConfig{Host: "h", MaxPacketLossPct: f(0)}, ""},
		{"max rtt zero", PingMonitorConfig{Host: "h", MaxRTTMs: i64(0)}, "max_rtt_ms"},
		{"warn rtt zero", PingMonitorConfig{Host: "h", WarnRTTMs: i64(0)}, "warn_rtt_ms"},
		{"warn not below max", PingMonitorConfig{Host: "h", MaxRTTMs: i64(100), WarnRTTMs: i64(100)}, "warn_rtt_ms must be below max_rtt_ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPingConfigDefaultsKeepSingleEcho(t *testing.T) {
	var cfg PingMonitorConfig
	if err := json.Unmarshal([]byte(`{"host":"example.com"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveCount() != 1 || cfg.EffectiveInterval().Milliseconds() != 1000 || cfg.EffectivePacketSize() != PingMinPacketSize {
		t.Fatalf("defaults = %d/%v/%d, want 1/1s/%d", cfg.EffectiveCount(), cfg.EffectiveInterval(), cfg.EffectivePacketSize(), PingMinPacketSize)
	}
}

func TestValidatePingSchedule(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		timeout int
		wantErr bool
	}{
		{"legacy single echo, 1s", `{"host":"h"}`, 1, false},
		{"3 echoes 1s apart need 3s", `{"host":"h","count":3}`, 3, false},
		{"3 echoes 1s apart in 2s", `{"host":"h","count":3}`, 2, true},
		{"10 echoes 200ms apart in 3s", `{"host":"h","count":10,"interval_ms":200}`, 3, false},
		{"10 echoes 1s apart in 5s", `{"host":"h","count":10}`, 5, true},
		{"unparseable is ValidateConfig's", `{"count":"x"}`, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePingSchedule(json.RawMessage(tt.config), 60, tt.timeout)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePingSchedule() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
