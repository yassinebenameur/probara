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

	"github.com/yassinebenameur/probara/shared/netguard"
)

// fakePinger records the request and answers with fixed stats.
type fakePinger struct {
	stats pingStats
	err   error
	got   *pingRequest
}

func (f *fakePinger) run(_ context.Context, req pingRequest) (pingStats, error) {
	f.got = &req
	return f.stats, f.err
}

func pingCheckerWith(f *fakePinger, blockPrivate bool, allowed ...string) *PingChecker {
	var cidrs []*net.IPNet
	for _, c := range allowed {
		_, n, _ := net.ParseCIDR(c)
		cidrs = append(cidrs, n)
	}
	c := NewPingChecker(blockPrivate, cidrs)
	c.run = f.run
	return c
}

func pingMetricsOf(t *testing.T, res CheckResult) pingMetrics {
	t.Helper()
	var env pingMetricsEnvelope
	if err := json.Unmarshal(res.MetricsData, &env); err != nil || env.Ping == nil {
		t.Fatalf("metrics_data = %s (%v)", res.MetricsData, err)
	}
	return *env.Ping
}

func ms(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }

// A bare host (every ping monitor saved before echoes were counted) sends
// three echoes 250 ms apart and stops a second after the last one, never at
// the monitor's timeout; one lost echo of three does not fail it: only
// total loss did before, and only total loss does by default.
func TestPingChecker_DefaultsSendThreeEchoes(t *testing.T) {
	f := &fakePinger{stats: pingStats{sent: 3, received: 2, min: ms(10), avg: ms(12.5), max: ms(15)}}
	res := pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 10)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success", res.Status, errText(res.ErrorMessage))
	}
	if f.got == nil || f.got.count != 3 || f.got.interval != 250*time.Millisecond || f.got.timeout != 1500*time.Millisecond || f.got.ip.String() != "192.0.2.10" {
		t.Fatalf("request = %+v, want 3 echoes 250ms apart stopping after 1.5s", f.got)
	}
	m := pingMetricsOf(t, res)
	if m.Sent != 3 || m.Received != 2 || m.LossPct != 33.33 || m.IntervalMs != 250 || *m.RTTMinMs != 10 || *m.RTTAvgMs != 12.5 || *m.RTTMaxMs != 15 || m.IP != "192.0.2.10" {
		t.Fatalf("metrics = %+v", m)
	}
	if res.LatencyMs == nil || *res.LatencyMs != 12 {
		t.Fatalf("latency = %v, want the average round trip", res.LatencyMs)
	}
}

// A monitor saved with a 1 s timeout and a slow explicit gap keeps working
// after the upgrade: the echoes are squeezed to fit, never sent past it.
func TestPingChecker_ShortTimeoutCompressesInterval(t *testing.T) {
	f := &fakePinger{stats: pingStats{sent: 3, received: 3, avg: ms(1)}}
	res := pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10","interval_ms":1000}`), 1)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s)", res.Status, errText(res.ErrorMessage))
	}
	if f.got.count != 3 || f.got.interval < 240*time.Millisecond || f.got.interval > 250*time.Millisecond || f.got.timeout > time.Second {
		t.Fatalf("request = %+v, want 3 echoes ~250ms apart inside 1s", f.got)
	}
	if m := pingMetricsOf(t, res); m.IntervalMs != f.got.interval.Milliseconds() {
		t.Fatalf("interval_ms = %d, want the interval actually used", m.IntervalMs)
	}
}

// Spacing comes from the time resolving left: after 0.8 s of a 2 s timeout
// went to DNS, every echo and its reply wait still end inside the timeout.
func TestPingChecker_SlowResolutionShrinksSchedule(t *testing.T) {
	f := &fakePinger{stats: pingStats{sent: 3, received: 3, avg: ms(1)}}
	c := pingCheckerWith(f, false)
	c.resolveHook = func(ctx context.Context, host string) (net.IP, error) {
		time.Sleep(800 * time.Millisecond)
		return net.ParseIP("192.0.2.10"), nil
	}
	res := c.Check(context.Background(), json.RawMessage(`{"host":"slow.example","interval_ms":500}`), 2)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s)", res.Status, errText(res.ErrorMessage))
	}
	if f.got.timeout > 1200*time.Millisecond || 2*f.got.interval >= f.got.timeout {
		t.Fatalf("request = %+v, want a schedule inside the 1.2s left", f.got)
	}
}

func TestPingChecker_Thresholds(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		stats   pingStats
		status  string
		message string
	}{
		{"total loss fails", `{"host":"192.0.2.10"}`, pingStats{sent: 3}, "failure", "no response received (0/3 echoes answered)"},
		{"loss above threshold", `{"host":"192.0.2.10","count":4,"max_packet_loss_pct":25}`, pingStats{sent: 4, received: 2, avg: ms(5)}, "failure", "packet loss: 50% > 25% (2/4 echoes answered)"},
		{"loss at threshold passes", `{"host":"192.0.2.10","count":4,"max_packet_loss_pct":25}`, pingStats{sent: 4, received: 3, avg: ms(5)}, "success", ""},
		{"zero loss allowed", `{"host":"192.0.2.10","max_packet_loss_pct":0}`, pingStats{sent: 3, received: 2, avg: ms(5)}, "failure", "packet loss: 33.33% > 0% (2/3 echoes answered)"},
		{"rtt above threshold", `{"host":"192.0.2.10","max_rtt_ms":100}`, pingStats{sent: 3, received: 3, min: ms(90), avg: ms(120.44), max: ms(150)}, "failure", "rtt: avg 120.4ms > 100ms"},
		{"both", `{"host":"192.0.2.10","max_rtt_ms":100,"max_packet_loss_pct":10}`, pingStats{sent: 3, received: 2, avg: ms(120)}, "failure", "packet loss: 33.33% > 10% (2/3 echoes answered); rtt: avg 120ms > 100ms"},
		{"duplicates never count as negative loss", `{"host":"192.0.2.10"}`, pingStats{sent: 3, received: 4, avg: ms(5)}, "success", ""},
		{"nothing sent is an error", `{"host":"192.0.2.10"}`, pingStats{}, "error", "ping failed: no echo request could be sent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := pingCheckerWith(&fakePinger{stats: tc.stats}, false).Check(context.Background(), json.RawMessage(tc.config), 10)
			if res.Status != tc.status || errText(res.ErrorMessage) != tc.message {
				t.Fatalf("result = %q %q, want %q %q", res.Status, errText(res.ErrorMessage), tc.status, tc.message)
			}
			if m := pingMetricsOf(t, res); m.LossPct < 0 || m.Received > m.Sent {
				t.Fatalf("metrics = %+v", m)
			}
		})
	}
}

// The target is resolved and vetted by the worker's SSRF policy before any
// echo, for IPv4 and IPv6 alike, and a refused target is an error (the
// checker's policy), never a failure.
func TestPingChecker_SSRFPolicy(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "::1", "fd00::5", "localhost"} {
		f := &fakePinger{stats: pingStats{sent: 3, received: 3}}
		res := pingCheckerWith(f, true).Check(context.Background(), json.RawMessage(fmt.Sprintf(`{"host":%q}`, host)), 5)
		if res.Status != "error" || !strings.Contains(errText(res.ErrorMessage), netguard.ErrBlocked.Error()) {
			t.Fatalf("%s: result = %q %q, want an ssrf_blocked error", host, res.Status, errText(res.ErrorMessage))
		}
		if f.got != nil {
			t.Fatalf("%s: an echo was sent to a blocked address", host)
		}
	}

	// Allowed explicitly, and IPv6 literals go through as IPv6.
	f := &fakePinger{stats: pingStats{sent: 3, received: 3}}
	res := pingCheckerWith(f, true, "fd00::/8").Check(context.Background(), json.RawMessage(`{"host":"fd00::5"}`), 5)
	if res.Status != "success" || f.got.ip.String() != "fd00::5" {
		t.Fatalf("allow-listed IPv6: %q %v", res.Status, f.got)
	}
	f = &fakePinger{stats: pingStats{sent: 3, received: 3}}
	res = pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(`{"host":"2001:db8::7"}`), 5)
	if res.Status != "success" || f.got.ip.String() != "2001:db8::7" {
		t.Fatalf("public IPv6: %q %v", res.Status, f.got)
	}
	// A name is resolved once and the vetted address is what gets pinged.
	f = &fakePinger{stats: pingStats{sent: 3, received: 3}}
	res = pingCheckerWith(f, true, "127.0.0.0/8", "::1/128").Check(context.Background(), json.RawMessage(`{"host":"localhost"}`), 5)
	if res.Status != "success" || f.got == nil || !f.got.ip.IsLoopback() {
		t.Fatalf("allow-listed localhost: %q %v", res.Status, f.got)
	}
}

// Without the sysctl the socket cannot be opened: the error says so instead
// of a bare "permission denied". A send a firewall refuses is not that.
func TestPingChecker_SocketDenied(t *testing.T) {
	denied := &net.OpError{Op: "listen", Net: "udp4", Err: os.NewSyscallError("socket", syscall.EACCES)}
	res := pingCheckerWith(&fakePinger{err: denied}, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 5)
	if res.Status != "error" || errText(res.ErrorMessage) != pingSocketDenied {
		t.Fatalf("result = %q %q", res.Status, errText(res.ErrorMessage))
	}

	refused := &net.OpError{Op: "write", Net: "udp4", Err: os.NewSyscallError("sendto", syscall.EPERM)}
	res = pingCheckerWith(&fakePinger{err: refused}, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 5)
	if res.Status != "error" || !strings.HasPrefix(errText(res.ErrorMessage), "ping failed: ") {
		t.Fatalf("send refusal = %q %q", res.Status, errText(res.ErrorMessage))
	}
}

// A run the deadline cut short is judged on what it saw.
func TestPingChecker_DeadlineKeepsPartialStats(t *testing.T) {
	f := &fakePinger{stats: pingStats{sent: 3, received: 1, avg: ms(4)}, err: context.DeadlineExceeded}
	res := pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 5)
	if res.Status != "success" || pingMetricsOf(t, res).Received != 1 {
		t.Fatalf("result = %q %q", res.Status, errText(res.ErrorMessage))
	}
	f = &fakePinger{err: errors.New("network is unreachable")}
	res = pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(`{"host":"192.0.2.10"}`), 5)
	if res.Status != "error" {
		t.Fatalf("run error = %q, want error", res.Status)
	}
}

func TestPingChecker_InvalidConfigIsError(t *testing.T) {
	for _, raw := range []string{`{"host":""}`, `{"host":"192.0.2.1","count":11}`, `{"host":"192.0.2.1","max_packet_loss_pct":100}`, `nope`} {
		f := &fakePinger{}
		if res := pingCheckerWith(f, false).Check(context.Background(), json.RawMessage(raw), 5); res.Status != "error" || f.got != nil {
			t.Errorf("%s: status = %q, sent = %v", raw, res.Status, f.got != nil)
		}
	}
}

// The real prober against loopback, over an unprivileged ICMP socket. It
// skips when this machine cannot run it: a Linux group outside
// net.ipv4.ping_group_range, or a host that drops ICMP to 127.0.0.1 (some
// macOS setups do) — neither says anything about the checker.
func TestPingChecker_LiveLoopback(t *testing.T) {
	res := NewPingChecker(false, nil).Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1","count":2,"interval_ms":200}`), 3)
	switch {
	case res.Status == "error" && errText(res.ErrorMessage) == pingSocketDenied:
		t.Skip("unprivileged ICMP sockets are not permitted here (net.ipv4.ping_group_range)")
	case res.Status == "failure" && strings.HasPrefix(errText(res.ErrorMessage), "no response received"):
		t.Skip("this host drops ICMP echo to 127.0.0.1")
	case res.Status != "success":
		t.Fatalf("status = %q (%s)", res.Status, errText(res.ErrorMessage))
	}
	if m := pingMetricsOf(t, res); m.Sent != 2 || m.Received != 2 || m.RTTAvgMs == nil {
		t.Fatalf("metrics = %+v", m)
	}
}
