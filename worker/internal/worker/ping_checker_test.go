package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/models"
)

// fakePinger records the requests a PingChecker makes and answers from a
// script, so loss and RTT evaluation run without sockets.
type fakePinger struct {
	reqs    []pingRequest
	results []fakePingResult
}

type fakePingResult struct {
	stats pingStats
	err   error
}

func (f *fakePinger) run(_ context.Context, req pingRequest) (pingStats, error) {
	f.reqs = append(f.reqs, req)
	r := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	r.stats.privileged = req.privileged
	return r.stats, r.err
}

func newFakePingChecker(block bool, allowed []*net.IPNet, fake *fakePinger, lookup map[string][]string) *PingChecker {
	c := NewPingChecker(block, allowed)
	c.run = fake.run
	c.lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		ips, ok := lookup[host]
		if !ok {
			return nil, fmt.Errorf("lookup %s: no such host", host)
		}
		out := make([]net.IPAddr, 0, len(ips))
		for _, ip := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
		}
		return out, nil
	}
	return c
}

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func TestEvaluatePing(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	f := func(v float64) *float64 { return &v }
	rtt := func(sent, recv int, avg float64) pingStats {
		return pingStats{sent: sent, received: recv, min: ms(avg / 2), avg: ms(avg), max: ms(avg * 2), stddev: ms(1)}
	}
	tests := []struct {
		name       string
		cfg        models.PingMonitorConfig
		stats      pingStats
		wantStatus string
		wantErr    string
		wantLoss   float64
		wantWarn   bool
	}{
		{"all answered", models.PingMonitorConfig{}, rtt(3, 3, 12), "success", "", 0, false},
		{"partial loss, no threshold passes", models.PingMonitorConfig{}, rtt(3, 2, 12), "success", "", 33.33, false},
		{"total loss fails", models.PingMonitorConfig{}, pingStats{sent: 3}, "failure", "0/3 echoes", 100, false},
		{"single echo lost keeps legacy message", models.PingMonitorConfig{}, pingStats{sent: 1}, "failure", "no response received", 100, false},
		{"loss within threshold", models.PingMonitorConfig{MaxPacketLossPct: f(34)}, rtt(3, 2, 12), "success", "", 33.33, false},
		{"loss over threshold", models.PingMonitorConfig{MaxPacketLossPct: f(20)}, rtt(5, 3, 12), "failure", "packet loss: 40% > 20%", 40, false},
		{"zero tolerance", models.PingMonitorConfig{MaxPacketLossPct: f(0)}, rtt(10, 9, 12), "failure", "packet loss: 10% > 0%", 10, false},
		{"avg rtt within max", models.PingMonitorConfig{MaxRTTMs: i64(50)}, rtt(3, 3, 49.9), "success", "", 0, false},
		{"avg rtt over max", models.PingMonitorConfig{MaxRTTMs: i64(50)}, rtt(3, 3, 50.4), "failure", "rtt: avg 50.4ms > 50ms", 0, false},
		{"warn only flags", models.PingMonitorConfig{WarnRTTMs: i64(10), MaxRTTMs: i64(50)}, rtt(3, 3, 20), "success", "", 0, true},
		{"failing check carries no warn flag", models.PingMonitorConfig{WarnRTTMs: i64(10), MaxRTTMs: i64(50)}, rtt(3, 3, 80), "failure", "rtt: avg 80ms", 0, false},
		{"both thresholds fail together", models.PingMonitorConfig{MaxPacketLossPct: f(0), MaxRTTMs: i64(5)}, rtt(2, 1, 20), "failure", "packet loss: 50% > 0%; rtt: avg 20ms > 5ms", 50, false},
		{"nothing sent is an error", models.PingMonitorConfig{}, pingStats{}, "error", "no echo request could be sent", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := evaluatePing(tt.cfg, "example.com", "192.0.2.1", tt.stats)
			if res.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (err %v)", res.Status, tt.wantStatus, deref(res.ErrorMessage))
			}
			if tt.wantErr != "" && !strings.Contains(deref(res.ErrorMessage), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", deref(res.ErrorMessage), tt.wantErr)
			}
			if tt.wantErr == "" && res.ErrorMessage != nil {
				t.Fatalf("unexpected error %q", *res.ErrorMessage)
			}
			var env pingMetricsEnvelope
			if err := json.Unmarshal(res.MetricsData, &env); err != nil || env.Ping == nil {
				t.Fatalf("metrics_data %s: %v", res.MetricsData, err)
			}
			if env.Ping.LossPct != tt.wantLoss {
				t.Fatalf("loss_pct = %v, want %v", env.Ping.LossPct, tt.wantLoss)
			}
			if (env.Ping.RTTWarnMs != nil) != tt.wantWarn {
				t.Fatalf("rtt_warn_ms = %v, want set=%v", env.Ping.RTTWarnMs, tt.wantWarn)
			}
			if tt.stats.received > 0 {
				if env.Ping.RTTMinMs == nil || env.Ping.RTTAvgMs == nil || env.Ping.RTTMaxMs == nil || env.Ping.RTTStdDevMs == nil {
					t.Fatalf("rtt fields missing: %s", res.MetricsData)
				}
				if res.LatencyMs == nil || *res.LatencyMs != tt.stats.avg.Milliseconds() {
					t.Fatalf("latency = %v, want avg %d", res.LatencyMs, tt.stats.avg.Milliseconds())
				}
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestPingCheckerSendsConfiguredEchoes(t *testing.T) {
	fake := &fakePinger{results: []fakePingResult{{stats: pingStats{sent: 3, received: 3, avg: ms(5)}}}}
	c := newFakePingChecker(false, nil, fake, map[string][]string{"example.com": {"2001:db8::1", "93.184.216.34"}})
	res := c.Check(context.Background(), json.RawMessage(`{"host":"example.com","count":3,"interval_ms":250,"packet_size":56}`), 7)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s)", res.Status, deref(res.ErrorMessage))
	}
	req := fake.reqs[0]
	if req.count != 3 || req.interval != 250*time.Millisecond || req.size != 56 || req.timeout != 7*time.Second || req.privileged {
		t.Fatalf("request = %+v", req)
	}
	if got := req.ip.IP.String(); got != "93.184.216.34" {
		t.Fatalf("pinged %s, want the IPv4 answer", got)
	}
}

func TestPingCheckerLegacyConfigSendsOneEcho(t *testing.T) {
	fake := &fakePinger{results: []fakePingResult{{stats: pingStats{sent: 1, received: 1, avg: ms(5)}}}}
	c := newFakePingChecker(false, nil, fake, nil)
	if res := c.Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 10); res.Status != "success" {
		t.Fatalf("status = %q", res.Status)
	}
	if fake.reqs[0].count != 1 || fake.reqs[0].interval != time.Second {
		t.Fatalf("legacy request = %+v, want one echo", fake.reqs[0])
	}
}

func TestPingCheckerRefusesPrivateTargets(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("10.1.0.0/16")
	lookup := map[string][]string{
		"internal.example": {"10.0.0.5"},
		"mixed.example":    {"169.254.169.254", "93.184.216.34"},
		"allowed.example":  {"10.1.2.3"},
	}
	tests := []struct {
		host       string
		wantStatus string
		wantIP     string
	}{
		{"127.0.0.1", "error", ""},
		{"169.254.169.254", "error", ""},
		{"internal.example", "error", ""},
		{"mixed.example", "success", "93.184.216.34"},
		{"allowed.example", "success", "10.1.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			fake := &fakePinger{results: []fakePingResult{{stats: pingStats{sent: 1, received: 1, avg: ms(1)}}}}
			c := newFakePingChecker(true, []*net.IPNet{allowed}, fake, lookup)
			res := c.Check(context.Background(), json.RawMessage(`{"host":"`+tt.host+`"}`), 5)
			if res.Status != tt.wantStatus {
				t.Fatalf("status = %q (%s), want %q", res.Status, deref(res.ErrorMessage), tt.wantStatus)
			}
			if tt.wantStatus == "error" {
				if !strings.HasPrefix(deref(res.ErrorMessage), "ssrf_blocked") {
					t.Fatalf("error = %q, want ssrf_blocked", deref(res.ErrorMessage))
				}
				if len(fake.reqs) != 0 {
					t.Fatal("a refused target was pinged")
				}
				return
			}
			if got := fake.reqs[0].ip.IP.String(); got != tt.wantIP {
				t.Fatalf("pinged %s, want %s", got, tt.wantIP)
			}
		})
	}
}

func TestPingCheckerPrivateTargetsAllowedWithoutBlock(t *testing.T) {
	fake := &fakePinger{results: []fakePingResult{{stats: pingStats{sent: 1, received: 1, avg: ms(1)}}}}
	c := newFakePingChecker(false, nil, fake, nil)
	if res := c.Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1"}`), 5); res.Status != "success" {
		t.Fatalf("status = %q (%s)", res.Status, deref(res.ErrorMessage))
	}
}

func TestPingCheckerModes(t *testing.T) {
	permErr := &net.OpError{Op: "listen", Net: "udp4", Err: os.NewSyscallError("socket", syscall.EACCES)}
	ok := fakePingResult{stats: pingStats{sent: 1, received: 1, avg: ms(1)}}
	tests := []struct {
		mode         string
		results      []fakePingResult
		wantPrivRuns []bool
		wantStatus   string
		wantPriv     bool
	}{
		{config.PingModeUnprivileged, []fakePingResult{ok}, []bool{false}, "success", false},
		{config.PingModeUnprivileged, []fakePingResult{{err: permErr}}, []bool{false}, "error", false},
		{config.PingModePrivileged, []fakePingResult{ok}, []bool{true}, "success", true},
		{config.PingModeAuto, []fakePingResult{{err: permErr}, ok}, []bool{false, true}, "success", true},
		{config.PingModeAuto, []fakePingResult{{err: errors.New("network is unreachable")}}, []bool{false}, "error", false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			fake := &fakePinger{results: tt.results}
			c := newFakePingChecker(false, nil, fake, nil)
			c.SetMode(tt.mode)
			res := c.Check(context.Background(), json.RawMessage(`{"host":"192.0.2.1"}`), 5)
			if res.Status != tt.wantStatus {
				t.Fatalf("status = %q (%s), want %q", res.Status, deref(res.ErrorMessage), tt.wantStatus)
			}
			if len(fake.reqs) != len(tt.wantPrivRuns) {
				t.Fatalf("runs = %d, want %d", len(fake.reqs), len(tt.wantPrivRuns))
			}
			for i, want := range tt.wantPrivRuns {
				if fake.reqs[i].privileged != want {
					t.Fatalf("run %d privileged = %v, want %v", i, fake.reqs[i].privileged, want)
				}
			}
			if res.Status == "success" {
				var env pingMetricsEnvelope
				_ = json.Unmarshal(res.MetricsData, &env)
				if env.Ping.Privileged != tt.wantPriv {
					t.Fatalf("metrics privileged = %v, want %v", env.Ping.Privileged, tt.wantPriv)
				}
			}
		})
	}
}

func TestPingCheckerRejectsInvalidConfig(t *testing.T) {
	fake := &fakePinger{results: []fakePingResult{{}}}
	c := newFakePingChecker(false, nil, fake, nil)
	res := c.Check(context.Background(), json.RawMessage(`{"host":"h","count":50}`), 5)
	if res.Status != "error" || len(fake.reqs) != 0 {
		t.Fatalf("status = %q runs = %d, want error and no run", res.Status, len(fake.reqs))
	}
}
