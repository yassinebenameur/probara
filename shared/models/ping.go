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
// through PingMonitorConfig.Validate.
const (
	PingDefaultCount      = 1    // absent count: one echo, the original behaviour
	PingMaxCount          = 10   // echoes per check
	PingDefaultIntervalMs = 1000 // gap between echoes
	PingMinIntervalMs     = 200  // iputils' floor for unprivileged senders
	PingMaxIntervalMs     = 10000
	PingMinPacketSize     = 24   // the prober's timestamp + tracker payload
	PingMaxPacketSize     = 1472 // largest IPv4 echo payload in a 1500-byte MTU
	// PingReplyWaitMs is the time a schedule must leave after the last echo
	// is sent for its reply to arrive (ValidatePingSchedule).
	PingReplyWaitMs = 1000
)

// PingMonitorConfig represents configuration for ping monitors. Every field
// but host is optional and absent means today's single-echo check that fails
// only when nothing answers; the operator UI sends count 3 for new monitors.
type PingMonitorConfig struct {
	Host string `json:"host"`
	// Count is the number of echo requests per check (1-10, absent = 1).
	Count *int `json:"count,omitempty"`
	// IntervalMs is the gap between echo requests (200-10000, absent = 1000).
	IntervalMs *int `json:"interval_ms,omitempty"`
	// PacketSize is the echo payload in bytes (24-1472, absent = 24).
	PacketSize *int `json:"packet_size,omitempty"`
	// MaxPacketLossPct fails the check when more than this share of echoes
	// went unanswered (0-99.99). Absent: only 100% loss fails.
	MaxPacketLossPct *float64 `json:"max_packet_loss_pct,omitempty"`
	// MaxRTTMs fails the check when the average round trip exceeds it.
	MaxRTTMs *int64 `json:"max_rtt_ms,omitempty"`
	// WarnRTTMs flags (never fails) a check whose average round trip
	// exceeds it: recorded as ping.rtt_warn_ms in metrics_data.
	WarnRTTMs *int64 `json:"warn_rtt_ms,omitempty"`
}

// EffectiveCount is the number of echoes a check sends.
func (c PingMonitorConfig) EffectiveCount() int {
	if c.Count == nil {
		return PingDefaultCount
	}
	return *c.Count
}

// EffectiveInterval is the gap between echoes.
func (c PingMonitorConfig) EffectiveInterval() time.Duration {
	if c.IntervalMs == nil {
		return PingDefaultIntervalMs * time.Millisecond
	}
	return time.Duration(*c.IntervalMs) * time.Millisecond
}

// EffectivePacketSize is the echo payload size in bytes.
func (c PingMonitorConfig) EffectivePacketSize() int {
	if c.PacketSize == nil {
		return PingMinPacketSize
	}
	return *c.PacketSize
}

// Validate checks a ping config. The worker runs it on every job too, so a
// rule added here must hold for configs saved before it existed.
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
	if c.PacketSize != nil && (*c.PacketSize < PingMinPacketSize || *c.PacketSize > PingMaxPacketSize) {
		return fmt.Errorf("packet_size must be between %d and %d bytes", PingMinPacketSize, PingMaxPacketSize)
	}
	if c.MaxPacketLossPct != nil {
		v := *c.MaxPacketLossPct
		// 100 would make the threshold unreachable: total loss always fails.
		if math.IsNaN(v) || v < 0 || v >= 100 {
			return fmt.Errorf("max_packet_loss_pct must be 0 or more and below 100")
		}
	}
	if c.MaxRTTMs != nil && *c.MaxRTTMs <= 0 {
		return fmt.Errorf("max_rtt_ms must be greater than 0")
	}
	if c.WarnRTTMs != nil && *c.WarnRTTMs <= 0 {
		return fmt.Errorf("warn_rtt_ms must be greater than 0")
	}
	if c.MaxRTTMs != nil && c.WarnRTTMs != nil && *c.WarnRTTMs >= *c.MaxRTTMs {
		return fmt.Errorf("warn_rtt_ms must be below max_rtt_ms")
	}
	return nil
}

// ValidatePingSchedule checks that a check's echoes fit its timeout: the
// last echo leaves (count-1)*interval after the first, and its reply needs
// PingReplyWaitMs on top. A config that does not parse is ValidateConfig's
// to report.
func ValidatePingSchedule(configRaw json.RawMessage, _ /* intervalSeconds */, timeoutSeconds int) error {
	var c PingMonitorConfig
	if err := json.Unmarshal(configRaw, &c); err != nil {
		return nil
	}
	count := c.EffectiveCount()
	if count < 1 || count > PingMaxCount {
		return nil // ValidateConfig reports it
	}
	needMs := int64(count-1)*c.EffectiveInterval().Milliseconds() + PingReplyWaitMs
	if timeoutSeconds > 0 && needMs > int64(timeoutSeconds)*1000 {
		return fmt.Errorf("timeout_seconds must be at least %ds for %d echoes %dms apart (the last reply needs %dms)",
			(needMs+999)/1000, count, c.EffectiveInterval().Milliseconds(), PingReplyWaitMs)
	}
	return nil
}
