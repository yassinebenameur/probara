package importservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Better Stack (formerly Better Uptime) migration support.
//
// Better Stack has no export button; its Uptime API v2 answers in JSON:API
// form, `{"data": [{"id", "type", "attributes"}], "pagination": {...}}`.
// This adapter accepts one response (GET /api/v2/monitors, /heartbeats,
// /monitor-groups, /heartbeat-groups), the same with the resource kinds
// merged into one `data` array, or a JSON array of such responses. Monitor
// and heartbeat groups resolve their members' group ids to names, so a file
// that carries them keeps the grouping.

// betterStackKinds are the JSON:API resource types this adapter reads; a
// document of only these is Better Stack's.
var betterStackKinds = map[string]bool{
	"monitor":         true,
	"heartbeat":       true,
	"monitor_group":   true,
	"heartbeat_group": true,
}

// betterStackHTTPTypes are the monitor types that become http monitors.
var betterStackHTTPTypes = map[string]bool{
	"status":               true,
	"expected_status_code": true,
	"keyword":              true,
	"keyword_absence":      true,
}

// betterStackSkipReasons explains, per monitor type, why there is nothing
// faithful to import.
var betterStackSkipReasons = map[string]string{
	"udp":        "UDP checks are not implemented",
	"smtp":       "an SMTP server check would degrade to a bare TCP connect",
	"pop":        "a POP3 server check would degrade to a bare TCP connect",
	"imap":       "an IMAP server check would degrade to a bare TCP connect",
	"playwright": "a Playwright scenario cannot be translated - recreate it as a synthetic_browser monitor with steps",
}

const (
	// betterStackDefaultFrequency is Better Stack's own default check
	// frequency, used when an export omits it.
	betterStackDefaultFrequency = 180

	betterStackRegionsNote       = "Better Stack regions were not migrated: Probara checks run from your own workers - assign locations after import"
	betterStackNotificationsNote = "Better Stack notification settings, escalation policies and on-call routing were not migrated - attach Probara alert channels after import"
	betterStackMaintenanceNote   = "Better Stack maintenance windows were not migrated - recreate them as Probara maintenance windows"
	betterStackPaginationNote    = "the Better Stack response links to a next page that is not in this file - fetch every page so no monitor is left out"
)

type betterStackDocument struct {
	Data       []betterStackResource `json:"data"`
	Included   []betterStackResource `json:"included"`
	Pagination *struct {
		Next *string `json:"next"`
	} `json:"pagination"`
}

type betterStackResource struct {
	ID         foreignID       `json:"id"`
	Type       string          `json:"type"`
	Attributes json.RawMessage `json:"attributes"`
}

type betterStackHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// betterStackHeaders accepts request_headers as the API's array of
// {name, value} objects or as a plain name -> value object.
type betterStackHeaders []betterStackHeader

func (h *betterStackHeaders) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*h = nil
		return nil
	}
	if trimmed[0] == '{' {
		var asMap map[string]string
		if err := json.Unmarshal(trimmed, &asMap); err != nil {
			return err
		}
		out := make(betterStackHeaders, 0, len(asMap))
		for _, name := range sortedKeys(asMap) {
			out = append(out, betterStackHeader{Name: name, Value: asMap[name]})
		}
		*h = out
		return nil
	}
	var arr []betterStackHeader
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return err
	}
	*h = arr
	return nil
}

// betterStackAlerting is the notification and escalation part every monitor
// and heartbeat carries; none of it is migrated.
type betterStackAlerting struct {
	Call               kumaBool  `json:"call"`
	SMS                kumaBool  `json:"sms"`
	Email              kumaBool  `json:"email"`
	Push               kumaBool  `json:"push"`
	CriticalAlert      kumaBool  `json:"critical_alert"`
	TeamWait           kumaInt   `json:"team_wait"`
	PolicyID           foreignID `json:"policy_id"`
	ExpirationPolicyID foreignID `json:"expiration_policy_id"`
}

func (a betterStackAlerting) configured() bool {
	return bool(a.Call) || bool(a.SMS) || bool(a.Email) || bool(a.Push) || bool(a.CriticalAlert) ||
		a.TeamWait > 0 || a.PolicyID != "" || a.ExpirationPolicyID != ""
}

type betterStackMonitor struct {
	betterStackAlerting
	URL                 string             `json:"url"`
	PronounceableName   string             `json:"pronounceable_name"`
	MonitorType         string             `json:"monitor_type"`
	MonitorGroupID      foreignID          `json:"monitor_group_id"`
	CheckFrequency      kumaInt            `json:"check_frequency"`
	RequestTimeout      kumaInt            `json:"request_timeout"`
	ConfirmationPeriod  kumaInt            `json:"confirmation_period"`
	RecoveryPeriod      kumaInt            `json:"recovery_period"`
	RequiredKeyword     string             `json:"required_keyword"`
	ExpectedStatusCodes []kumaInt          `json:"expected_status_codes"`
	HTTPMethod          string             `json:"http_method"`
	RequestBody         string             `json:"request_body"`
	RequestHeaders      betterStackHeaders `json:"request_headers"`
	AuthUsername        string             `json:"auth_username"`
	AuthPassword        string             `json:"auth_password"`
	VerifySSL           *bool              `json:"verify_ssl"`
	FollowRedirects     *bool              `json:"follow_redirects"`
	SSLExpiration       kumaInt            `json:"ssl_expiration"`
	DomainExpiration    kumaInt            `json:"domain_expiration"`
	// Port is a string for smtp/pop/imap ("25,465"), a number elsewhere.
	Port      foreignID `json:"port"`
	Paused    kumaBool  `json:"paused"`
	IPVersion string    `json:"ip_version"`
	// Read only for presence, so an unexpected shape cannot make the whole
	// record unreadable.
	Regions         json.RawMessage `json:"regions"`
	MaintenanceFrom json.RawMessage `json:"maintenance_from"`
	MaintenanceDays json.RawMessage `json:"maintenance_days"`
}

type betterStackHeartbeat struct {
	betterStackAlerting
	Name             string          `json:"name"`
	Period           kumaInt         `json:"period"`
	Grace            kumaInt         `json:"grace"`
	Paused           kumaBool        `json:"paused"`
	HeartbeatGroupID foreignID       `json:"heartbeat_group_id"`
	MaintenanceFrom  json.RawMessage `json:"maintenance_from"`
	MaintenanceDays  json.RawMessage `json:"maintenance_days"`
}

type betterStackGroup struct {
	Name   string   `json:"name"`
	Paused kumaBool `json:"paused"`
}

// parseBetterStackExport recognizes a Better Stack Uptime API response and
// translates it. The bool reports whether the payload was Better Stack's at
// all, so a non-match falls through to the generic JSON shapes.
func (s *Service) parseBetterStackExport(data []byte) (*parsedSource, bool, error) {
	pages, ok := probeBetterStack(data)
	if !ok {
		return nil, false, nil
	}

	// From here the payload is unambiguously Better Stack's.
	docs := make([]betterStackDocument, 0, len(pages))
	for _, page := range pages {
		var doc betterStackDocument
		if err := json.Unmarshal(page, &doc); err != nil {
			return nil, true, fmt.Errorf("invalid Better Stack export: %w", err)
		}
		docs = append(docs, doc)
	}
	return translateBetterStack(docs), true, nil
}

// probeBetterStack returns the JSON:API page documents when every resource
// in them is one of betterStackKinds with an attributes object, and there is
// at least one. A generic `{"data": [...]}` list of monitors fails the test.
func probeBetterStack(data []byte) ([]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, false
	}

	var pages []json.RawMessage
	switch trimmed[0] {
	case '{':
		pages = []json.RawMessage{trimmed}
	case '[':
		if err := json.Unmarshal(trimmed, &pages); err != nil || len(pages) == 0 {
			return nil, false
		}
	default:
		return nil, false
	}

	resources := 0
	for _, page := range pages {
		var probe struct {
			Data     []map[string]json.RawMessage `json:"data"`
			Included []map[string]json.RawMessage `json:"included"`
		}
		page = bytes.TrimSpace(page)
		if len(page) == 0 || page[0] != '{' || json.Unmarshal(page, &probe) != nil || probe.Data == nil {
			return nil, false
		}
		for _, resource := range append(probe.Data, probe.Included...) {
			var kind string
			if json.Unmarshal(resource["type"], &kind) != nil || !betterStackKinds[kind] {
				return nil, false
			}
			attributes := bytes.TrimSpace(resource["attributes"])
			if len(attributes) == 0 || attributes[0] != '{' {
				return nil, false
			}
			resources++
		}
	}
	return pages, resources > 0
}

func translateBetterStack(docs []betterStackDocument) *parsedSource {
	t := newForeignTranslation("Better Stack", models.ImportSchemaBetterStackExport)

	var resources []betterStackResource
	for _, doc := range docs {
		resources = append(resources, doc.Data...)
		resources = append(resources, doc.Included...)
		if len(docs) == 1 && doc.Pagination != nil && doc.Pagination.Next != nil && strings.TrimSpace(*doc.Pagination.Next) != "" {
			t.note(betterStackPaginationNote)
		}
	}

	// Groups first, so members can name the group they belong to.
	monitorGroups := make(map[string]string)
	heartbeatGroups := make(map[string]string)
	for _, resource := range resources {
		if resource.Type != "monitor_group" && resource.Type != "heartbeat_group" {
			continue
		}
		var group betterStackGroup
		if err := json.Unmarshal(resource.Attributes, &group); err != nil {
			t.skip("Better Stack "+strings.ReplaceAll(resource.Type, "_", " ")+" "+string(resource.ID), resource.Type, "the record could not be read")
			continue
		}
		name := strings.TrimSpace(group.Name)
		if name == "" {
			name = fmt.Sprintf("Better Stack group %s", resource.ID)
		}
		if resource.Type == "monitor_group" {
			monitorGroups[string(resource.ID)] = name
		} else {
			heartbeatGroups[string(resource.ID)] = name
		}
		t.declareGroup(name, !bool(group.Paused))
	}

	seen := make(map[string]bool)
	for _, resource := range resources {
		// The same resource can arrive twice (a page fetched twice, a group
		// in both data and included).
		key := resource.Type + "\x00" + string(resource.ID)
		if resource.ID != "" && seen[key] {
			continue
		}
		seen[key] = true

		switch resource.Type {
		case "monitor":
			var monitor betterStackMonitor
			if err := json.Unmarshal(resource.Attributes, &monitor); err != nil {
				t.skip("Better Stack monitor "+string(resource.ID), "monitor", "the record could not be read")
				continue
			}
			translateBetterStackMonitor(t, string(resource.ID), monitor, monitorGroups)
		case "heartbeat":
			var heartbeat betterStackHeartbeat
			if err := json.Unmarshal(resource.Attributes, &heartbeat); err != nil {
				t.skip("Better Stack heartbeat "+string(resource.ID), "heartbeat", "the record could not be read")
				continue
			}
			translateBetterStackHeartbeat(t, string(resource.ID), heartbeat, heartbeatGroups)
		}
	}

	return t.finish()
}

func betterStackMonitorName(id string, monitor betterStackMonitor) string {
	if name := strings.TrimSpace(monitor.PronounceableName); name != "" {
		return name
	}
	if url := strings.TrimSpace(monitor.URL); url != "" && !strings.Contains(url, "@") {
		return url
	}
	return "Better Stack monitor " + id
}

func translateBetterStackMonitor(t *foreignTranslation, id string, monitor betterStackMonitor, groups map[string]string) {
	name := betterStackMonitorName(id, monitor)
	kind := strings.ToLower(strings.TrimSpace(monitor.MonitorType))

	if kind == "" {
		t.skip(name, "monitor", "monitor has no type")
		return
	}
	if reason, ok := betterStackSkipReasons[kind]; ok {
		t.skip(name, kind, reason)
		return
	}

	m := &foreignMonitor{
		sourceID:   id,
		sourceType: kind,
		name:       name,
		enabled:    !bool(monitor.Paused),
		interval:   betterStackDefaultFrequency,
	}
	m.setInterval(int(monitor.CheckFrequency))

	var reason string
	switch {
	case betterStackHTTPTypes[kind]:
		m.target = models.MonitorTypeHTTP
		m.config, reason = betterStackHTTPConfig(m, monitor, kind)
		if reason == "" {
			m.timeout = int(monitor.RequestTimeout)
		}
	case kind == "ping":
		m.target = models.MonitorTypePing
		m.config, reason = betterStackHostConfig(monitor, false)
	case kind == "tcp":
		m.target = models.MonitorTypeTCP
		m.config, reason = betterStackHostConfig(monitor, true)
	case kind == "dns":
		m.target = models.MonitorTypeDNS
		m.config, reason = betterStackDNSConfig(m, monitor)
	default:
		reason = fmt.Sprintf("unrecognized Better Stack monitor type %q", kind)
	}
	if reason != "" {
		t.skip(name, kind, reason)
		return
	}
	if m.target != models.MonitorTypeHTTP {
		// Better Stack states server timeouts in milliseconds.
		if ms := int(monitor.RequestTimeout); ms > 0 {
			m.timeout = (ms + 999) / 1000
		}
	}

	// Better Stack opens an incident once a failure has lasted the
	// confirmation period and resolves it after the recovery period; here
	// both are counted in checks at the monitor's interval.
	confirmation := int(monitor.ConfirmationPeriod)
	m.setFailureThreshold(periodToChecks(confirmation, m.interval))
	if confirmation > 0 {
		m.warn("%s", betterStackPeriodNote("confirmation", confirmation, m.interval, m.failureThreshold, "failing"))
	}
	if recovery := int(monitor.RecoveryPeriod); recovery > 0 {
		m.setRecoveryThreshold(periodToChecks(recovery, m.interval))
		m.warn("%s", betterStackPeriodNote("recovery", recovery, m.interval, m.recoveryThreshold, "passing"))
	}

	if groupID := string(monitor.MonitorGroupID); groupID != "" {
		if group, ok := groups[groupID]; ok {
			m.group = group
		} else {
			m.warn("it belongs to Better Stack monitor group %s, which is not in this file - include GET /api/v2/monitor-groups to keep the grouping", groupID)
		}
	}

	if strings.TrimSpace(monitor.IPVersion) != "" {
		m.warn("the %s-only setting was dropped - Probara resolves the host normally", monitor.IPVersion)
	}
	betterStackNotes(t, monitor.betterStackAlerting, jsonPresent(monitor.Regions),
		jsonPresent(monitor.MaintenanceFrom) || jsonPresent(monitor.MaintenanceDays))
	t.add(m)

	if days := int(monitor.DomainExpiration); days > 0 {
		host := foreignHost(monitor.URL)
		if m.target == models.MonitorTypeDNS {
			host = m.config["host"].(string)
		}
		t.addDomainExpiry(m, host, days,
			"Probara raises a domain expiry alert inside the window while the check keeps passing, as Better Stack notifies")
	}
}

// betterStackPeriodNote words a time-based period that became a check count.
func betterStackPeriodNote(kind string, period, interval, checks int, state string) string {
	if wanted := periodToChecks(period, interval); wanted != checks {
		return fmt.Sprintf("Better Stack's %ds %s period spans %d checks at this interval; Probara's maximum of %d consecutive %s checks was used",
			period, kind, wanted, checks, state)
	}
	return fmt.Sprintf("Better Stack's %ds %s period became %d consecutive %s checks", period, kind, checks, state)
}

func betterStackNotes(t *foreignTranslation, alerting betterStackAlerting, regions, maintenance bool) {
	if regions {
		t.note(betterStackRegionsNote)
	}
	if alerting.configured() {
		t.note(betterStackNotificationsNote)
	}
	if maintenance {
		t.note(betterStackMaintenanceNote)
	}
}

func betterStackHTTPConfig(m *foreignMonitor, monitor betterStackMonitor, kind string) (map[string]interface{}, string) {
	url, urlUser, urlPassword, reason := parseForeignHTTPURL(monitor.URL)
	if reason != "" {
		return nil, reason
	}

	method := strings.ToUpper(strings.TrimSpace(monitor.HTTPMethod))
	if method == "" {
		method = "GET"
	}
	if !kumaHTTPMethods[method] {
		return nil, fmt.Sprintf("HTTP method %q is not supported", method)
	}

	config := map[string]interface{}{"url": url, "method": method}

	// Better Stack sends the body only with POST, PUT and PATCH.
	if body := monitor.RequestBody; strings.TrimSpace(body) != "" && (method == "POST" || method == "PUT" || method == "PATCH") {
		config["body"] = body
	}

	headers := make(map[string]string, len(monitor.RequestHeaders))
	for _, header := range monitor.RequestHeaders {
		headers[header.Name] = header.Value
	}
	setForeignHTTPHeaders(config, headers)

	user, password := monitor.AuthUsername, monitor.AuthPassword
	if strings.TrimSpace(user) == "" {
		user, password = urlUser, urlPassword
	}
	setForeignBasicAuth(m, config, user, password)

	https := isHTTPS(url)
	if monitor.VerifySSL != nil && !*monitor.VerifySSL {
		if https {
			config["tls_skip_verify"] = true
		} else {
			m.warn("the disabled SSL verification was dropped because the monitor targets a plain http:// URL")
		}
	}
	if monitor.FollowRedirects != nil && !*monitor.FollowRedirects {
		config["follow_redirects"] = false
	}
	if days := int(monitor.SSLExpiration); days > 0 {
		if https {
			// A notification at Better Stack, a tls_expiry alert here: the
			// check keeps passing in both.
			config["tls_min_days_valid"] = days
		} else {
			m.warn("the SSL expiration alert was dropped because the monitor targets a plain http:// URL")
		}
	}

	switch kind {
	case "expected_status_code":
		statuses := make([]int, 0, len(monitor.ExpectedStatusCodes))
		dropped := make([]string, 0)
		for _, code := range monitor.ExpectedStatusCodes {
			if code < 100 || code > 599 {
				dropped = append(dropped, strconv.Itoa(int(code)))
				continue
			}
			statuses = append(statuses, int(code))
		}
		if len(statuses) == 0 {
			return nil, "the expected status code monitor lists no valid status code"
		}
		config["expected_statuses"] = statuses
		if len(dropped) > 0 {
			m.warn("expected status codes %s are not HTTP status codes and were dropped", strings.Join(dropped, ", "))
		}
	case "keyword", "keyword_absence":
		keyword := monitor.RequiredKeyword
		if strings.TrimSpace(keyword) == "" {
			m.warn("the keyword check had no keyword set and was imported as a plain HTTP check")
			break
		}
		op := "contains"
		if kind == "keyword_absence" {
			op = "not_contains"
		}
		config["body_assertions"] = []map[string]interface{}{{"op": op, "value": keyword}}
	}

	return config, ""
}

// betterStackHostConfig builds a ping or tcp config from the url field,
// which holds a bare host for those types.
func betterStackHostConfig(monitor betterStackMonitor, needsPort bool) (map[string]interface{}, string) {
	host := foreignHost(monitor.URL)
	if host == "" {
		return nil, "monitor has no host"
	}
	config := map[string]interface{}{"host": host}
	if !needsPort {
		return config, ""
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(monitor.Port)))
	if err != nil || port < 1 || port > 65535 {
		return nil, "TCP monitors require a port, and this one has none"
	}
	config["port"] = port
	return config, ""
}

// betterStackDNSConfig: Better Stack's DNS monitor queries the server named
// in url for the domain in request_body.
func betterStackDNSConfig(m *foreignMonitor, monitor betterStackMonitor) (map[string]interface{}, string) {
	server := foreignHost(monitor.URL)
	if server == "" {
		return nil, "the DNS monitor names no DNS server"
	}
	domain := strings.TrimSuffix(strings.TrimSpace(monitor.RequestBody), ".")
	if domain == "" {
		return nil, "the DNS monitor names no domain to query"
	}
	config := map[string]interface{}{"host": domain, "record_type": "A", "nameserver": server}
	m.warn("the DNS check was imported as an A-record lookup of %s on %s; change the record type if Better Stack checked another", domain, server)
	if expected := strings.TrimSpace(monitor.RequiredKeyword); expected != "" {
		config["expected_answers"] = []string{expected}
		m.warn("the expected answer %q must be one of the A records returned; verify it matches what Better Stack checked", expected)
	}
	return config, ""
}

func translateBetterStackHeartbeat(t *foreignTranslation, id string, heartbeat betterStackHeartbeat, groups map[string]string) {
	name := strings.TrimSpace(heartbeat.Name)
	if name == "" {
		name = "Better Stack heartbeat " + id
	}
	period := int(heartbeat.Period)
	if period <= 0 {
		t.skip(name, "heartbeat", "the heartbeat has no expected period")
		return
	}

	m := &foreignMonitor{
		sourceID:   id,
		sourceType: "heartbeat",
		name:       name,
		target:     models.MonitorTypePush,
		enabled:    !bool(heartbeat.Paused),
		interval:   betterStackDefaultFrequency,
	}
	m.setInterval(period)
	grace := int(heartbeat.Grace)
	if grace < 0 {
		grace = 0
	}
	m.config = map[string]interface{}{
		"expected_interval_seconds": m.interval,
		"grace_period_seconds":      grace,
	}
	m.warn("Probara issues its own push token, so this heartbeat's URL is different from the Better Stack one - repoint whatever sends the heartbeat")

	if groupID := string(heartbeat.HeartbeatGroupID); groupID != "" {
		if group, ok := groups[groupID]; ok {
			m.group = group
		} else {
			m.warn("it belongs to Better Stack heartbeat group %s, which is not in this file - include GET /api/v2/heartbeat-groups to keep the grouping", groupID)
		}
	}

	betterStackNotes(t, heartbeat.betterStackAlerting, false,
		jsonPresent(heartbeat.MaintenanceFrom) || jsonPresent(heartbeat.MaintenanceDays))
	t.add(m)
}
