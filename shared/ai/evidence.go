package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// MonitorEvidence is the entire allowlist sent for an RCA decision. Raw config,
// text errors, addresses, response bodies and display values never cross it.
type MonitorEvidence struct {
	Checks               int  `json:"checks"`
	Failures             int  `json:"failures"`
	DNSFailures          int  `json:"dns_failures"`
	ConnectionFailures   int  `json:"connection_failures"`
	TLSFailures          int  `json:"tls_failures"`
	ProtocolFailures     int  `json:"protocol_failures"`
	SlowChecks           int  `json:"slow_checks"`
	BaselineMS           int  `json:"baseline_ms"`
	MaxLatencyMS         int  `json:"max_latency_ms"`
	MaxDNSMS             int  `json:"max_dns_ms"`
	MaxConnectMS         int  `json:"max_connect_ms"`
	MaxTLSMS             int  `json:"max_tls_ms"`
	MaxTTFBMS            int  `json:"max_ttfb_ms"`
	CertificateExpired   bool `json:"certificate_expired"`
	CertificateChanged   bool `json:"certificate_changed"`
	ResourcePressure     bool `json:"resource_pressure"`
	HostMetricsAvailable bool `json:"host_metrics_available"`
	UpstreamFailedBefore bool `json:"upstream_failed_before"`
	MaintenanceOverlap   bool `json:"maintenance_overlap"`
	Conflict             bool `json:"conflict"`
	MissingChecks        bool `json:"missing_checks"`
}

// ErrorCategory maps bounded checker text to a closed set of symptoms. The
// original text can contain URLs or credentials and is never sent or rendered.
func ErrorCategory(text string) string {
	if len(text) > 4096 {
		text = text[:4096]
	}
	s := strings.ToLower(text)
	switch {
	case strings.Contains(s, "no such host") || strings.Contains(s, "dns lookup") || strings.Contains(s, "dns resolution") || strings.Contains(s, "dns error"):
		return "dns"
	case strings.Contains(s, "certificate") || strings.Contains(s, "tls handshake") || strings.Contains(s, "x509:"):
		return "tls"
	case strings.Contains(s, "connection refused") || strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host") || strings.Contains(s, "connection reset"):
		return "connection"
	case strings.Contains(s, "unexpected status") || strings.Contains(s, "status code") || strings.Contains(s, "status_code:") || strings.Contains(s, "assertion") || strings.Contains(s, "expected status"):
		return "protocol"
	default:
		return "unknown"
	}
}

// SummarizeChecks reduces all rows in the bounded window, before sampling.
func SummarizeChecks(checks []CheckContext) MonitorEvidence {
	e := MonitorEvidence{Checks: len(checks), MissingChecks: len(checks) == 0}
	var baseline []int
	serial := ""
	for _, c := range checks {
		if c.Status == "success" && c.LatencyMS != nil {
			baseline = append(baseline, *c.LatencyMS)
		}
	}
	if len(baseline) > 0 {
		sort.Ints(baseline)
		e.BaselineMS = baseline[len(baseline)/2]
	}
	for _, c := range checks {
		cat := c.ErrorCategory
		if cat == "" {
			cat = ErrorCategory(c.ErrorMessage)
		}
		failure := c.Status == "failure" || (c.Status == "error" && cat != "unknown")
		if failure {
			e.Failures++
		}
		if failure {
			switch cat {
			case "dns":
				e.DNSFailures++
			case "connection":
				e.ConnectionFailures++
			case "tls":
				e.TLSFailures++
			case "protocol":
				e.ProtocolFailures++
			}
		}
		if c.LatencyMS != nil {
			e.MaxLatencyMS = max(e.MaxLatencyMS, *c.LatencyMS)
			if e.BaselineMS > 0 && *c.LatencyMS >= 3*e.BaselineMS && *c.LatencyMS >= 500 {
				e.SlowChecks++
			}
		}
		// Decode only known nested numeric timings and certificate details. Never
		// retain arbitrary strings from metrics_data.
		var m struct {
			HTTP struct {
				Timing struct {
					DNSMS     int `json:"dns_ms"`
					ConnectMS int `json:"connect_ms"`
					TLSMS     int `json:"tls_handshake_ms"`
					TTFBMS    int `json:"ttfb_ms"`
				} `json:"timing"`
				TLS struct {
					NotAfter string `json:"not_after"`
					Serial   string `json:"serial_number"`
				} `json:"tls"`
			} `json:"http"`
		}
		if len(c.Metrics) <= 64*1024 && json.Unmarshal(c.Metrics, &m) == nil {
			e.MaxDNSMS = max(e.MaxDNSMS, m.HTTP.Timing.DNSMS)
			e.MaxConnectMS = max(e.MaxConnectMS, m.HTTP.Timing.ConnectMS)
			e.MaxTLSMS = max(e.MaxTLSMS, m.HTTP.Timing.TLSMS)
			e.MaxTTFBMS = max(e.MaxTTFBMS, m.HTTP.Timing.TTFBMS)
			if end, err := time.Parse(time.RFC3339, m.HTTP.TLS.NotAfter); err == nil && !end.After(c.CheckedAt) {
				e.CertificateExpired = true
			}
			if m.HTTP.TLS.Serial != "" {
				if serial != "" && serial != m.HTTP.TLS.Serial {
					e.CertificateChanged = true
				}
				serial = m.HTTP.TLS.Serial
			}
		}
	}
	// Distinct error domains in one compact decision are ambiguous. Larger
	// incidents are decomposed by monitor before this flag is consulted.
	domains := 0
	for _, n := range []int{e.DNSFailures, e.ConnectionFailures, e.TLSFailures, e.ProtocolFailures} {
		if n > 0 {
			domains++
		}
	}
	e.Conflict = domains > 1
	return e
}

func (e MonitorEvidence) Supports(h string) bool {
	if e.MissingChecks || e.Conflict {
		return h == "insufficient_evidence"
	}
	switch h {
	case "dns_failure":
		return e.DNSFailures > 0
	case "connection_failure":
		return e.ConnectionFailures > 0
	case "tls_problem":
		return e.TLSFailures > 0 || (e.CertificateExpired && e.Failures > 0)
	case "protocol_error":
		return e.ProtocolFailures > 0
	case "latency_degradation":
		return e.SlowChecks > 0 && e.BaselineMS > 0
	case "resource_pressure":
		return e.ResourcePressure && e.HostMetricsAvailable && e.Failures > 0
	case "upstream_failure":
		return e.UpstreamFailedBefore && e.Failures > 0
	case "maintenance_downtime":
		return e.MaintenanceOverlap && e.Failures > 0
	case "insufficient_evidence":
		return true
	}
	return false
}

func EvidenceFacts(ref string, e MonitorEvidence) []EvidenceReference {
	out := []EvidenceReference{{Ref: ref + "/checks", MonitorRef: ref, Fact: fmt.Sprintf("%d of %d checks failed in the analysis window.", e.Failures, e.Checks)}}
	add := func(k, f string) { out = append(out, EvidenceReference{Ref: ref + "/" + k, MonitorRef: ref, Fact: f}) }
	for _, v := range []struct {
		k string
		n int
	}{{"dns", e.DNSFailures}, {"connection", e.ConnectionFailures}, {"tls", e.TLSFailures}, {"protocol", e.ProtocolFailures}} {
		if v.n > 0 {
			add(v.k, fmt.Sprintf("%d checks recorded %s failure symptoms.", v.n, v.k))
		}
	}
	if e.SlowChecks > 0 {
		add("latency", fmt.Sprintf("%d checks exceeded three times the %d ms healthy median; maximum %d ms.", e.SlowChecks, e.BaselineMS, e.MaxLatencyMS))
	}
	if e.CertificateExpired {
		add("certificate", "A recorded certificate expiry precedes a check in this window.")
	}
	if e.CertificateChanged {
		add("certificate-change", "The recorded certificate serial changed within this window.")
	}
	if e.ResourcePressure && e.HostMetricsAvailable {
		add("resource", "Measured host CPU or memory utilization exceeded 90% during failures.")
	}
	if e.UpstreamFailedBefore {
		add("upstream", "A configured upstream or linked dependency root failed before this monitor's first failure.")
	}
	if e.MaintenanceOverlap {
		add("maintenance", "A configured maintenance window overlaps measured failure time.")
	}
	if e.MaxDNSMS+e.MaxConnectMS+e.MaxTLSMS+e.MaxTTFBMS > 0 {
		add("timings", fmt.Sprintf("Maximum DNS/connect/TLS/first-byte timings: %d/%d/%d/%d ms.", e.MaxDNSMS, e.MaxConnectMS, e.MaxTLSMS, e.MaxTTFBMS))
	}
	return out
}

// SampleTransitions keeps the first/last observations and status changes,
// then uniformly samples the remaining timeline; summary always covers all rows.
func SampleTransitions(checks []CheckContext, limit int) ([]CheckContext, bool) {
	if len(checks) <= limit {
		out := make([]CheckContext, len(checks))
		for i, c := range checks {
			c.ErrorCategory = ErrorCategory(c.ErrorMessage)
			c.ErrorMessage = ""
			c.Metrics = nil
			out[i] = c
		}
		return out, false
	}
	if limit < 2 {
		return nil, true
	}
	indices := []int{0, len(checks) - 1}
	seen := map[int]bool{0: true, len(checks) - 1: true}
	for i := 1; i < len(checks) && len(indices) < limit; i++ {
		if checks[i].Status != checks[i-1].Status {
			indices = append(indices, i)
			seen[i] = true
		}
	}
	for i := 0; i < limit && len(indices) < limit; i++ {
		j := int(math.Round(float64(i) * float64(len(checks)-1) / float64(limit-1)))
		if !seen[j] {
			indices = append(indices, j)
			seen[j] = true
		}
	}
	sort.Ints(indices)
	out := make([]CheckContext, 0, len(indices))
	for _, i := range indices {
		c := checks[i]
		c.ErrorCategory = ErrorCategory(c.ErrorMessage)
		c.ErrorMessage = ""
		c.Metrics = nil
		out = append(out, c)
	}
	return out, true
}
