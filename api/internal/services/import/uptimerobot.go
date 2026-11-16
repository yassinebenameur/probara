package importservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// UptimeRobot migration support.
//
// UptimeRobot's dashboard has no full monitor export; its API v2
// getMonitors call returns every setting: `{"stat": "ok", "pagination":
// {...}, "monitors": [...]}`. This adapter accepts that response. Monitor
// types and most enums are integers there, which is also how the document
// is told apart from a generic `{"monitors": [...]}` list.

const (
	// uptimeRobotCertExpiryDays is what an enabled SSL expiry reminder
	// becomes. UptimeRobot's first reminder goes out 30 days ahead; here it
	// opens a tls_expiry alert while the check keeps passing, the same
	// notify-only behavior.
	uptimeRobotCertExpiryDays = 30

	// uptimeRobotDefaultInterval is UptimeRobot's default (5 minutes), used
	// when an export omits the interval.
	uptimeRobotDefaultInterval = 300

	uptimeRobotAlertContactsNote = "UptimeRobot alert contacts were not migrated - attach Probara alert channels after import"
	uptimeRobotMaintenanceNote   = "UptimeRobot maintenance windows were not migrated - recreate them as Probara maintenance windows"
)

// uptimeRobotMethods maps the http_method enum.
var uptimeRobotMethods = map[int]string{
	1: "HEAD", 2: "GET", 3: "POST", 4: "PUT", 5: "PATCH", 6: "DELETE", 7: "OPTIONS",
}

// uptimeRobotPorts maps a port monitor's predefined sub_type to its port;
// 99 means the custom `port` field.
var uptimeRobotPorts = map[int]int{1: 80, 2: 443, 3: 21, 4: 25, 5: 110, 6: 143}

type uptimeRobotResponse struct {
	Stat       string `json:"stat"`
	Pagination *struct {
		Offset kumaInt `json:"offset"`
		Total  kumaInt `json:"total"`
	} `json:"pagination"`
	Monitors []json.RawMessage `json:"monitors"`
}

// uptimeRobotHeaders accepts custom_http_headers as an object, or the empty
// array the API writes when there are none.
type uptimeRobotHeaders map[string]string

func (h *uptimeRobotHeaders) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		*h = nil
		return nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return err
	}
	out := make(uptimeRobotHeaders, len(raw))
	for name, value := range raw {
		out[name] = fmt.Sprint(value)
	}
	*h = out
	return nil
}

type uptimeRobotMonitor struct {
	ID                 foreignID          `json:"id"`
	FriendlyName       string             `json:"friendly_name"`
	URL                string             `json:"url"`
	Type               kumaInt            `json:"type"`
	SubType            foreignID          `json:"sub_type"`
	Port               foreignID          `json:"port"`
	KeywordType        kumaInt            `json:"keyword_type"`
	KeywordCaseType    kumaInt            `json:"keyword_case_type"`
	KeywordValue       string             `json:"keyword_value"`
	HTTPUsername       string             `json:"http_username"`
	HTTPPassword       string             `json:"http_password"`
	HTTPAuthType       kumaInt            `json:"http_auth_type"`
	HTTPMethod         kumaInt            `json:"http_method"`
	CustomHTTPHeaders  uptimeRobotHeaders `json:"custom_http_headers"`
	CustomHTTPStatuses string             `json:"custom_http_statuses"`
	PostType           kumaInt            `json:"post_type"`
	PostValue          json.RawMessage    `json:"post_value"`
	PostContentType    kumaInt            `json:"post_content_type"`
	Interval           kumaInt            `json:"interval"`
	Timeout            kumaInt            `json:"timeout"`
	Status             kumaInt            `json:"status"`
	IgnoreSSLErrors    kumaBool           `json:"ignore_ssl_errors"`
	SSL                *struct {
		IgnoreErrors         kumaBool `json:"ignore_errors"`
		DisableNotifications kumaBool `json:"disable_notifications"`
	} `json:"ssl"`
	AlertContacts json.RawMessage `json:"alert_contacts"`
	MWindows      json.RawMessage `json:"mwindows"`
}

// parseUptimeRobotExport recognizes an UptimeRobot getMonitors response and
// translates it. The bool reports whether the payload was UptimeRobot's.
func (s *Service) parseUptimeRobotExport(data []byte) (*parsedSource, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}

	var probe struct {
		Stat     string                       `json:"stat"`
		Error    json.RawMessage              `json:"error"`
		Monitors []map[string]json.RawMessage `json:"monitors"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, false, nil
	}
	if probe.Stat == "fail" && jsonPresent(probe.Error) && len(probe.Monitors) == 0 {
		return nil, true, fmt.Errorf("the file is an UptimeRobot API error response, not a monitor list - check the API key and call getMonitors again")
	}
	if !isUptimeRobotMonitorList(probe.Monitors) {
		return nil, false, nil
	}

	var response uptimeRobotResponse
	if err := json.Unmarshal(trimmed, &response); err != nil {
		return nil, true, fmt.Errorf("invalid UptimeRobot export: %w", err)
	}
	return translateUptimeRobot(response), true, nil
}

// isUptimeRobotMonitorList: every monitor has friendly_name and a numeric
// type, which a generic list (string types, "name") never has.
func isUptimeRobotMonitorList(monitors []map[string]json.RawMessage) bool {
	if len(monitors) == 0 {
		return false
	}
	for _, monitor := range monitors {
		if _, ok := monitor["friendly_name"]; !ok {
			return false
		}
		raw := strings.Trim(string(bytes.TrimSpace(monitor["type"])), `"`)
		if _, err := strconv.Atoi(raw); err != nil {
			return false
		}
	}
	return true
}

func translateUptimeRobot(response uptimeRobotResponse) *parsedSource {
	t := newForeignTranslation("UptimeRobot", models.ImportSchemaUptimeRobotExport)

	if p := response.Pagination; p != nil && int(p.Total) > int(p.Offset)+len(response.Monitors) {
		t.note(fmt.Sprintf("this file holds %d of the account's %d UptimeRobot monitors - call getMonitors with offset to fetch the rest",
			len(response.Monitors), int(p.Total)))
	}

	for i, raw := range response.Monitors {
		var monitor uptimeRobotMonitor
		if err := json.Unmarshal(raw, &monitor); err != nil {
			t.skip(fmt.Sprintf("UptimeRobot monitor %d", i+1), "", "the record could not be read")
			continue
		}
		translateUptimeRobotMonitor(t, monitor)
	}
	return t.finish()
}

func uptimeRobotName(monitor uptimeRobotMonitor) string {
	if name := strings.TrimSpace(monitor.FriendlyName); name != "" {
		return name
	}
	return "UptimeRobot monitor " + string(monitor.ID)
}

func uptimeRobotTypeName(kind int) string {
	switch kind {
	case 1:
		return "http"
	case 2:
		return "keyword"
	case 3:
		return "ping"
	case 4:
		return "port"
	case 5:
		return "heartbeat"
	}
	return strconv.Itoa(kind)
}

func translateUptimeRobotMonitor(t *foreignTranslation, monitor uptimeRobotMonitor) {
	name := uptimeRobotName(monitor)
	kind := int(monitor.Type)
	typeName := uptimeRobotTypeName(kind)

	m := &foreignMonitor{
		sourceID:   string(monitor.ID),
		sourceType: typeName,
		name:       name,
		// Status 0 is "paused"; every other value is a live monitor.
		enabled:  int(monitor.Status) != 0,
		interval: uptimeRobotDefaultInterval,
	}
	m.setInterval(int(monitor.Interval))
	m.timeout = int(monitor.Timeout)

	var reason string
	switch kind {
	case 1, 2:
		m.target = models.MonitorTypeHTTP
		m.config, reason = uptimeRobotHTTPConfig(m, monitor, kind == 2)
	case 3:
		m.target = models.MonitorTypePing
		m.config, reason = uptimeRobotHostConfig(monitor, false)
	case 4:
		m.target = models.MonitorTypeTCP
		m.config, reason = uptimeRobotHostConfig(monitor, true)
	case 5:
		m.target = models.MonitorTypePush
		m.timeout = 0
		m.config = map[string]interface{}{
			"expected_interval_seconds": m.interval,
			"grace_period_seconds":      0,
		}
		m.warn("Probara issues its own push token, so this heartbeat's URL is different from the UptimeRobot one - repoint whatever sends the heartbeat")
	default:
		reason = fmt.Sprintf("unrecognized UptimeRobot monitor type %d", kind)
	}
	if reason != "" {
		t.skip(name, typeName, reason)
		return
	}

	if jsonPresent(monitor.AlertContacts) {
		t.note(uptimeRobotAlertContactsNote)
	}
	if jsonPresent(monitor.MWindows) {
		t.note(uptimeRobotMaintenanceNote)
	}
	t.add(m)
}

func uptimeRobotHTTPConfig(m *foreignMonitor, monitor uptimeRobotMonitor, keyword bool) (map[string]interface{}, string) {
	url, urlUser, urlPassword, reason := parseForeignHTTPURL(monitor.URL)
	if reason != "" {
		return nil, reason
	}

	method := "GET"
	if code := int(monitor.HTTPMethod); code != 0 {
		known, ok := uptimeRobotMethods[code]
		if !ok {
			return nil, fmt.Sprintf("HTTP method %d is not recognized", code)
		}
		method = known
	}
	config := map[string]interface{}{"url": url, "method": method}

	headers := map[string]string(monitor.CustomHTTPHeaders)
	if headers == nil {
		headers = make(map[string]string)
	}
	if method != "GET" && method != "HEAD" {
		if body, ok := uptimeRobotBody(m, monitor.PostValue); ok {
			config["body"] = body
			if int(monitor.PostContentType) == 1 && !hasHeader(headers, "Content-Type") {
				headers["Content-Type"] = "application/json"
			}
		}
	}
	setForeignHTTPHeaders(config, headers)

	user, password := monitor.HTTPUsername, monitor.HTTPPassword
	if strings.TrimSpace(user) == "" {
		user, password = urlUser, urlPassword
	}
	if strings.TrimSpace(user) != "" && int(monitor.HTTPAuthType) == 2 {
		return nil, "HTTP digest authentication is not supported - Probara's HTTP monitor sends basic auth only"
	}
	setForeignBasicAuth(m, config, user, password)

	https := isHTTPS(url)
	ignoreErrors := bool(monitor.IgnoreSSLErrors) || (monitor.SSL != nil && bool(monitor.SSL.IgnoreErrors))
	if ignoreErrors && https {
		config["tls_skip_verify"] = true
	}
	if https && monitor.SSL != nil && !bool(monitor.SSL.DisableNotifications) {
		config["tls_min_days_valid"] = uptimeRobotCertExpiryDays
		m.warn("UptimeRobot's SSL expiry reminders became one TLS expiry alert, opened %d days before the certificate expires", uptimeRobotCertExpiryDays)
	}

	if statuses := strings.TrimSpace(monitor.CustomHTTPStatuses); statuses != "" {
		set, warning := uptimeRobotStatusSet(statuses)
		if warning != "" {
			m.warn("%s", warning)
		}
		if set.empty() {
			return nil, "the custom HTTP statuses mark every status code as down"
		}
		if !set.isDefault2xx() {
			set.apply(config)
		}
	}

	if keyword {
		value := monitor.KeywordValue
		if strings.TrimSpace(value) == "" {
			m.warn("the keyword check had no keyword set and was imported as a plain HTTP check")
			return config, ""
		}
		// keyword_type says when UptimeRobot flags the monitor down: 1 when
		// the keyword exists, 2 when it does not.
		var op string
		switch int(monitor.KeywordType) {
		case 1:
			op = "not_contains"
		case 2:
			op = "contains"
		default:
			return nil, "the keyword monitor does not say whether the keyword must be present or absent"
		}
		assertion := map[string]interface{}{"op": op, "value": value}
		// keyword_case_type 0 is UptimeRobot's case-insensitive default.
		if int(monitor.KeywordCaseType) == 0 {
			assertion["case_insensitive"] = true
		}
		config["body_assertions"] = []map[string]interface{}{assertion}
	}

	return config, ""
}

// uptimeRobotBody reads post_value: raw text is sent as is, key-value data
// as a JSON object.
func uptimeRobotBody(m *foreignMonitor, raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if !jsonPresent(trimmed) {
		return "", false
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil || strings.TrimSpace(text) == "" {
			return "", false
		}
		return text, true
	}
	m.warn("the key-value request body was imported as a JSON object; check it matches what the endpoint expects")
	return string(trimmed), true
}

func hasHeader(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// uptimeRobotStatusSet applies custom_http_statuses ("404:1_500:0": code,
// then 1 for up or 0 for down) on top of UptimeRobot's default, where 2xx
// and 3xx responses are up.
func uptimeRobotStatusSet(spec string) (statusSet, string) {
	var set statusSet
	for code := 200; code <= 399; code++ {
		set.set(code, true)
	}
	var unreadable []string
	for _, entry := range strings.Split(spec, "_") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		codeText, upText, ok := strings.Cut(entry, ":")
		code, codeErr := strconv.Atoi(strings.TrimSpace(codeText))
		if !ok || codeErr != nil || code < 100 || code > 599 || (upText != "0" && upText != "1") {
			unreadable = append(unreadable, entry)
			continue
		}
		set.set(code, upText == "1")
	}
	if len(unreadable) > 0 {
		return set, fmt.Sprintf("custom HTTP statuses %s were not understood and were dropped", strings.Join(unreadable, ", "))
	}
	return set, ""
}

// uptimeRobotHostConfig builds a ping or tcp config; url holds the host.
func uptimeRobotHostConfig(monitor uptimeRobotMonitor, needsPort bool) (map[string]interface{}, string) {
	host := foreignHost(monitor.URL)
	if host == "" {
		return nil, "monitor has no host"
	}
	config := map[string]interface{}{"host": host}
	if !needsPort {
		return config, ""
	}
	subType, _ := strconv.Atoi(strings.TrimSpace(string(monitor.SubType)))
	port, known := uptimeRobotPorts[subType]
	if !known {
		port, _ = strconv.Atoi(strings.TrimSpace(string(monitor.Port)))
	}
	if port < 1 || port > 65535 {
		return nil, "TCP monitors require a port, and this one has none"
	}
	config["port"] = port
	return config, ""
}
