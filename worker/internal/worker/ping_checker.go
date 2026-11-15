package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/yassinebenameur/probara/shared/models"
)

// PingChecker implements Checker for ping monitors: several ICMP echoes to
// one address, judged on loss and average round trip.
//
// The target goes through the worker's SSRF policy like every dial
// (dialGuard's Policy, shared/netguard): the name is resolved once, every
// address vetted, and the prober is handed the chosen IP, so it never
// resolves again and DNS rebinding between vetting and sending is
// impossible.
//
// Echoes always go over unprivileged ICMP ("ping") datagram sockets, never
// raw sockets: no capability or setuid, but on Linux the worker's group must
// sit inside net.ipv4.ping_group_range (the Helm chart and Compose set it).
// A worker outside it reports an error that names the sysctl.
type PingChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet

	// run is the prober; a test seam (production: pro-bing).
	run func(ctx context.Context, req pingRequest) (pingStats, error)
}

// NewPingChecker creates a ping checker that enforces the worker's
// private-IP policy.
func NewPingChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *PingChecker {
	return &PingChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs, run: runProbingPing}
}

// pingRequest is one prober run against an already-vetted address.
type pingRequest struct {
	ip       net.IP
	count    int
	interval time.Duration
	timeout  time.Duration
}

// pingStats is what a run observed.
type pingStats struct {
	sent, received int
	min, avg, max  time.Duration
}

type pingMetricsEnvelope struct {
	Ping *pingMetrics `json:"ping,omitempty"`
}

// pingMetrics is recorded under metrics_data.ping. The keys are a stored
// contract (web PingMetrics): add keys, never rename them.
type pingMetrics struct {
	IP       string  `json:"ip,omitempty"`
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	LossPct  float64 `json:"loss_pct"`
	// IntervalMs is the gap actually used between echoes: the configured
	// one, or shorter when the timeout could not hold it (PingSchedule).
	IntervalMs int64    `json:"interval_ms"`
	RTTMinMs   *float64 `json:"rtt_min_ms,omitempty"`
	RTTAvgMs   *float64 `json:"rtt_avg_ms,omitempty"`
	RTTMaxMs   *float64 `json:"rtt_max_ms,omitempty"`
}

// pingSocketDenied is the error a worker outside ping_group_range reports.
// It names the cause, since "permission denied" alone reads like a target
// problem.
const pingSocketDenied = "ping: this worker may not open unprivileged ICMP sockets; " +
	"its group must be inside the net.ipv4.ping_group_range sysctl (the Helm chart and Compose set it)"

// Check performs a ping check.
func (c *PingChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var cfg models.PingMonitorConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return pingError(fmt.Sprintf("failed to unmarshal ping config: %v", err))
	}
	if err := cfg.Validate(); err != nil {
		return pingError(fmt.Sprintf("invalid ping config: %v", err))
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ip, err := c.resolve(checkCtx, strings.TrimSpace(cfg.Host))
	if err != nil {
		// A refused address is the checker's policy and a failed lookup was
		// always an error here: neither is evidence about the target.
		return pingError(err.Error())
	}

	count, interval := cfg.PingSchedule(timeout)
	stats, err := c.run(checkCtx, pingRequest{ip: ip, count: count, interval: interval, timeout: timeout})
	if err != nil {
		if isPingSocketDenied(err) {
			return pingError(pingSocketDenied)
		}
		// Cut short by the deadline: what was sent and received still
		// counts, judged below. Any other run error decides nothing.
		if !errors.Is(err, context.DeadlineExceeded) || stats.sent == 0 {
			return pingError(fmt.Sprintf("ping failed: %v", err))
		}
	}
	return evaluatePing(cfg, ip.String(), interval, stats)
}

// isPingSocketDenied reports whether err is the kernel refusing to open the
// ICMP socket at all (the group outside ping_group_range), as opposed to a
// send a local firewall refused, which fails with EPERM too.
func isPingSocketDenied(err error) bool {
	var se *os.SyscallError
	return errors.As(err, &se) && se.Syscall == "socket" &&
		(errors.Is(se.Err, syscall.EPERM) || errors.Is(se.Err, syscall.EACCES))
}

// resolve returns the address to ping through the worker's SSRF policy:
// the first allowed IPv4 answer, else the first allowed IPv6 one. IPv4
// first is what the prober chose by itself before the policy applied.
func (c *PingChecker) resolve(ctx context.Context, host string) (net.IP, error) {
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, 0)
	addrs, err := guard.Policy.ResolveAllowed(ctx, host)
	if err != nil {
		if isSSRFBlocked(err) {
			return nil, err
		}
		return nil, fmt.Errorf("dns: %v", err)
	}
	for _, ip := range addrs {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	return addrs[0], nil
}

// runProbingPing is the production run: pro-bing against the vetted IP over
// an unprivileged ("udp") ICMP socket.
func runProbingPing(ctx context.Context, req pingRequest) (pingStats, error) {
	p := probing.New("")
	p.SetIPAddr(&net.IPAddr{IP: req.ip}) // no resolve: the vetted address is the one pinged
	p.SetPrivileged(false)
	p.SetLogger(probing.NoopLogger{})
	p.Count = req.count
	p.Interval = req.interval
	p.Timeout = req.timeout
	err := p.RunWithContext(ctx)
	st := p.Statistics()
	return pingStats{sent: st.PacketsSent, received: st.PacketsRecv, min: st.MinRtt, avg: st.AvgRtt, max: st.MaxRtt}, err
}

// evaluatePing turns a run into a result: no reply at all, loss above
// max_packet_loss_pct, or an average round trip above max_rtt_ms fails the
// check. Latency is the average round trip.
func evaluatePing(cfg models.PingMonitorConfig, ip string, interval time.Duration, s pingStats) CheckResult {
	m := pingMetrics{IP: ip, Sent: s.sent, Received: s.received, IntervalMs: interval.Milliseconds()}
	if s.sent == 0 {
		metricsJSON, _ := json.Marshal(pingMetricsEnvelope{Ping: &m})
		errMsg := "ping failed: no echo request could be sent"
		return CheckResult{Status: "error", ErrorMessage: &errMsg, MetricsData: metricsJSON}
	}
	received := min(s.received, s.sent) // duplicates never make loss negative
	m.Received = received
	m.LossPct = roundTo(float64(s.sent-received)*100/float64(s.sent), 2)

	if received == 0 {
		metricsJSON, _ := json.Marshal(pingMetricsEnvelope{Ping: &m})
		errMsg := fmt.Sprintf("no response received (0/%d echoes answered)", s.sent)
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, MetricsData: metricsJSON}
	}

	m.RTTMinMs = msPtr(s.min)
	m.RTTAvgMs = msPtr(s.avg)
	m.RTTMaxMs = msPtr(s.max)
	latencyMs := s.avg.Milliseconds()
	avgMs := float64(s.avg) / float64(time.Millisecond)

	var failed []string
	if cfg.MaxPacketLossPct != nil && m.LossPct > *cfg.MaxPacketLossPct {
		failed = append(failed, fmt.Sprintf("packet loss: %s%% > %s%% (%d/%d echoes answered)",
			trimFloat(m.LossPct), trimFloat(*cfg.MaxPacketLossPct), received, s.sent))
	}
	if cfg.MaxRTTMs != nil && avgMs > float64(*cfg.MaxRTTMs) {
		failed = append(failed, fmt.Sprintf("rtt: avg %sms > %dms", trimFloat(roundTo(avgMs, 1)), *cfg.MaxRTTMs))
	}

	metricsJSON, _ := json.Marshal(pingMetricsEnvelope{Ping: &m})
	if len(failed) > 0 {
		errMsg := strings.Join(failed, "; ")
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs, MetricsData: metricsJSON}
	}
	return CheckResult{Status: "success", LatencyMs: &latencyMs, MetricsData: metricsJSON}
}

func pingError(msg string) CheckResult {
	return CheckResult{Status: "error", ErrorMessage: &msg}
}

func msPtr(d time.Duration) *float64 {
	v := roundTo(float64(d)/float64(time.Millisecond), 3)
	return &v
}

func roundTo(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}
