package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
)

// PingChecker implements Checker for ping monitors: count ICMP echoes to one
// resolved address, then judge loss and average round trip.
//
// The target is resolved once, here, and every candidate address vetted by
// the worker's SSRF policy (shared/netguard, the same table dialGuard uses);
// the prober is handed the chosen IP, so it never resolves again and DNS
// rebinding between vetting and sending is impossible.
//
// Socket mode (WORKER_PING_MODE) defaults to unprivileged: Linux "ping"
// datagram sockets, which need the process's group inside
// net.ipv4.ping_group_range (the Helm chart and Compose set the sysctl).
// Privileged mode opens raw ICMP sockets and needs CAP_NET_RAW; auto tries
// unprivileged and falls back to raw only on a permission error.
type PingChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
	mode            string

	// lookup and run are seams for tests; production uses the system
	// resolver and pro-bing.
	lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
	run    func(ctx context.Context, req pingRequest) (pingStats, error)
}

// NewPingChecker creates a ping checker that enforces the worker's
// private-IP policy, in unprivileged mode until SetMode says otherwise.
func NewPingChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *PingChecker {
	return &PingChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
		mode:            config.PingModeUnprivileged,
		lookup:          net.DefaultResolver.LookupIPAddr,
		run:             runProbingPing,
	}
}

// SetMode selects the socket mode (config.PingMode*). Call it at startup,
// before any check runs; an unknown value keeps unprivileged.
func (c *PingChecker) SetMode(mode string) {
	switch mode {
	case config.PingModePrivileged, config.PingModeAuto, config.PingModeUnprivileged:
		c.mode = mode
	}
}

// pingRequest is one prober run against an already-vetted address.
type pingRequest struct {
	ip         *net.IPAddr
	count      int
	interval   time.Duration
	size       int
	timeout    time.Duration
	privileged bool
}

// pingStats is what a run observed.
type pingStats struct {
	sent, received        int
	min, avg, max, stddev time.Duration
	privileged            bool
}

type pingMetricsEnvelope struct {
	Ping *pingMetrics `json:"ping,omitempty"`
}

// pingMetrics is recorded under metrics_data.ping. The keys are a stored
// contract (web PingMetrics): add keys, never rename them.
type pingMetrics struct {
	Host        string   `json:"host,omitempty"`
	IP          string   `json:"ip,omitempty"`
	Sent        int      `json:"sent"`
	Received    int      `json:"received"`
	LossPct     float64  `json:"loss_pct"`
	RTTMinMs    *float64 `json:"rtt_min_ms,omitempty"`
	RTTAvgMs    *float64 `json:"rtt_avg_ms,omitempty"`
	RTTMaxMs    *float64 `json:"rtt_max_ms,omitempty"`
	RTTStdDevMs *float64 `json:"rtt_stddev_ms,omitempty"`
	Privileged  bool     `json:"privileged"`
	// RTTWarnMs is set when the average round trip exceeded warn_rtt_ms
	// on a check that still passed (flag only, like latency_warn_ms).
	RTTWarnMs *int64 `json:"rtt_warn_ms,omitempty"`
}

// Check performs a ping check.
func (c *PingChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var cfg models.PingMonitorConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return pingError(fmt.Sprintf("failed to unmarshal ping config: %v", err))
	}
	if err := cfg.Validate(); err != nil {
		return pingError(fmt.Sprintf("invalid ping config: %v", err))
	}
	host := strings.TrimSpace(cfg.Host)

	ip, err := c.resolve(ctx, host)
	if err != nil {
		// A refused address is the checker's policy, and a failed lookup was
		// always an error here: neither is evidence about the target.
		return pingError(err.Error())
	}

	req := pingRequest{
		ip:       ip,
		count:    cfg.EffectiveCount(),
		interval: cfg.EffectiveInterval(),
		size:     cfg.EffectivePacketSize(),
		timeout:  time.Duration(timeoutSeconds) * time.Second,
	}
	stats, err := c.runInMode(ctx, req)
	if err != nil {
		return pingError(fmt.Sprintf("ping failed: %v", err))
	}
	return evaluatePing(cfg, host, ip.IP.String(), stats)
}

// resolve returns the address to ping: the literal itself, else the first
// policy-allowed IPv4 answer, else the first allowed IPv6 one.
func (c *PingChecker) resolve(ctx context.Context, host string) (*net.IPAddr, error) {
	policy := netguard.Policy{BlockPrivate: c.blockPrivateIPs, AllowedCIDRs: c.allowedCIDRs}
	if ip := net.ParseIP(host); ip != nil {
		if !policy.Allows(ip) {
			return nil, fmt.Errorf("%w: ip %s not allowed", netguard.ErrBlocked, host)
		}
		return &net.IPAddr{IP: ip}, nil
	}
	addrs, err := c.lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("dns: %v", err)
	}
	var v6 *net.IPAddr
	blocked := false
	for i := range addrs {
		a := addrs[i]
		if !policy.Allows(a.IP) {
			blocked = true
			continue
		}
		if a.IP.To4() != nil {
			return &a, nil
		}
		if v6 == nil {
			v6 = &a
		}
	}
	if v6 != nil {
		return v6, nil
	}
	if blocked {
		return nil, fmt.Errorf("%w: every address of %s is blocked", netguard.ErrBlocked, host)
	}
	return nil, fmt.Errorf("dns: no addresses found for %s", host)
}

// runInMode runs the prober in the configured socket mode. auto starts
// unprivileged and retries raw only when the datagram socket was refused
// for lack of permission (gid outside ping_group_range).
func (c *PingChecker) runInMode(ctx context.Context, req pingRequest) (pingStats, error) {
	switch c.mode {
	case config.PingModePrivileged:
		req.privileged = true
		return c.run(ctx, req)
	case config.PingModeAuto:
		stats, err := c.run(ctx, req)
		if err != nil && isPermissionError(err) {
			req.privileged = true
			return c.run(ctx, req)
		}
		return stats, err
	default:
		return c.run(ctx, req)
	}
}

func isPermissionError(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

// runProbingPing is the production run: pro-bing against the vetted IP.
func runProbingPing(ctx context.Context, req pingRequest) (pingStats, error) {
	p := probing.New("")
	p.SetIPAddr(req.ip) // no resolve: the vetted address is the one pinged
	p.SetPrivileged(req.privileged)
	p.SetLogger(probing.NoopLogger{})
	p.Count = req.count
	p.Interval = req.interval
	p.Size = req.size
	p.Timeout = req.timeout
	err := p.RunWithContext(ctx)
	st := p.Statistics()
	stats := pingStats{
		sent:       st.PacketsSent,
		received:   st.PacketsRecv,
		min:        st.MinRtt,
		avg:        st.AvgRtt,
		max:        st.MaxRtt,
		stddev:     st.StdDevRtt,
		privileged: req.privileged,
	}
	return stats, err
}

// evaluatePing turns a run into a result: no reply at all, loss above
// max_packet_loss_pct, or an average round trip above max_rtt_ms fails the
// check; warn_rtt_ms only flags it. Latency is the average round trip.
func evaluatePing(cfg models.PingMonitorConfig, host, ip string, s pingStats) CheckResult {
	m := pingMetrics{Host: host, IP: ip, Sent: s.sent, Received: s.received, Privileged: s.privileged}
	if s.sent == 0 {
		metricsJSON, _ := json.Marshal(pingMetricsEnvelope{Ping: &m})
		errMsg := "ping failed: no echo request could be sent"
		return CheckResult{Status: "error", ErrorMessage: &errMsg, MetricsData: metricsJSON}
	}
	received := s.received
	if received > s.sent { // duplicates are not counted, but never report negative loss
		received = s.sent
	}
	m.LossPct = roundTo(float64(s.sent-received)*100/float64(s.sent), 2)

	if received == 0 {
		metricsJSON, _ := json.Marshal(pingMetricsEnvelope{Ping: &m})
		errMsg := "no response received"
		if s.sent > 1 {
			errMsg = fmt.Sprintf("no response received (0/%d echoes answered)", s.sent)
		}
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, MetricsData: metricsJSON}
	}

	m.RTTMinMs = msPtr(s.min)
	m.RTTAvgMs = msPtr(s.avg)
	m.RTTMaxMs = msPtr(s.max)
	m.RTTStdDevMs = msPtr(s.stddev)
	latencyMs := s.avg.Milliseconds()
	avgMs := float64(s.avg) / float64(time.Millisecond)

	var failed []string
	if cfg.MaxPacketLossPct != nil && m.LossPct > *cfg.MaxPacketLossPct {
		failed = append(failed, fmt.Sprintf("packet loss: %s%% > %s%%", trimFloat(m.LossPct), trimFloat(*cfg.MaxPacketLossPct)))
	}
	if cfg.MaxRTTMs != nil && avgMs > float64(*cfg.MaxRTTMs) {
		failed = append(failed, fmt.Sprintf("rtt: avg %sms > %dms", trimFloat(roundTo(avgMs, 1)), *cfg.MaxRTTMs))
	}
	if len(failed) == 0 && cfg.WarnRTTMs != nil && avgMs > float64(*cfg.WarnRTTMs) {
		m.RTTWarnMs = cfg.WarnRTTMs
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
