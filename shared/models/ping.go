package models

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// Ping config bounds. The API validator and the worker both apply them
// through PingMonitorConfig.Validate and PingSchedule.
const (
	// PingDefaultCount applies to every monitor without a count, including
	// those saved before the field existed: three echoes, so one lost packet
	// is measured as loss instead of deciding the check on its own.
	PingDefaultCount = 3
	PingMaxCount     = 10
	// PingDefaultIntervalMs keeps a healthy three-echo check near half a
	// second (the last echo leaves 500 ms after the first), so a ping holds
	// a worker slot about as long as one round trip, not seconds.
	PingDefaultIntervalMs = 250
	// PingMinIntervalMs is the smallest gap an operator may configure
	// (iputils' floor for unprivileged senders).
	PingMinIntervalMs = 200
	PingMaxIntervalMs = 10000
	// PingReplyWaitMs is how long a check waits for the last echo's reply
	// when its time allows; less time gets half of itself. An echo answered
	// later than that counts as lost.
	PingReplyWaitMs = 1000
)

// PingMonitorConfig represents configuration for ping monitors. Only host is
// required: absent count and interval mean three echoes a second apart, and
// absent thresholds mean the check fails only when no echo is answered.
type PingMonitorConfig struct {
	Host string `json:"host"`
	// Count is the number of echo requests per check (1-10, absent = 3).
	Count *int `json:"count,omitempty"`
	// IntervalMs is the gap between echo requests (200-10000, absent = 250;
	// PingSchedule shortens it to fit the time a check has).
	IntervalMs *int `json:"interval_ms,omitempty"`
	// MaxPacketLossPct fails the check when more than this share of echoes
	// went unanswered (0 to below 100). Absent: only 100% loss fails.
	MaxPacketLossPct *float64 `json:"max_packet_loss_pct,omitempty"`
	// MaxRTTMs fails the check when the average round trip exceeds it.
	MaxRTTMs *int64 `json:"max_rtt_ms,omitempty"`
}

// EffectiveCount is the number of echoes a check sends.
func (c PingMonitorConfig) EffectiveCount() int {
	if c.Count == nil {
		return PingDefaultCount
	}
	return *c.Count
}

// Validate checks a ping config. The worker runs it on every job too, so a
// rule added here must hold for configs saved before it existed (a bare
// host, which every rule below accepts).
func (c PingMonitorConfig) Validate() error {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if net.ParseIP(host) == nil && !IsValidHostname(host) {
		return fmt.Errorf("host must be a valid IP address or hostname")
	}
	if c.Count != nil && (*c.Count < 1 || *c.Count > PingMaxCount) {
		return fmt.Errorf("count must be between 1 and %d", PingMaxCount)
	}
	if c.IntervalMs != nil && (*c.IntervalMs < PingMinIntervalMs || *c.IntervalMs > PingMaxIntervalMs) {
		return fmt.Errorf("interval_ms must be between %d and %d", PingMinIntervalMs, PingMaxIntervalMs)
	}
	if c.MaxPacketLossPct != nil {
		v := *c.MaxPacketLossPct
		// 100 would be unreachable: losing every echo always fails.
		if math.IsNaN(v) || v < 0 || v >= 100 {
			return fmt.Errorf("max_packet_loss_pct must be 0 or more and below 100")
		}
	}
	if c.MaxRTTMs != nil && *c.MaxRTTMs <= 0 {
		return fmt.Errorf("max_rtt_ms must be greater than 0")
	}
	return nil
}

// PingSchedule is how a check with this config spaces its echoes inside
// budget (the monitor's timeout, less what resolving the host used) and when
// the prober stops. The last echo leaves (count-1)*interval after the first
// and its reply gets PingReplyWaitMs, or half of budget when that is less;
// deadline is the sum, so a lost echo holds the worker that long, never the
// whole timeout. When the configured (or default) interval leaves less, the
// interval is shortened to fit: the run never outlasts budget, so a monitor
// saved with a 1 s timeout before echoes were counted keeps working instead
// of failing.
//
// ValidatePingSchedule refuses, at save time, an explicit interval_ms that
// would be shortened under the full timeout, and a schedule whose interval
// would drop below PingMinIntervalMs; the worker still applies whatever this
// returns.
func (c PingMonitorConfig) PingSchedule(budget time.Duration) (count int, interval, deadline time.Duration) {
	count = c.EffectiveCount()
	interval = PingDefaultIntervalMs * time.Millisecond
	if c.IntervalMs != nil {
		interval = time.Duration(*c.IntervalMs) * time.Millisecond
	}
	if budget <= 0 {
		return count, interval, 0
	}
	if count <= 1 {
		return count, interval, min(PingReplyWaitMs*time.Millisecond, budget)
	}
	wait := min(PingReplyWaitMs*time.Millisecond, budget/2)
	if fit := (budget - wait) / time.Duration(count-1); interval > fit {
		interval = fit.Truncate(time.Millisecond)
	}
	return count, interval, time.Duration(count-1)*interval + wait
}

// ValidatePingSchedule checks that a ping config's echoes fit the monitor's
// timeout without the worker having to compress them (PingSchedule): an
// explicit interval_ms must fit as configured, and a default one must not
// shrink below PingMinIntervalMs. A config that does not parse or validate
// is ValidateConfig's to report.
func ValidatePingSchedule(configRaw json.RawMessage, timeoutSeconds int) error {
	var c PingMonitorConfig
	if err := json.Unmarshal(configRaw, &c); err != nil || c.Validate() != nil || timeoutSeconds <= 0 {
		return nil
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	count, interval, _ := c.PingSchedule(timeout)
	if count <= 1 {
		return nil
	}
	if c.IntervalMs != nil {
		want := time.Duration(*c.IntervalMs) * time.Millisecond
		if interval < want {
			return fmt.Errorf("timeout_seconds must be at least %d for %d echoes %d ms apart (the last reply needs up to %d ms)",
				minPingTimeoutSeconds(c), count, *c.IntervalMs, PingReplyWaitMs)
		}
		return nil
	}
	if interval < PingMinIntervalMs*time.Millisecond {
		return fmt.Errorf("timeout_seconds must be at least %d for %d echoes (or lower count)", minPingTimeoutSeconds(c), count)
	}
	return nil
}

// minPingTimeoutSeconds is the smallest whole timeout ValidatePingSchedule
// accepts for c.
func minPingTimeoutSeconds(c PingMonitorConfig) int {
	floor := time.Duration(PingMinIntervalMs) * time.Millisecond
	if c.IntervalMs != nil {
		floor = time.Duration(*c.IntervalMs) * time.Millisecond
	}
	for s := 1; s < 3600; s++ {
		if _, interval, _ := c.PingSchedule(time.Duration(s) * time.Second); interval >= floor {
			return s
		}
	}
	return 3600
}
