package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// DNSChecker implements Checker for DNS monitors
type DNSChecker struct{}

// NewDNSChecker creates a new DNS checker
func NewDNSChecker() *DNSChecker {
	return &DNSChecker{}
}

type dnsMetricsEnvelope struct {
	DNS *dnsMetrics `json:"dns,omitempty"`
}

type dnsMetrics struct {
	RecordType string   `json:"record_type,omitempty"`
	Answers    []string `json:"answers,omitempty"`
}

// Check performs a DNS lookup check
func (c *DNSChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.DNSMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal dns config: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		errMsg := "host is required"
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	recordType := strings.ToUpper(strings.TrimSpace(config.RecordType))
	if recordType == "" {
		recordType = "A"
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()
	answers, err := c.lookup(ctx, recordType, host)
	latencyMs := time.Since(startTime).Milliseconds()

	if err != nil {
		errMsg := fmt.Sprintf("dns lookup failed: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	if len(answers) == 0 {
		errMsg := "no dns answers returned"
		return CheckResult{
			Status:       "failure",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	expected := normalizeExpectedAnswers(config.ExpectedAnswers)
	if len(expected) > 0 {
		actual := normalizeExpectedAnswers(answers)
		missing := make([]string, 0)
		for _, exp := range expected {
			found := false
			for _, ans := range actual {
				if ans == exp {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, exp)
			}
		}

		if len(missing) > 0 {
			errMsg := fmt.Sprintf("missing expected answers: %s", strings.Join(missing, ", "))
			return CheckResult{
				Status:       "failure",
				ErrorMessage: &errMsg,
				LatencyMs:    &latencyMs,
			}
		}
	}

	metricsJSON, _ := json.Marshal(dnsMetricsEnvelope{DNS: &dnsMetrics{
		RecordType: recordType,
		Answers:    answers,
	}})

	return CheckResult{
		Status:      "success",
		LatencyMs:   &latencyMs,
		MetricsData: metricsJSON,
	}
}

func (c *DNSChecker) lookup(ctx context.Context, recordType, host string) ([]string, error) {
	resolver := net.DefaultResolver

	switch recordType {
	case "A":
		ips, err := resolver.LookupIP(ctx, "ip4", host)
		if err != nil {
			return nil, err
		}
		return ipsToStrings(ips), nil
	case "AAAA":
		ips, err := resolver.LookupIP(ctx, "ip6", host)
		if err != nil {
			return nil, err
		}
		return ipsToStrings(ips), nil
	case "CNAME":
		cname, err := resolver.LookupCNAME(ctx, host)
		if err != nil {
			return nil, err
		}
		return []string{cname}, nil
	case "TXT":
		txts, err := resolver.LookupTXT(ctx, host)
		if err != nil {
			return nil, err
		}
		return txts, nil
	case "MX":
		mxs, err := resolver.LookupMX(ctx, host)
		if err != nil {
			return nil, err
		}
		answers := make([]string, 0, len(mxs))
		for _, mx := range mxs {
			answers = append(answers, mx.Host)
		}
		return answers, nil
	case "NS":
		nss, err := resolver.LookupNS(ctx, host)
		if err != nil {
			return nil, err
		}
		answers := make([]string, 0, len(nss))
		for _, ns := range nss {
			answers = append(answers, ns.Host)
		}
		return answers, nil
	default:
		return nil, fmt.Errorf("unsupported record type: %s", recordType)
	}
}

func ipsToStrings(ips []net.IP) []string {
	answers := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		answers = append(answers, ip.String())
	}
	return answers
}

func normalizeExpectedAnswers(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		n := normalizeDNSAnswer(v)
		if n != "" {
			normalized = append(normalized, n)
		}
	}
	return normalized
}

func normalizeDNSAnswer(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String()
	}
	trimmed = strings.TrimSuffix(trimmed, ".")
	return strings.ToLower(trimmed)
}
