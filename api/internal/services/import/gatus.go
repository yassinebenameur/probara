package importservice

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Gatus migration support.
//
// Gatus is configured by one YAML file (or a directory of them, which
// Gatus merges; merge their lists into one file to upload). Its `endpoints` become
// monitors by URL scheme, their `conditions` become assertions (gatus
// conditions translation lives in gatus_conditions.go), `group` becomes a
// group, and `external-endpoints` with a heartbeat become push monitors.
// Alerting providers are not migrated; an endpoint's alert failure and
// success thresholds become the monitor's down and recovery thresholds.

const (
	// Gatus's own defaults when an endpoint leaves them out.
	gatusDefaultInterval         = 60
	gatusDefaultTimeout          = 10
	gatusDefaultFailureThreshold = 3
	gatusDefaultSuccessThreshold = 2

	gatusAlertingNote    = "Gatus alerting providers and endpoint alerts were not migrated - attach Probara alert channels after import (alert failure and success thresholds were kept as each monitor's down and recovery thresholds)"
	gatusMaintenanceNote = "Gatus maintenance windows were not migrated - recreate them as Probara maintenance windows"
)

// gatusSkipSchemes explains, per URL scheme, why there is nothing faithful
// to import.
var gatusSkipSchemes = map[string]string{
	"udp":      "UDP checks are not implemented",
	"sctp":     "SCTP checks are not implemented",
	"starttls": "a STARTTLS check would degrade to a bare TCP connect",
	"ssh":      "SSH checks are not implemented",
}

var gatusDNSRecordTypes = kumaDNSRecordTypes

// gatusEnvPlaceholder matches what Gatus expands from the environment when
// it loads the file ($VAR and ${VAR}); the importer never resolves them.
var gatusEnvPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

type gatusConfig struct {
	Endpoints         []gatusEndpoint         `yaml:"endpoints"`
	ExternalEndpoints []gatusExternalEndpoint `yaml:"external-endpoints"`
	// Providers are read one by one (gatusAlertDefaults) so an unexpected
	// provider shape cannot make the file unreadable.
	Alerting    map[string]yaml.Node `yaml:"alerting"`
	Maintenance interface{}          `yaml:"maintenance"`
}

type gatusAlert struct {
	Type             string `yaml:"type"`
	Enabled          *bool  `yaml:"enabled"`
	FailureThreshold int    `yaml:"failure-threshold"`
	SuccessThreshold int    `yaml:"success-threshold"`
}

type gatusClient struct {
	Insecure           bool                   `yaml:"insecure"`
	IgnoreRedirect     bool                   `yaml:"ignore-redirect"`
	Timeout            string                 `yaml:"timeout"`
	DNSResolver        string                 `yaml:"dns-resolver"`
	OAuth2             interface{}            `yaml:"oauth2"`
	IdentityAwareProxy interface{}            `yaml:"identity-aware-proxy"`
	TLS                map[string]interface{} `yaml:"tls"`
	Network            string                 `yaml:"network"`
	ProxyURL           string                 `yaml:"proxy-url"`
}

type gatusEndpoint struct {
	Enabled    *bool             `yaml:"enabled"`
	Name       string            `yaml:"name"`
	Group      string            `yaml:"group"`
	URL        string            `yaml:"url"`
	Method     string            `yaml:"method"`
	Body       string            `yaml:"body"`
	GraphQL    bool              `yaml:"graphql"`
	Headers    map[string]string `yaml:"headers"`
	Interval   string            `yaml:"interval"`
	Conditions []string          `yaml:"conditions"`
	Alerts     []gatusAlert      `yaml:"alerts"`
	DNS        *struct {
		QueryType string `yaml:"query-type"`
		QueryName string `yaml:"query-name"`
	} `yaml:"dns"`
	Client             *gatusClient `yaml:"client"`
	MaintenanceWindows interface{}  `yaml:"maintenance-windows"`
}

type gatusExternalEndpoint struct {
	Enabled   *bool        `yaml:"enabled"`
	Name      string       `yaml:"name"`
	Group     string       `yaml:"group"`
	Alerts    []gatusAlert `yaml:"alerts"`
	Heartbeat struct {
		Interval string `yaml:"interval"`
	} `yaml:"heartbeat"`
}

// parseGatusConfig recognizes a Gatus configuration file and translates it.
// The bool reports whether the payload was Gatus's at all, so a non-match
// falls through to the generic YAML shapes (which also read `endpoints`).
func (s *Service) parseGatusConfig(data []byte) (*parsedSource, bool, error) {
	var probe map[string]interface{}
	if err := yaml.Unmarshal(data, &probe); err != nil || !isGatusConfig(probe) {
		return nil, false, nil
	}

	var config gatusConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, true, fmt.Errorf("invalid Gatus configuration: %w", err)
	}
	return translateGatus(config), true, nil
}

// isGatusConfig: Gatus refuses an endpoint without conditions and an
// external endpoint without a token, so requiring them on every entry tells
// a Gatus file from a generic YAML list under an `endpoints` key.
func isGatusConfig(doc map[string]interface{}) bool {
	entries := 0
	if raw, ok := doc["endpoints"]; ok {
		list, ok := raw.([]interface{})
		if !ok {
			return false
		}
		for _, item := range list {
			endpoint, ok := item.(map[string]interface{})
			if !ok {
				return false
			}
			conditions, ok := endpoint["conditions"].([]interface{})
			if _, named := endpoint["name"]; !named || !ok || len(conditions) == 0 {
				return false
			}
			entries++
		}
	}
	if raw, ok := doc["external-endpoints"]; ok {
		list, ok := raw.([]interface{})
		if !ok {
			return false
		}
		for _, item := range list {
			endpoint, ok := item.(map[string]interface{})
			if !ok {
				return false
			}
			_, named := endpoint["name"]
			_, token := endpoint["token"]
			if !named || !token {
				return false
			}
			entries++
		}
	}
	return entries > 0
}

func translateGatus(config gatusConfig) *parsedSource {
	t := newForeignTranslation("Gatus", models.ImportSchemaGatusConfig)
	defaults := gatusAlertDefaults(config.Alerting)

	if len(config.Alerting) > 0 {
		t.note(gatusAlertingNote)
	}
	if config.Maintenance != nil {
		t.note(gatusMaintenanceNote)
	}

	for i, endpoint := range config.Endpoints {
		translateGatusEndpoint(t, i+1, endpoint, defaults)
	}
	for i, external := range config.ExternalEndpoints {
		translateGatusExternal(t, i+1, external, defaults)
	}
	return t.finish()
}

// gatusAlertDefaults reads each provider's default-alert, which fills in an
// endpoint alert of that type that leaves a threshold out.
func gatusAlertDefaults(alerting map[string]yaml.Node) map[string]gatusAlert {
	defaults := make(map[string]gatusAlert, len(alerting))
	for provider, node := range alerting {
		var settings struct {
			DefaultAlert *gatusAlert `yaml:"default-alert"`
		}
		if err := node.Decode(&settings); err == nil && settings.DefaultAlert != nil {
			defaults[provider] = *settings.DefaultAlert
		}
	}
	return defaults
}

// gatusThresholds is the earliest any enabled alert would fire and resolve:
// Gatus pages after failure-threshold consecutive failures and resolves
// after success-threshold successes, per alert.
func gatusThresholds(m *foreignMonitor, alerts []gatusAlert, defaults map[string]gatusAlert) {
	failure, success := 0, 0
	differ := false
	for _, alert := range alerts {
		def := defaults[alert.Type]
		enabled := alert.Enabled
		if enabled == nil {
			enabled = def.Enabled
		}
		if enabled != nil && !*enabled {
			continue
		}
		f := firstPositive(alert.FailureThreshold, def.FailureThreshold, gatusDefaultFailureThreshold)
		s := firstPositive(alert.SuccessThreshold, def.SuccessThreshold, gatusDefaultSuccessThreshold)
		if failure != 0 && (f != failure || s != success) {
			differ = true
		}
		if failure == 0 || f < failure {
			failure = f
		}
		if success == 0 || s < success {
			success = s
		}
	}
	if failure == 0 {
		return
	}
	if m.setFailureThreshold(failure) {
		m.warn("the alert failure threshold of %d exceeds Probara's maximum and was lowered to %d", failure, m.failureThreshold)
	}
	if m.setRecoveryThreshold(success) {
		m.warn("the alert success threshold of %d exceeds Probara's maximum and was lowered to %d", success, m.recoveryThreshold)
	}
	if differ {
		m.warn("its alerts had different failure or success thresholds; the lowest of each was kept")
	}
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// parseGatusDuration reads a Go duration ("30s", "5m", "1h30m") and the
// day suffix Gatus users write for expirations ("30d").
func parseGatusDuration(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
		if err != nil || days < 0 {
			return 0, false
		}
		return time.Duration(days * float64(24*time.Hour)), true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

func ceilSeconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

// gatusPlaceholders lists the environment variables a value refers to.
func gatusPlaceholders(value string) []string {
	matches := gatusEnvPlaceholder.FindAllStringSubmatch(value, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		names = append(names, "$"+name)
	}
	return names
}

func gatusEndpointName(index int, endpoint gatusEndpoint) string {
	if name := strings.TrimSpace(endpoint.Name); name != "" {
		return name
	}
	return fmt.Sprintf("Gatus endpoint %d", index)
}

// gatusScheme is the endpoint's check kind: the URL scheme, or "dns" for an
// endpoint with a dns block (whose url is the DNS server).
func gatusScheme(endpoint gatusEndpoint) string {
	if endpoint.DNS != nil {
		return "dns"
	}
	raw := strings.TrimSpace(endpoint.URL)
	if idx := strings.Index(raw, "://"); idx > 0 {
		return strings.ToLower(raw[:idx])
	}
	return ""
}

func translateGatusEndpoint(t *foreignTranslation, index int, endpoint gatusEndpoint, defaults map[string]gatusAlert) {
	name := gatusEndpointName(index, endpoint)
	scheme := gatusScheme(endpoint)

	if reason, ok := gatusSkipSchemes[scheme]; ok {
		t.skip(name, scheme, reason)
		return
	}

	m := &foreignMonitor{
		sourceID:   strconv.Itoa(index),
		sourceType: scheme,
		name:       name,
		group:      strings.TrimSpace(endpoint.Group),
		enabled:    endpoint.Enabled == nil || *endpoint.Enabled,
		interval:   gatusDefaultInterval,
		timeout:    gatusDefaultTimeout,
	}
	if raw := strings.TrimSpace(endpoint.Interval); raw != "" {
		if d, ok := parseGatusDuration(raw); ok && d > 0 {
			m.setInterval(ceilSeconds(d))
		} else {
			m.warn("the interval %q could not be read; the Gatus default of %ds was used", raw, gatusDefaultInterval)
		}
	}

	// Placeholders anywhere but the host are kept verbatim and the monitor
	// is created disabled until they are replaced; a host that is itself a
	// placeholder has nothing to check.
	placeholders := make(map[string][]string)
	collect := func(where, value string) {
		if names := gatusPlaceholders(value); len(names) > 0 {
			placeholders[where] = append(placeholders[where], names...)
		}
	}
	collect("URL", endpoint.URL)
	collect("body", endpoint.Body)
	for _, header := range sortedKeys(endpoint.Headers) {
		collect("header "+header, endpoint.Headers[header])
	}
	if endpoint.DNS != nil {
		collect("DNS query", endpoint.DNS.QueryName)
	}
	if len(placeholders["URL"]) > 0 {
		masked := gatusEnvPlaceholder.ReplaceAllString(endpoint.URL, "gatusplaceholder")
		if host := urlHostname(masked); host == "" || strings.Contains(host, "gatusplaceholder") || (scheme == "dns" && strings.Contains(masked, "gatusplaceholder")) {
			t.skip(name, scheme, fmt.Sprintf("the target host comes from the environment (%s), which only Gatus resolves", strings.Join(placeholders["URL"], ", ")))
			return
		}
	}

	check := &gatusCheck{monitor: m}
	var reason string
	switch scheme {
	case "http", "https":
		check.kind = gatusKindHTTP
		reason = gatusHTTPConfig(m, endpoint)
	case "icmp":
		check.kind = gatusKindPing
		reason = gatusHostConfig(m, endpoint.URL, models.MonitorTypePing, false)
	case "tcp":
		check.kind = gatusKindTCP
		reason = gatusHostConfig(m, endpoint.URL, models.MonitorTypeTCP, true)
	case "tls":
		check.kind = gatusKindTLS
		reason = gatusHostConfig(m, endpoint.URL, models.MonitorTypeTCP, true)
		if reason == "" {
			m.config["use_tls"] = true
			if endpoint.Client != nil && endpoint.Client.Insecure {
				m.config["tls_skip_verify"] = true
			}
		}
	case "ws", "wss":
		check.kind = gatusKindWebSocket
		reason = gatusWebSocketConfig(m, endpoint)
	case "grpc", "grpcs":
		check.kind = gatusKindGRPC
		reason = gatusGRPCConfig(m, endpoint.URL, scheme == "grpcs")
	case "dns":
		check.kind = gatusKindDNS
		reason = gatusDNSConfig(m, endpoint)
	case "":
		reason = "the endpoint's URL has no scheme, so its check type is unknown"
	default:
		reason = fmt.Sprintf("the %q endpoint type has no equivalent", scheme)
	}
	if reason != "" {
		t.skip(name, scheme, reason)
		return
	}

	if (check.kind == gatusKindTCP || check.kind == gatusKindTLS) && strings.TrimSpace(endpoint.Body) != "" {
		m.warn("the body Gatus sent after connecting was dropped - Probara's TCP monitor only connects")
	}
	gatusClientWarnings(m, endpoint.Client, check.kind)

	for _, condition := range endpoint.Conditions {
		if skip := check.translate(condition); skip != "" {
			t.skip(name, scheme, skip)
			return
		}
	}
	if skip := check.apply(); skip != "" {
		t.skip(name, scheme, skip)
		return
	}

	if len(placeholders) > 0 {
		parts := make([]string, 0, len(placeholders))
		for _, where := range []string{"URL", "body", "DNS query"} {
			if names := placeholders[where]; len(names) > 0 {
				parts = append(parts, fmt.Sprintf("%s in the %s", strings.Join(names, ", "), where))
			}
		}
		for _, header := range sortedKeys(endpoint.Headers) {
			if names := placeholders["header "+header]; len(names) > 0 {
				parts = append(parts, fmt.Sprintf("%s in the %s header", strings.Join(names, ", "), header))
			}
		}
		m.disable(fmt.Sprintf("it uses environment variables that only Gatus resolves (%s); they were kept verbatim, so the monitor was created disabled - replace them with real values and enable it",
			strings.Join(parts, "; ")))
	}

	if len(endpoint.Alerts) > 0 {
		t.note(gatusAlertingNote)
		gatusThresholds(m, endpoint.Alerts, defaults)
	}
	if endpoint.MaintenanceWindows != nil {
		t.note(gatusMaintenanceNote)
	}

	t.add(m)

	if check.domainDays > 0 {
		host := urlHostname(endpoint.URL)
		if check.kind == gatusKindDNS {
			host, _ = m.config["host"].(string)
		}
		t.addDomainExpiry(m, host, check.domainDays,
			"Gatus failed the check inside the window, while Probara raises a domain expiry alert and the check keeps passing")
	}
}

// gatusClientWarnings reports client settings with no equivalent. Missing
// authentication would fail every check, so it leaves the monitor disabled.
func gatusClientWarnings(m *foreignMonitor, client *gatusClient, kind gatusKind) {
	if client == nil {
		return
	}
	if raw := strings.TrimSpace(client.Timeout); raw != "" {
		if d, ok := parseGatusDuration(raw); ok && d > 0 {
			m.timeout = ceilSeconds(d)
		} else {
			m.warn("the client timeout %q could not be read; the Gatus default of %ds was used", raw, gatusDefaultTimeout)
		}
	}
	if client.Insecure && kind != gatusKindHTTP && kind != gatusKindWebSocket && kind != gatusKindTLS {
		m.warn("the client's insecure setting was dropped - this check type does not verify certificates")
	}

	auth := make([]string, 0, 3)
	if client.OAuth2 != nil {
		auth = append(auth, "OAuth2 client credentials")
	}
	if client.IdentityAwareProxy != nil {
		auth = append(auth, "Identity-Aware Proxy")
	}
	if _, ok := client.TLS["certificate-file"]; ok {
		auth = append(auth, "client certificate")
	}
	if len(auth) > 0 {
		m.disable(fmt.Sprintf("the endpoint's authentication (%s) has no equivalent, so this monitor was created disabled - set its credentials (headers or basic auth) and enable it",
			strings.Join(auth, ", ")))
	}

	dropped := make([]string, 0, 3)
	if strings.TrimSpace(client.DNSResolver) != "" {
		dropped = append(dropped, "custom DNS resolver")
	}
	if strings.TrimSpace(client.Network) != "" {
		dropped = append(dropped, "network ("+client.Network+")")
	}
	if strings.TrimSpace(client.ProxyURL) != "" {
		dropped = append(dropped, "proxy")
	}
	if len(dropped) > 0 {
		m.warn("client settings with no equivalent were dropped: %s", strings.Join(dropped, ", "))
	}
}

func gatusHTTPConfig(m *foreignMonitor, endpoint gatusEndpoint) string {
	target, user, password, reason := parseForeignHTTPURL(endpoint.URL)
	if reason != "" {
		return reason
	}
	m.target = models.MonitorTypeHTTP

	method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if method == "" {
		method = "GET"
	}
	headers := make(map[string]string, len(endpoint.Headers)+1)
	for name, value := range endpoint.Headers {
		headers[name] = value
	}
	body := endpoint.Body
	if endpoint.GraphQL {
		// Gatus posts a GraphQL body as {"query": body}.
		encoded, _ := json.Marshal(map[string]string{"query": body})
		body = string(encoded)
		method = "POST"
		if !hasHeader(headers, "Content-Type") {
			headers["Content-Type"] = "application/json"
		}
	}
	if !kumaHTTPMethods[method] {
		return fmt.Sprintf("HTTP method %q is not supported", method)
	}

	config := map[string]interface{}{"url": target, "method": method}
	if strings.TrimSpace(body) != "" {
		config["body"] = body
	}
	setForeignHTTPHeaders(config, headers)
	setForeignBasicAuth(m, config, user, password)

	if endpoint.Client != nil {
		if endpoint.Client.Insecure {
			if isHTTPS(target) {
				config["tls_skip_verify"] = true
			} else {
				m.warn("the client's insecure setting was dropped because the endpoint is a plain http:// URL")
			}
		}
		if endpoint.Client.IgnoreRedirect {
			config["follow_redirects"] = false
		}
	}
	m.config = config
	return ""
}

// gatusHostConfig builds a ping or tcp config from icmp://, tcp:// or
// tls:// URLs.
func gatusHostConfig(m *foreignMonitor, raw string, target models.MonitorType, needsPort bool) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "the endpoint's URL names no host"
	}
	m.target = target
	m.config = map[string]interface{}{"host": parsed.Hostname()}
	if !needsPort {
		return ""
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "TCP monitors require a port, and this endpoint has none"
	}
	m.config["port"] = port
	return ""
}

func gatusWebSocketConfig(m *foreignMonitor, endpoint gatusEndpoint) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint.URL))
	if err != nil || parsed.Host == "" {
		return "the endpoint's URL is not a valid WebSocket URL"
	}
	m.target = models.MonitorTypeWebSocket
	config := map[string]interface{}{"url": parsed.String()}
	// Every WebSocket header value is a secret field (encrypted at rest).
	if len(endpoint.Headers) > 0 {
		config["headers"] = endpoint.Headers
	}
	if body := endpoint.Body; strings.TrimSpace(body) != "" {
		config["send_message"] = body
	}
	if endpoint.Client != nil && endpoint.Client.Insecure && parsed.Scheme == "wss" {
		config["tls_skip_verify"] = true
	}
	m.config = config
	return ""
}

func gatusGRPCConfig(m *foreignMonitor, raw string, useTLS bool) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "the endpoint's URL names no host"
	}
	m.target = models.MonitorTypeGRPC
	config := map[string]interface{}{"host": parsed.Hostname()}
	if port, err := strconv.Atoi(parsed.Port()); err == nil && port > 0 {
		config["port"] = port
	}
	if useTLS {
		config["use_tls"] = true
	}
	m.config = config
	return ""
}

// gatusDNSConfig: a Gatus DNS endpoint's url is the DNS server and its dns
// block names the query.
func gatusDNSConfig(m *foreignMonitor, endpoint gatusEndpoint) string {
	recordType := strings.ToUpper(strings.TrimSpace(endpoint.DNS.QueryType))
	if recordType == "" {
		recordType = "A"
	}
	if !gatusDNSRecordTypes[recordType] {
		return fmt.Sprintf("DNS record type %s is not supported (Probara resolves A, AAAA, CNAME, TXT, MX and NS)", recordType)
	}
	query := strings.TrimSuffix(strings.TrimSpace(endpoint.DNS.QueryName), ".")
	if query == "" {
		return "the DNS endpoint names no query"
	}

	server := strings.TrimSpace(endpoint.URL)
	if idx := strings.Index(server, "://"); idx >= 0 {
		if strings.EqualFold(server[:idx], "tcp") {
			m.warn("the DNS server is queried over UDP; Gatus used TCP")
		}
		server = server[idx+3:]
	}
	server = strings.TrimSuffix(server, "/")
	if server == "" {
		return "the DNS endpoint names no DNS server"
	}
	if host, port, err := net.SplitHostPort(server); err == nil && port == "53" {
		server = host
	}

	m.target = models.MonitorTypeDNS
	m.config = map[string]interface{}{"host": query, "record_type": recordType, "nameserver": server}
	return ""
}

func translateGatusExternal(t *foreignTranslation, index int, external gatusExternalEndpoint, defaults map[string]gatusAlert) {
	name := strings.TrimSpace(external.Name)
	if name == "" {
		name = fmt.Sprintf("Gatus external endpoint %d", index)
	}
	d, ok := parseGatusDuration(external.Heartbeat.Interval)
	if !ok || d <= 0 {
		t.skip(name, "external-endpoint", "the external endpoint has no heartbeat interval, so there is no expected check-in to watch - Probara's push monitor needs one")
		return
	}

	m := &foreignMonitor{
		sourceID:   fmt.Sprintf("external %d", index),
		sourceType: "external-endpoint",
		name:       name,
		target:     models.MonitorTypePush,
		group:      strings.TrimSpace(external.Group),
		enabled:    external.Enabled == nil || *external.Enabled,
		interval:   gatusDefaultInterval,
	}
	m.setInterval(ceilSeconds(d))
	m.config = map[string]interface{}{
		"expected_interval_seconds": m.interval,
		"grace_period_seconds":      0,
	}
	m.warn("Probara issues its own push token, so results must be sent to this monitor's push URL instead of Gatus's external endpoint API")
	if len(external.Alerts) > 0 {
		t.note(gatusAlertingNote)
		gatusThresholds(m, external.Alerts, defaults)
	}
	t.add(m)
}
